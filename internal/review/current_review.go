package review

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var currentReviewMu sync.Mutex
var managedRunName = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}-|unit-evidence-|analysis-)`)

func reviewStorage() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate review storage: %w", err)
	}
	return filepath.Join(cache, "go-review", "runs"), nil
}

// SaveCurrentReview publishes one self-contained review per repository. Only
// direct, non-symlink run directories identifying the same repository are pruned.
// In-progress collection directories without a manifest are never touched.
func SaveCurrentReview(run *RepositoryReview) error {
	currentReviewMu.Lock()
	defer currentReviewMu.Unlock()
	if run == nil || run.Root == "" || !filepath.IsAbs(run.Directory) || !managedRunName.MatchString(filepath.Base(run.Directory)) {
		return fmt.Errorf("current review requires a repository and an owned artifact directory")
	}
	info, err := os.Lstat(run.Directory)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("current review directory must be a regular directory: %s: %v", run.Directory, err)
	}
	if err := consolidateCurrentArtifacts(run); err != nil {
		return err
	}
	for _, module := range run.Modules {
		if module.Report != nil {
			module.Report.Review = &ReviewReference{Root: run.Root, Directory: run.Directory, Status: run.Status,
				StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, ModuleCount: len(run.Modules)}
		}
	}
	data, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode current review: %w", err)
	}
	if err := os.WriteFile(filepath.Join(run.Directory, "review.json"), data, 0600); err != nil {
		return fmt.Errorf("save current review: %w", err)
	}
	storage := filepath.Dir(run.Directory)
	entries, err := os.ReadDir(storage)
	if err != nil {
		return fmt.Errorf("list superseded review evidence: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !managedRunName.MatchString(entry.Name()) {
			continue
		}
		directory := filepath.Join(storage, entry.Name())
		if directory == filepath.Clean(run.Directory) {
			continue
		}
		root, previousDirectory, err := reviewIdentity(filepath.Join(directory, "review.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect superseded review %s: %w", directory, err)
		}
		if root != run.Root || filepath.Clean(previousDirectory) != directory {
			continue
		}
		if err := os.RemoveAll(directory); err != nil {
			return fmt.Errorf("remove superseded review evidence %s: %w", directory, err)
		}
	}
	return nil
}

// Root and directory precede the potentially multi-gigabyte module payload.
// Bound identity inspection so cleanup never loads old source/trace evidence.
func reviewIdentity(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	d := json.NewDecoder(io.LimitReader(file, 64*1024))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", fmt.Errorf("invalid review identity: %v", err)
	}
	values := map[string]string{}
	for len(values) < 2 && d.More() {
		key, err := d.Token()
		if err != nil {
			return "", "", err
		}
		if key != "root" && key != "directory" {
			return "", "", fmt.Errorf("review identity must precede evidence")
		}
		var value string
		if err := d.Decode(&value); err != nil {
			return "", "", err
		}
		values[key.(string)] = value
	}
	if values["root"] == "" || values["directory"] == "" {
		return "", "", fmt.Errorf("incomplete review identity")
	}
	return values["root"], values["directory"], nil
}

// Copy only files referenced by the current module snapshots, not old manifests
// or replaced test selections. Published snapshots are copied by the caller.
func consolidateCurrentArtifacts(run *RepositoryReview) error {
	for i := range run.Modules {
		report := run.Modules[i].Report
		if report == nil || report.UnitEvidence == nil || inside(run.Directory, report.UnitEvidence.Directory) {
			continue
		}
		evidence := report.UnitEvidence
		destination := filepath.Join(run.Directory, fmt.Sprintf("module-%d", i+1))
		oldDirectory := evidence.Directory
		paths := map[string]string{}
		copyPath := func(path *string, incomplete bool) error {
			if *path == "" {
				return nil
			}
			if !inside(oldDirectory, *path) {
				return fmt.Errorf("unit artifact is outside its evidence directory: %s", *path)
			}
			relative, err := filepath.Rel(oldDirectory, *path)
			if err != nil {
				return err
			}
			target := filepath.Join(destination, relative)
			data, err := os.ReadFile(*path)
			if os.IsNotExist(err) && incomplete {
				evidence.Limitations = append(evidence.Limitations, "Artifact unavailable from incomplete test execution: "+*path)
				*path = ""
				return nil
			}
			if err != nil {
				return fmt.Errorf("carry current unit artifact %s: %w", *path, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0600); err != nil {
				return err
			}
			paths[*path], *path = target, target
			return nil
		}
		for j := range evidence.Runs {
			unit := &evidence.Runs[j]
			for _, path := range []*string{&unit.EventsPath, &unit.CoveragePath, &unit.RuntimeTracePath} {
				if err := copyPath(path, unit.Status != "pass"); err != nil {
					return err
				}
			}
			if unit.Runtime != nil {
				if err := copyPath(&unit.Runtime.ParsedPath, unit.Status != "pass"); err != nil {
					return err
				}
			}
		}
		if report.Coverage != nil && inside(oldDirectory, report.Coverage.Origin) {
			if err := copyPath(&report.Coverage.Origin, false); err != nil {
				return err
			}
		}
		evidence.Directory = destination
		remap := func(commands []Command) {
			for i := range commands {
				for j, arg := range commands[i].Args {
					for old, current := range paths {
						if arg == old {
							commands[i].Args[j] = current
						} else if flag, value, ok := strings.Cut(arg, "="); ok && value == old {
							commands[i].Args[j] = flag + "=" + current
						}
					}
				}
			}
		}
		remap(report.Commands)
		for j := range evidence.Runs {
			remap(evidence.Runs[j].Commands)
		}
		for j := range run.Steps {
			if run.Steps[j].Module == report.Root {
				remap(run.Steps[j].Commands)
			}
		}
	}
	return nil
}

// SaveAnalysis replaces old execution evidence with a fresh static snapshot.
func SaveAnalysis(report *Report) (*RepositoryReview, error) {
	storage, err := reviewStorage()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(storage, "analysis-")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	run := &RepositoryReview{Root: report.Scope.Root, Directory: directory, Status: "analyzed", StartedAt: now, FinishedAt: now, Scope: report.Scope,
		Steps: []ReviewStep{}, Modules: []ReviewedModule{{Directory: report.Root, Report: report}}}
	return run, SaveCurrentReview(run)
}
