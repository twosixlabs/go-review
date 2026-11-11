package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentReviewReplacesOldEvidenceButPreservesOtherRepositories(t *testing.T) {
	storage := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeReviewFixture(t, root, "go.mod", "module example.test/current\n\ngo 1.24\n")
	otherRoot := t.TempDir()
	makeRun := func(name, repository string) *RepositoryReview {
		t.Helper()
		dir := filepath.Join(storage, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		run := &RepositoryReview{Root: repository, Directory: dir, Status: "completed"}
		data, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "review.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return run
	}
	old := makeRun("20261111T010000-old", root)
	other := makeRun("20261111T010001-other", otherRoot)
	writeReviewFixture(t, old.Directory, "obsolete.log", "old test evidence")
	current := makeRun("20261111T010002-current", root)
	if err := SaveCurrentReview(current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Directory); !os.IsNotExist(err) {
		t.Fatalf("superseded evidence still exists: %v", err)
	}
	if _, err := os.Stat(other.Directory); err != nil {
		t.Fatalf("unrelated repository evidence removed: %v", err)
	}
}

func TestRepositoryReviewSkippedChecksDoNotImplyFailures(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/skip\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package skip\n\nfunc F() int { return 7 }\n")
	writeReviewFixture(t, root, "app_test.go", "package skip\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F() != 7 {\n\t\tt.Fatal(\"wrong result\")\n\t}\n}\n")
	run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" {
		t.Fatalf("skipped checks were treated as failures: %+v", run.Steps)
	}
	skipped := 0
	for _, step := range run.Steps {
		if step.Status == "not-run" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatal("fixture did not exercise an intentionally skipped check")
	}
}

func TestCurrentReviewCarriesOnlyStillCurrentModuleArtifacts(t *testing.T) {
	storage := t.TempDir()
	root := t.TempDir()
	old := filepath.Join(storage, "20261111T010000-old")
	writeReviewFixture(t, old, "module-1/selection-1/test-events.jsonl", "current module A events")
	writeReviewFixture(t, old, "module-1/selection-1/coverage.out", "current module A coverage")
	writeReviewFixture(t, old, "module-2/selection-1/test-events.jsonl", "obsolete module B events")
	prior := &RepositoryReview{Root: root, Directory: old}
	data, _ := json.Marshal(prior)
	writeReviewFixture(t, old, "review.json", string(data))
	next := filepath.Join(storage, "unit-evidence-next")
	writeReviewFixture(t, next, "selection-1/test-events.jsonl", "new module B events")
	run := &RepositoryReview{Root: root, Directory: next, Status: "completed", Modules: []ReviewedModule{
		{Directory: root + "/a", Report: &Report{Root: root + "/a", UnitEvidence: &UnitEvidence{
			Directory: filepath.Join(old, "module-1"), Runs: []UnitTestRun{{
				Status: "pass", EventsPath: filepath.Join(old, "module-1/selection-1/test-events.jsonl"),
				CoveragePath: filepath.Join(old, "module-1/selection-1/coverage.out"),
			}},
		}}},
		{Directory: root + "/b", Report: &Report{Root: root + "/b", UnitEvidence: &UnitEvidence{
			Directory: next, Runs: []UnitTestRun{{Status: "pass", EventsPath: filepath.Join(next, "selection-1/test-events.jsonl")}},
		}}},
	}}
	if err := SaveCurrentReview(run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old evidence was retained to support another module: %v", err)
	}
	events := run.Modules[0].Report.UnitEvidence.Runs[0].EventsPath
	body, err := os.ReadFile(events)
	if err != nil || string(body) != "current module A events" || !inside(next, events) {
		t.Fatalf("current module A artifacts not consolidated: %s %q %v", events, body, err)
	}
	if _, err := os.Stat(filepath.Join(next, "module-2/selection-1/test-events.jsonl")); !os.IsNotExist(err) {
		t.Fatal("obsolete module B evidence was copied forward")
	}
	for _, module := range run.Modules {
		if module.Report.Review == nil || module.Report.Review.Directory != next {
			t.Fatal("module still points at superseded manifest")
		}
	}
}

func TestCurrentReviewNeverDeletesOutsideOwnedRunDirectory(t *testing.T) {
	storage := t.TempDir()
	root := t.TempDir()
	outside := t.TempDir()
	writeReviewFixture(t, outside, "keep", "not review evidence")
	if err := os.Symlink(outside, filepath.Join(storage, "20261111T010000-symlink")); err != nil {
		t.Fatal(err)
	}
	writeReviewFixture(t, storage, "unrelated/keep", "not a run")
	current := &RepositoryReview{Root: root, Directory: filepath.Join(storage, "unit-evidence-current")}
	if err := os.Mkdir(current.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveCurrentReview(current); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join(outside, "keep"), filepath.Join(storage, "unrelated/keep")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("unsafe review cleanup: %s: %v", name, err)
		}
	}
}

func TestCurrentReviewPreservesFailedModuleWithoutInventingMissingArtifacts(t *testing.T) {
	storage := t.TempDir()
	root := t.TempDir()
	old := filepath.Join(storage, "20261111T010000-old")
	writeReviewFixture(t, old, "selection-1/test-events.jsonl", "build failed")
	run := &RepositoryReview{Root: root, Directory: filepath.Join(storage, "unit-evidence-next"), Modules: []ReviewedModule{{
		Directory: root, Report: &Report{Root: root, UnitEvidence: &UnitEvidence{
			Directory: old, Runs: []UnitTestRun{{
				Status: "partial", Error: "test build failed; coverage unavailable",
				EventsPath: filepath.Join(old, "selection-1/test-events.jsonl"), CoveragePath: filepath.Join(old, "selection-1/missing.out"),
			}},
		}},
	}}}
	if err := os.Mkdir(run.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveCurrentReview(run); err != nil {
		t.Fatalf("cannot preserve failed module alongside refreshed module: %v", err)
	}
	unit := run.Modules[0].Report.UnitEvidence.Runs[0]
	if unit.Status != "partial" || unit.Error == "" || unit.CoveragePath != "" {
		t.Fatal("missing partial artifact must remain explicitly unavailable, not a stale file reference")
	}
	if len(run.Modules[0].Report.UnitEvidence.Limitations) == 0 {
		t.Fatal("missing artifact was silently discarded")
	}
}

func TestCurrentReviewReportsCleanupFailure(t *testing.T) {
	storage := t.TempDir()
	root := t.TempDir()
	old := filepath.Join(storage, "20261111T010000-old")
	if err := os.MkdirAll(filepath.Join(old, "review.json"), 0700); err != nil {
		t.Fatal(err)
	}
	current := &RepositoryReview{Root: root, Directory: filepath.Join(storage, "analysis-current")}
	if err := os.Mkdir(current.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveCurrentReview(current); err == nil {
		t.Fatal("cleanup failed but was reported as successful")
	}
}

func TestReviewReplacementDeletesPriorRepositoryRun(t *testing.T) {
	root := t.TempDir()
	storage := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/replace\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package replace\n\nfunc F() int { return 7 }\n")
	writeReviewFixture(t, root, "app_test.go", "package replace\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F() != 7 {\n\t\tt.Fatal(\"wrong result\")\n\t}\n}\n")
	previous, err := ReviewRepository(context.Background(), root, storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ReviewRepository(context.Background(), root, storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previous.Directory); !os.IsNotExist(err) {
		t.Fatalf("real repeat review left old evidence: %v", err)
	}
	if _, err := os.Stat(current.Modules[0].Report.UnitEvidence.Runs[0].EventsPath); err != nil {
		t.Fatalf("current evidence missing: %v", err)
	}
}
