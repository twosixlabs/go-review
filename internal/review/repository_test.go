package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeReviewFixture(t *testing.T, root, path, text string) {
	t.Helper()
	name := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func saveFixtureReviewScope(t *testing.T, root string, scope ReviewScope) {
	t.Helper()
	if err := SaveReviewScope(root, scope); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadReviewScope(root)
	if err != nil {
		t.Fatal(err)
	}
	name, err := scopeConfigPath(saved.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

func TestReviewDiscoversRealModulesButNotFixturesOrDependencies(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"server/go.mod", "tools/go.mod", "testdata/fake/go.mod", "vendor/dep/go.mod", "node_modules/dep/go.mod", ".cache/dep/go.mod"} {
		writeReviewFixture(t, root, path, "module example.com/fixture\n\ngo 1.24\n")
	}

	modules, err := discoverReviewModules(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 || modules[0] != filepath.Join(root, "server") || modules[1] != filepath.Join(root, "tools") {
		t.Fatalf("unexpected review targets: %v", modules)
	}
}

func TestRepositoryReviewRetainsFailuresAndSelectedUnitEvidence(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "server/go.mod", "module example.com/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "server/app.go", "package app\n\nfunc F() int { return 7 }\n")
	writeReviewFixture(t, root, "server/app_test.go", "package app\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=7 { t.Fatal(\"bad\") } }\n")
	writeReviewFixture(t, root, "zbroken/go.mod", "not a Go module\n")
	writeReviewFixture(t, root, "scripts/verify-repository.sh", "#!/bin/sh\nprintf 'repository check ran\\n'\nexit 3\n")
	saveFixtureReviewScope(t, root, ReviewScope{UnitSelections: []UnitTestSelection{{Package: "example.com/app", Tests: []string{"TestF"}}}, VerifyRepository: true})
	var progress []string
	var updates []ReviewProgress
	result, err := ReviewRepository(context.Background(), root, t.TempDir(), func(p ReviewProgress) {
		progress = append(progress, p.Phase)
		updates = append(updates, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Modules) != 2 || result.Modules[0].Report == nil || result.Modules[1].Error == "" {
		t.Fatalf("missing module result: %+v", result.Modules)
	}

	r := result.Modules[0].Report
	if r.Coverage == nil || !r.Coverage.SourceVerified || r.Coverage.CoveredStatements == 0 {
		t.Fatal("review must collect coverage without a separate import")
	}
	if r.UnitEvidence == nil || len(r.UnitEvidence.Runs) != 1 || r.UnitEvidence.Runs[0].Status != "pass" || r.Trace != nil {
		t.Fatal("review must retain the selected standard unit-test run, not custom call recording")
	}
	statuses := map[string]string{}
	for _, c := range r.Assessment.Checks {
		statuses[c.ID] = c.Status
	}
	if statuses["vet"] != "pass" || statuses["tests"] != "pass" || (statuses["repository-hygiene"] != "fail" && statuses["repository-hygiene"] != "error") {
		t.Fatalf("incorrect execution evidence: %v\n%+v", statuses, r.Assessment.Checks)
	}
	if statuses["deadcode"] != "pass" {
		t.Fatalf("bundled reachability analysis did not run: %v", statuses)
	}
	if result.Status != "attention" || len(progress) < 5 {
		t.Fatalf("lost failure or progress: %s %v", result.Status, progress)
	}
	commandUpdates := 0
	lastCompleted := 0
	previousPhase := ""
	previousModule := ""
	for _, p := range updates {
		if p.StartedAt.IsZero() || p.PhaseStartedAt.Before(p.StartedAt) || p.Root != result.Root {
			t.Fatalf("progress lacks reconnectable timing and repository scope: %+v", p)
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Command   *Command `json:"command"`
			Completed int      `json:"completedStages"`
			Total     int      `json:"totalStages"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Total != 11 || wire.Completed < lastCompleted || wire.Completed > wire.Total {
			t.Fatalf("stage progress must be monotonic with all planned modules included: %s", data)
		}
		lastCompleted = wire.Completed
		if wire.Completed == wire.Total && p.Command != nil {
			t.Fatal("terminal stage progress still has an executing command")
		}
		if p.Phase == "Repository verification" && previousPhase != p.Phase && wire.Completed != 2 {
			t.Fatalf("analysis stages were not counted before repository verification: %s", data)
		}
		previousPhase, previousModule = p.Phase, p.Module
		if wire.Command != nil {
			commandUpdates++
			if wire.Command.Executable == "" || wire.Command.Directory == "" || wire.Command.StartedAt.IsZero() || !wire.Command.FinishedAt.IsZero() {
				t.Fatalf("progress must describe the actual running command: %+v", wire.Command)
			}
		}
	}
	if commandUpdates == 0 {
		t.Fatal("review progress never exposed an executing command")
	}
	if lastCompleted != 11 || len(result.Steps) != 11 {
		t.Fatalf("missing terminal progress or explicit skipped stages: completed=%d steps=%d", lastCompleted, len(result.Steps))
	}
	if previousModule != result.Modules[1].Directory {
		t.Fatal("missing progress for skipped module stages")
	}
	for _, step := range result.Steps {
		if step.Module == result.Modules[1].Directory && step.ID != "analysis" && (step.Status != "not-run" || step.Detail == "") {
			t.Fatalf("unavailable module stage must be explicitly skipped: %+v", step)
		}
	}
	if updates[len(updates)-1].Command != nil {
		t.Fatal("completed review still advertises a running command")
	}
	data, err := os.ReadFile(filepath.Join(result.Directory, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved RepositoryReview
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Modules[0].Report.Coverage == nil || !strings.Contains(string(data), "verify-repository.sh") || !strings.Contains(string(data), "exit") {
		t.Fatal("persistent review lost its evidence or failure diagnostics")
	}
	if _, err := os.Stat(filepath.Join(root, "server", "coverage.out")); !os.IsNotExist(err) {
		t.Fatal("review artifacts must not be written into the checkout")
	}
}

func TestReviewPreservesFailedModuleAndDoesNotRunCanceledCommands(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "bad/go.mod", "not a module\n")
	writeReviewFixture(t, root, "scripts/verify-repository.sh", "#!/bin/sh\ntouch should-not-run\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReviewRepository(ctx, root, t.TempDir(), nil); err == nil {
		t.Fatal("canceled review must not appear successful")
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-run")); !os.IsNotExist(err) {
		t.Fatal("canceled review executed repository code")
	}
	result, err := ReviewRepository(context.Background(), filepath.Join(root, "bad"), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "attention" || len(result.Modules) != 1 || result.Modules[0].Error == "" {
		t.Fatal("unreviewable module must remain visible, not disappear")
	}
}
