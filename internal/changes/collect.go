package changes

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
	"golang.org/x/mod/modfile"
)

type SideModule struct {
	SnapshotID string `json:"snapshotID,omitempty"`
	Module     string `json:"module,omitempty"`
	Error      string `json:"error,omitempty"`
	Absent     bool   `json:"absent,omitempty"`
	Partial    bool   `json:"partial,omitempty"`
}

type Module struct {
	ID     string     `json:"id"`
	Path   string     `json:"path"`
	Before SideModule `json:"before"`
	After  SideModule `json:"after"`
}

type Comparison struct {
	ID              string             `json:"id"`
	Directory       string             `json:"directory"`
	Revisions       Resolved           `json:"revisions"`
	Scope           review.ReviewScope `json:"scope"`
	Environment     map[string]string  `json:"environment"`
	CreatedAt       time.Time          `json:"createdAt"`
	Files           []FileChange       `json:"files"`
	Modules         []Module           `json:"modules"`
	Limitations     []string           `json:"limitations"`
	Records         []Record           `json:"records"`
	Flows           []Flow             `json:"flows"`
	Correspondences []Correspondence   `json:"correspondences"`
}

type Progress struct {
	Phase  string `json:"phase"`
	Side   string `json:"side,omitempty"`
	Module string `json:"module,omitempty"`
}

func Collect(ctx context.Context, r Resolved, storage string, scope review.ReviewScope, progress func(Progress)) (_ *Comparison, err error) {
	scope.VerifyRepository = false
	scope.UnitSelections = nil
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(storage, "comparison-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = cleanupError(err, directory)
		}
	}()
	notify := func(phase, side, module string) {
		if progress != nil {
			progress(Progress{Phase: phase, Side: side, Module: module})
		}
	}
	notify("Inventory", "", "")
	files, err := Inventory(ctx, r)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{"GOWORK": "off", "GOENV": "off", "GOFLAGS": "", "CGO_ENABLED": "0", "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "GOTOOLCHAIN": "local"}
	c := &Comparison{Directory: directory, Revisions: r, Scope: scope, Environment: environment, Files: files,
		Modules: []Module{}, CreatedAt: time.Now().UTC(), Limitations: []string{
			"Static revision analysis runs Go tooling/export preparation and may download dependencies; it never runs target tests, hooks, verification scripts or applications.",
			"CGO is disabled and revision workspaces are not activated. Inactive build tags and unresolved dispatch remain limitations.",
			"Source-supported reachability is potential impact, not proof of changed runtime behavior or correctness.",
		}}
	for _, name := range []string{"blobs", "patches", "reports"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0700); err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		if file.Content == "lfs-pointer" {
			c.Limitations = append(c.Limitations, "LFS pointer retained without downloading payload: "+file.Before.Path+" -> "+file.After.Path)
		}
		for _, object := range []Object{file.Before, file.After} {
			if object.Path == "" || object.Mode == "160000" {
				continue
			}
			bytes, err := blobSize(ctx, r, object)
			if err != nil {
				return nil, err
			}
			if bytes > 16<<20 {
				c.Limitations = append(c.Limitations, "Blob exceeds retained 16 MiB limit: "+object.Path)
				continue
			}
			data, err := r.git(ctx, "cat-file", "blob", object.OID)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(directory, "blobs", object.OID), data, 0600); err != nil {
				return nil, err
			}
		}
		patch, _, err := filePatch(ctx, r, file, 3)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(directory, "patches", file.ID), patch, 0600); err != nil {
			return nil, err
		}
	}
	modules := map[string]*Module{}
	for _, side := range []string{"before", "after"} {
		commit := r.Before.Commit
		if side == "after" {
			commit = r.After.Commit
		}
		notify("Retaining revision", side, "")
		root := filepath.Join(directory, side)
		limits, err := materialize(ctx, r, commit, root)
		if err != nil {
			return nil, err
		}
		for _, limit := range limits {
			c.Limitations = append(c.Limitations, side+": "+limit)
		}
		paths, err := modulePaths(root)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			module := modules[path]
			if module == nil {
				module = &Module{ID: identity("module", path), Path: path, Before: SideModule{Absent: true}, After: SideModule{Absent: true}}
				modules[path] = module
			}
			result := SideModule{}
			dir := filepath.Join(root, filepath.FromSlash(path))
			notify("Static analysis", side, path)
			if safetyErr := validateReplacements(root, dir); safetyErr != nil {
				result.Error = safetyErr.Error()
			} else {
				sideScope := scope
				sideScope.Root = root
				sideScope.VerifyRepository = false
				sideScope.UnitSelections = nil
				report, analysisErr := review.Analyze(ctx, review.Options{Dir: dir, DocumentationDir: root, Scope: &sideScope, Environment: environment})
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if analysisErr != nil {
					result.Error = analysisErr.Error()
				} else {
					report.Revision = review.Revision{Commit: commit, Available: true}
					for _, pkg := range report.Packages {
						result.Partial = result.Partial || len(pkg.TypeErrors) > 0
					}
					result.Module = report.Module
					result.SnapshotID, err = retainedSideIdentity(side, commit, module.ID, report, scope, environment, directory)
					if err != nil {
						return nil, err
					}
					data, err := json.Marshal(report)
					if err != nil {
						return nil, err
					}
					if err := os.WriteFile(filepath.Join(directory, "reports", side+"-"+module.ID+".json"), data, 0600); err != nil {
						return nil, err
					}
				}
			}
			if side == "before" {
				module.Before = result
			} else {
				module.After = result
			}
		}
	}
	for _, module := range modules {
		c.Modules = append(c.Modules, *module)
	}
	sort.Slice(c.Modules, func(i, j int) bool { return c.Modules[i].Path < c.Modules[j].Path })
	scope.Root = r.Repository
	c.Scope = scope
	revisionIdentity := r
	revisionIdentity.Before.Label = ""
	revisionIdentity.After.Label = ""
	if r.Target != nil {
		target := *r.Target
		target.Label = ""
		revisionIdentity.Target = &target
	}
	moduleIdentity := make([]Module, len(c.Modules))
	for i, module := range c.Modules {
		module.Before.Error = strings.ReplaceAll(module.Before.Error, directory, "<comparison>")
		module.After.Error = strings.ReplaceAll(module.After.Error, directory, "<comparison>")
		moduleIdentity[i] = module
	}
	identityData, err := json.Marshal(struct {
		Revisions    Resolved
		Scope        review.ReviewScope
		Environment  map[string]string
		Modules      []Module
		Policy, Tool string
	}{revisionIdentity, scope, environment, moduleIdentity, Policy, runtime.Version()})
	if err != nil {
		return nil, err
	}
	c.ID = identity(string(identityData))
	if err := c.BuildContext(); err != nil {
		return nil, err
	}
	notify("Ready", "", "")
	return c, nil
}

