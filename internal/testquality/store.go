package testquality

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

type Store struct{ Directory string }

var assessmentID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func Digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func DefaultStore() (Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return Store{}, fmt.Errorf("test quality storage: %w", err)
	}
	return Store{Directory: filepath.Join(dir, "go-review", "test-quality")}, nil
}

// Create owns independent report and artifact copies: replacing the current
// repository review may prune the original evidence while this job is running.
func (s Store) Create(ctx context.Context, run *review.RepositoryReview, provider, model string) (_ *Assessment, err error) {
	if run == nil || run.Root == "" {
		return nil, fmt.Errorf("retained project review required")
	}
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	a := &Assessment{ID: hex.EncodeToString(id), Version: RubricVersion, Root: run.Root, ReviewID: run.Directory,
		Status: "queued", Provider: provider, Model: model, CreatedAt: time.Now().UTC(), Items: []Responsibility{}}
	dir := filepath.Join(s.Directory, a.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if cleanup := os.RemoveAll(dir); cleanup != nil {
				err = fmt.Errorf("%w; clean assessment %s: %v", err, dir, cleanup)
			}
		}
	}()
	for i, m := range run.Modules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.Report == nil {
			a.Unavailable = append(a.Unavailable, m.Directory+": "+m.Error)
			continue
		}
		raw, err := json.Marshal(m.Report)
		if err != nil {
			return nil, err
		}
		var r review.Report
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		snapshot := Digest(raw)
		if err := validateSources(&r); err != nil {
			return nil, err
		}
		if r.UnitEvidence != nil {
			original := r.UnitEvidence.Directory
			dest := filepath.Join(dir, fmt.Sprintf("module-%d", i))
			copyPath := func(path *string) error {
				if *path == "" {
					return nil
				}
				if !contained(original, *path) {
					return fmt.Errorf("unit artifact escapes retained directory: %s", *path)
				}
				real, err := filepath.EvalSymlinks(*path)
				if err != nil {
					return fmt.Errorf("resolve retained artifact %s: %w", *path, err)
				}
				base, err := filepath.EvalSymlinks(original)
				if err != nil {
					return err
				}
				if !contained(base, real) {
					return fmt.Errorf("retained artifact symlink escapes owner: %s", *path)
				}
				data, err := os.ReadFile(real)
				if err != nil {
					return fmt.Errorf("pin artifact %s: %w", *path, err)
				}
				relative, err := filepath.Rel(original, *path)
				if err != nil {
					return err
				}
				target := filepath.Join(dest, relative)
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					return err
				}
				if err := os.WriteFile(target, data, 0600); err != nil {
					return err
				}
				*path = target
				a.Artifacts = append(a.Artifacts, Artifact{Path: target, SHA256: Digest(data)})
				return nil
			}
			for j := range r.UnitEvidence.Runs {
				u := &r.UnitEvidence.Runs[j]
				for _, path := range []*string{&u.EventsPath, &u.CoveragePath, &u.RuntimeTracePath} {
					if err := copyPath(path); err != nil {
						return nil, err
					}
				}
				if u.Runtime != nil {
					if err := copyPath(&u.Runtime.ParsedPath); err != nil {
						return nil, err
					}
				}
			}
			if r.Coverage != nil && contained(original, r.Coverage.Origin) {
				if err := copyPath(&r.Coverage.Origin); err != nil {
					return nil, err
				}
			}
			r.UnitEvidence.Directory = dest
		}
		pinnedJSON, err := json.Marshal(&r)
		if err != nil {
			return nil, err
		}
		a.Reports = append(a.Reports, PinnedReport{SnapshotID: snapshot, SHA256: Digest(pinnedJSON), Report: &r})
		// Symbol presence is not proof of an assessable body (for example
		// assembly-backed declarations). Keep these in scope as insufficient.
		bodies := map[string]map[int]bool{}
		for _, file := range r.Files {
			set := token.NewFileSet()
			parsed, parseErr := parser.ParseFile(set, file.Path, file.Source, 0)
			bodies[file.ID] = map[int]bool{}
			if parseErr != nil {
				continue
			}
			for _, decl := range parsed.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					bodies[file.ID][set.Position(fn.Pos()).Line] = fn.Body != nil
				}
			}
		}
		for _, symbol := range r.Symbols {
			if symbol.Kind != "function" && symbol.Kind != "method" {
				continue
			}
			var file *review.File
			for j := range r.Files {
				if r.Files[j].ID == symbol.FileID {
					file = &r.Files[j]
					break
				}
			}
			testSource := false
			for _, test := range r.TestSources {
				if test.File.ID == symbol.FileID {
					testSource = true
					break
				}
			}
			if testSource || file != nil && strings.HasSuffix(file.Path, "_test.go") {
				continue
			}
			item := Responsibility{Identity: Identity{SnapshotID: snapshot, Module: r.Module, SymbolID: symbol.ID},
				Name: symbol.Name, Package: symbol.Package, Status: StatusUnreviewed}
			if file == nil || !bodies[symbol.FileID][symbol.Line] {
				item.Status = StatusInsufficient
				item.Reason = "Declaration source body is unavailable."
			}
			a.Items = append(a.Items, item)
		}
	}
	if _, err := Aggregate(a.Items); err != nil {
		return nil, err
	}
	if err := s.Save(a); err != nil {
		return nil, err
	}
	return a, nil
}

