package testquality

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	a := &Assessment{ID: hex.EncodeToString(id), Version: EvidenceRubricVersion, Root: run.Root, ReviewID: run.Directory,
		Status: "queued", Provider: provider, Model: model, CreatedAt: time.Now().UTC(), Items: []Responsibility{}}
	staging, err := os.MkdirTemp(s.Directory, ".preparing-")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(staging, a.ID)
	finalDir := filepath.Join(s.Directory, a.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, errors.Join(err, os.Remove(staging))
	}
	defer func() {
		if cleanup := os.RemoveAll(staging); cleanup != nil {
			err = errors.Join(err, fmt.Errorf("clean assessment staging %s: %w", staging, cleanup))
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
			finalDest := filepath.Join(finalDir, fmt.Sprintf("module-%d", i))
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
				*path = filepath.Join(finalDest, relative)
				a.Artifacts = append(a.Artifacts, Artifact{Path: *path, SHA256: Digest(data)})
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
			r.UnitEvidence.Directory = finalDest
		}
		pinnedJSON, err := json.Marshal(&r)
		if err != nil {
			return nil, err
		}
		a.Reports = append(a.Reports, PinnedReport{SnapshotID: snapshot, SHA256: Digest(pinnedJSON), Report: &r})
		for _, source := range r.TestSources {
			for _, test := range source.Declarations {
				if test.Name == "TestMain" || test.Kind != "test" && test.Kind != "example" && test.Kind != "fuzz" {
					continue
				}
				item := Responsibility{
					Identity: Identity{SnapshotID: snapshot, Module: r.Module, SymbolID: test.ID},
					Name:     test.Name, Package: source.File.Package, Status: StatusUnreviewed, TestKind: test.Kind,
					Declaration: Citation{SnapshotID: snapshot, FileID: source.File.ID, StartLine: test.Location.Line, EndLine: test.Location.EndLine},
				}
				for _, excluded := range source.ExcludedRanges {
					if test.Location.Line >= excluded.Line && test.Location.EndLine <= excluded.EndLine {
						item.Status = StatusExcluded
						item.Reason = "Test excluded by the retained review scope."
					}
				}
				if item.Status != StatusExcluded && (test.Location.Line < 1 || test.Location.EndLine < test.Location.Line) {
					item.Status = StatusInsufficient
					item.Reason = "Retained test declaration source is unavailable."
				}
				a.Items = append(a.Items, item)
			}
		}
	}
	if _, err := AggregateVersion(a.Version, a.Items); err != nil {
		return nil, err
	}
	if err := (Store{Directory: staging}).Save(a); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(dir, finalDir); err != nil {
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
	for _, file := range files(r) {
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
	summary, err := AggregateVersion(a.Version, a.Items)
	if err != nil {
		return err
	}
	a.SavedSummary = &summary
	a.UpdatedAt = time.Now().UTC()
	return s.saveEvidence(a)
}

func (s Store) Load(id string) (*Assessment, error) {
	if !assessmentID.MatchString(id) {
		return nil, fmt.Errorf("invalid assessment ID")
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, id, "assessment.json"))
	if err != nil {
		return nil, fmt.Errorf("load assessment: %w", err)
	}
	var header struct{ Version string }
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("decode assessment: %w", err)
	}
	if header.Version != EvidenceRubricVersion {
		return nil, fmt.Errorf("unsupported Test Quality version %q; start a new assessment", header.Version)
	}
	loaded, err := s.loadEvidence(id, data)
	if err != nil {
		return nil, err
	}
	return s.verifyLoaded(id, loaded)
}

func (s Store) verifyLoaded(id string, a *Assessment) (*Assessment, error) {
	if a.ID != id || a.Version != EvidenceRubricVersion {
		return nil, fmt.Errorf("assessment identity or rubric version mismatch")
	}
	if _, err := AggregateVersion(a.Version, a.Items); err != nil {
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
	artifacts := append([]Artifact{}, a.Artifacts...)
	for _, diagnostic := range a.Diagnostics {
		artifacts = append(artifacts, diagnostic.Artifacts...)
	}
	for _, artifact := range artifacts {
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
	return a, nil
}

func (s Store) List(root string) ([]*Assessment, error) {
	entries, err := s.ListSummaries(root)
	if err != nil {
		return nil, err
	}
	var out []*Assessment
	for _, entry := range entries {
		a, err := s.Load(entry.ID)
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
	list, err := s.ListSummaries("")
	if err != nil {
		return err
	}
	for _, entry := range list {
		if entry.Status != "running" && entry.Status != "queued" {
			continue
		}
		a, err := s.Load(entry.ID)
		if err != nil {
			return err
		}
		a.Status = "interrupted"
		a.Error = "Workbench stopped before this assessment finished. Start a new assessment to review remaining scope."
		if err := s.Save(a); err != nil {
			return err
		}
	}
	return nil
}