func cleanupError(cause error, directory string) error {
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("%w; clean owned revision inputs: %v", cause, err)
	}
	return cause
}

func modulePaths(root string) ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			path, err := filepath.Rel(root, filepath.Dir(name))
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func validateReplacements(root, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("revision module: %w", err)
	}
	for _, replacement := range mod.Replace {
		if replacement.New.Version != "" {
			continue
		}
		path := replacement.New.Path
		if filepath.IsAbs(path) {
			return fmt.Errorf("revision-local replacement must not use a host absolute path: %s", path)
		}
		path = filepath.Join(dir, filepath.FromSlash(path))
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("local replacement escapes retained revision: %s", replacement.New.Path)
		}
		if _, err := os.Stat(filepath.Join(path, "go.mod")); err != nil {
			return fmt.Errorf("local replacement unavailable in retained revision %s: %w", replacement.New.Path, err)
		}
	}
	return nil
}

func (c *Comparison) Report(moduleID, side string) (*review.Report, error) {
	if side != "before" && side != "after" {
		return nil, fmt.Errorf("side must be before or after")
	}
	for _, module := range c.Modules {
		if module.ID != moduleID {
			continue
		}
		result := module.Before
		if side == "after" {
			result = module.After
		}
		if result.Absent {
			return nil, fmt.Errorf("module is absent on %s", side)
		}
		if result.Error != "" {
			return nil, fmt.Errorf("%s module unavailable: %s", side, result.Error)
		}
		data, err := os.ReadFile(filepath.Join(c.Directory, "reports", side+"-"+module.ID+".json"))
		if err != nil {
			return nil, fmt.Errorf("read retained side evidence: %w", err)
		}
		var report review.Report
		if err := json.Unmarshal(data, &report); err != nil {
			return nil, fmt.Errorf("decode retained side evidence: %w", err)
		}
		return &report, nil
	}
	return nil, fmt.Errorf("unknown comparison module")
}