func contained(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validateSources(r *review.Report) error {
	files := append(append([]review.File{}, r.Files...), r.SupportingFiles...)
	for _, test := range r.TestSources {
		files = append(files, test.File)
	}
	for _, file := range files {
		if file.Path == "" || filepath.IsAbs(file.Path) || !contained("/retained", filepath.Join("/retained", file.Path)) {
			return fmt.Errorf("invalid retained source path %q", file.Path)
		}
		if Digest([]byte(file.Source)) != file.SHA256 {
			return fmt.Errorf("source digest mismatch: %s", file.Path)
		}
	}
	return nil
}

// Save publishes a checkpoint atomically, never replacing valid state with an
// incomplete JSON write. Callers own serialization between saves for one job.
func (s Store) Save(a *Assessment) error {
	if !assessmentID.MatchString(a.ID) {
		return fmt.Errorf("invalid assessment ID")
	}
	if _, err := Aggregate(a.Items); err != nil {
		return err
	}
	a.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Directory, a.ID)
	f, err := os.CreateTemp(dir, "checkpoint-")
	if err != nil {
		return fmt.Errorf("create assessment checkpoint: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write assessment: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, "assessment.json")); err != nil {
		return fmt.Errorf("publish assessment: %w", err)
	}
	return nil
}

func (s Store) Load(id string) (*Assessment, error) {
	if !assessmentID.MatchString(id) {
		return nil, fmt.Errorf("invalid assessment ID")
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, id, "assessment.json"))
	if err != nil {
		return nil, fmt.Errorf("load assessment: %w", err)
	}
	var a Assessment
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("decode assessment: %w", err)
	}
	if a.ID != id || a.Version != RubricVersion {
		return nil, fmt.Errorf("assessment identity or rubric version mismatch")
	}
	if _, err := Aggregate(a.Items); err != nil {
		return nil, err
	}
	for _, r := range a.Reports {
		if r.Report == nil {
			return nil, fmt.Errorf("missing pinned report")
		}
		if err := validateSources(r.Report); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(r.Report)
		if err != nil {
			return nil, err
		}
		if Digest(raw) != r.SHA256 {
			return nil, fmt.Errorf("pinned report digest mismatch")
		}
	}
	for _, artifact := range a.Artifacts {
		owner := filepath.Join(s.Directory, id)
		real, err := filepath.EvalSymlinks(artifact.Path)
		if err != nil {
			return nil, fmt.Errorf("retained artifact unavailable: %w", err)
		}
		base, err := filepath.EvalSymlinks(owner)
		if err != nil {
			return nil, err
		}
		if !contained(base, real) {
			return nil, fmt.Errorf("artifact escapes assessment owner")
		}
		file, err := os.Open(real)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
			return nil, fmt.Errorf("retained artifact digest mismatch: %s", artifact.Path)
		}
	}
	return &a, nil
}

func (s Store) List(root string) ([]*Assessment, error) {
	entries, err := os.ReadDir(s.Directory)
	if os.IsNotExist(err) {
		return []*Assessment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Assessment
	for _, e := range entries {
		if !e.IsDir() || !assessmentID.MatchString(e.Name()) {
			continue
		}
		a, err := s.Load(e.Name())
		if err != nil {
			return nil, err
		}
		if root == "" || a.Root == root {
			out = append(out, a)
		}
	}
	return out, nil
}

// Recover does not resume model calls or claim unfinished work succeeded.
func (s Store) Recover() error {
	list, err := s.List("")
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Status == "running" || a.Status == "queued" {
			a.Status = "interrupted"
			a.Error = "Workbench stopped before this assessment finished. Start a new assessment to review remaining scope."
			if err := s.Save(a); err != nil {
				return err
			}
		}
	}
	return nil
}
