package testquality

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		Files: []review.File{file("f", "f.go", source)},
		TestSources: []review.TestSource{{File: file("t", "f_test.go", test), Declarations: []review.TestDeclaration{
			{ID: "TestF", Name: "TestF", Kind: "test", Location: review.Location{FileID: "t", Line: 3, EndLine: 3}},
		}}},
		Symbols: []review.Symbol{{ID: "F", Kind: "function", Name: "F", Package: "example", Location: review.Location{FileID: "f", Line: 2, EndLine: 2}}}}
}

func TestStoreAssessmentPopulationIsTestsNotProductionFunctions(t *testing.T) {
	r := testReport(t.TempDir())
	r.TestSources[0].File.Source += "func TestOther(t *testing.T) { t.Log(F()) }\n"
	r.TestSources[0].File.SHA256 = Digest([]byte(r.TestSources[0].File.Source))
	r.TestSources[0].File.Package = "example"
	r.TestSources[0].Declarations = append(r.TestSources[0].Declarations,
		review.TestDeclaration{ID: "TestOther", Name: "TestOther", Kind: "test", Location: review.Location{FileID: "t", Line: 3, EndLine: 3}},
		review.TestDeclaration{ID: "helper", Name: "helper", Kind: "helper"},
		review.TestDeclaration{ID: "main", Name: "TestMain", Kind: "test"},
		review.TestDeclaration{ID: "bench", Name: "BenchmarkF", Kind: "benchmark"},
	)
	r.TestSources[0].ExcludedRanges = []review.Location{{FileID: "t", Line: 3, EndLine: 3}}
	r.TestSources[0].Declarations[1].Location = review.Location{FileID: "t", Line: 4, EndLine: 4}
	for i := 0; i < 40; i++ {
		r.Symbols = append(r.Symbols, review.Symbol{ID: fmt.Sprint(i), Kind: "function", Name: "Production"})
	}

	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 2 || a.Items[0].Name != "TestF" || a.Items[1].Name != "TestOther" {
		t.Fatalf("must assess tests once, never each production function: %+v", a.Items)
	}
	if a.Items[0].Status != StatusExcluded || a.Items[1].Status != StatusUnreviewed {
		t.Fatalf("test scope exclusions not honored: %+v", a.Items)
	}
}

func TestStoreRejectsObsoleteAssessmentsWithoutBlockingCurrentArchives(t *testing.T) {
	for _, version := range []string{"test-quality/1", "test-quality/2"} {
		t.Run(version, func(t *testing.T) {
			r := testReport(t.TempDir())
			s := Store{Directory: t.TempDir()}
			create := func() *Assessment {
				a, err := s.Create(t.Context(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
				if err != nil {
					t.Fatal(err)
				}
				return a
			}
			current, obsolete := create(), create()
			obsolete.Version = version
			raw, err := json.Marshal(obsolete)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Directory, obsolete.ID, "assessment.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(obsolete.ID); err == nil || !strings.Contains(err.Error(), "version") {
				t.Fatalf("obsolete archive remains readable: %v", err)
			}
			if _, err := Query(t.Context(), obsolete, QueryInput{AssessmentID: obsolete.ID, Operation: "overview"}); err == nil {
				t.Fatal("obsolete in-memory assessment remains queryable")
			}
			if err := s.Save(obsolete); err == nil {
				t.Fatal("obsolete assessment remains writable")
			}
			if err := s.Recover(); err != nil {
				t.Fatalf("obsolete archive blocked current recovery: %v", err)
			}
			list, err := s.ListSummaries("")
			if err != nil || len(list) != 1 || list[0].ID != current.ID {
				t.Fatalf("current archive listing lost: %+v %v", list, err)
			}
		})
	}
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
