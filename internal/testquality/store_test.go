package testquality

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func testReport(root string) *review.Report {
	source := "package example\nfunc F() int { return 1 }\n"
	test := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	file := func(id, path, text string) review.File {
		return review.File{ID: id, Path: path, Source: text, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}
	}
	return &review.Report{Root: root, Module: "example", SourceSHA256: "source",
		Files:       []review.File{file("f", "f.go", source)},
		TestSources: []review.TestSource{{File: file("t", "f_test.go", test)}},
		Symbols:     []review.Symbol{{ID: "F", Kind: "function", Name: "F", Package: "example", Location: review.Location{FileID: "f", Line: 2, EndLine: 2}}}}
}

func TestStorePinsEvidenceAndRecoversInterruptedJob(t *testing.T) {
	ctx := context.Background()
	store := Store{Directory: t.TempDir()}
	root, artifacts := t.TempDir(), t.TempDir()
	path := filepath.Join(artifacts, "coverage")
	if err := os.WriteFile(path, []byte("mode: set\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := testReport(root)
	r.UnitEvidence = &review.UnitEvidence{Directory: artifacts, Runs: []review.UnitTestRun{{Status: "pass", CoveragePath: path}}}
	run := &review.RepositoryReview{Root: root, Modules: []review.ReviewedModule{{Directory: root, Report: r}}}
	a, err := store.Create(ctx, run, "local", "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 1 || a.Items[0].Identity.SnapshotID == "" {
		t.Fatalf("scope: %+v", a.Items)
	}
	r.Files[0].Source = "changed"
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Reports[0].Report.Files[0].Source == "changed" {
		t.Fatal("report not pinned")
	}
	data, err := os.ReadFile(loaded.Reports[0].Report.UnitEvidence.Runs[0].CoveragePath)
	if err != nil || string(data) != "mode: set\n" {
		t.Fatalf("artifact: %q %v", data, err)
	}
	loaded.Status = "running"
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	if err := store.Recover(); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(a.ID)
	if err != nil || loaded.Status != "interrupted" {
		t.Fatalf("recovery: %+v %v", loaded, err)
	}
	if _, err := store.Load("../escape"); err == nil {
		t.Fatal("accepted invalid ID")
	}
}

func TestStoreRejectsCorruptSourceAndEscapingArtifact(t *testing.T) {
	for _, kind := range []string{"digest", "path", "artifact"} {
		t.Run(kind, func(t *testing.T) {
			r := testReport(t.TempDir())
			switch kind {
			case "digest":
				r.Files[0].SHA256 = "wrong"
			case "path":
				r.Files[0].Path = "../escape"
			case "artifact":
				r.UnitEvidence = &review.UnitEvidence{Directory: t.TempDir(), Runs: []review.UnitTestRun{{CoveragePath: "/outside"}}}
			}

			s := Store{Directory: t.TempDir()}
			if _, err := s.Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", ""); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestStoreSurvivesActualReviewPruningAndDetectsArtifactCorruption(t *testing.T) {
	storage := t.TempDir()
	root := t.TempDir()
	old := filepath.Join(storage, "unit-evidence-old")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(old, "events.jsonl")
	if err := os.WriteFile(path, []byte("original test events"), 0600); err != nil {
		t.Fatal(err)
	}
	r := testReport(root)
	r.UnitEvidence = &review.UnitEvidence{Directory: old, Runs: []review.UnitTestRun{{Status: "pass", EventsPath: path}}}
	run := &review.RepositoryReview{Root: root, Directory: old, Modules: []review.ReviewedModule{{Report: r}}}
	if err := review.SaveCurrentReview(run); err != nil {
		t.Fatal(err)
	}
	store := Store{Directory: t.TempDir()}
	a, err := store.Create(context.Background(), run, "", "")
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(storage, "analysis-replacement")
	if err := os.Mkdir(next, 0700); err != nil {
		t.Fatal(err)
	}
	if err := review.SaveCurrentReview(&review.RepositoryReview{Root: root, Directory: next}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("real pruning not exercised: %v", err)
	}
	loaded, err := store.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifact := loaded.Reports[0].Report.UnitEvidence.Runs[0].EventsPath
	body, err := os.ReadFile(artifact)
	if err != nil || string(body) != "original test events" {
		t.Fatalf("pinned evidence lost: %q %v", body, err)
	}
	if err := os.WriteFile(artifact, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(a.ID); err == nil {
		t.Fatal("corrupt retained artifact accepted")
	}
}
