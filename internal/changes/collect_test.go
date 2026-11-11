package changes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeCollectionIsStaticAndIndependentlyRetained(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/change\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package change\nfunc Entry() int {return helper()}\nfunc helper() int {return 1}\n")
	fixtureFile(t, root, "app_test.go", "package change\nimport(\"os\";\"testing\")\nfunc TestNever(t *testing.T){os.WriteFile(\"target-ran\",nil,0600)}\n")
	fixtureFile(t, root, "scripts/verify-repository.sh", "touch target-ran\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package change\nfunc Entry() int {return helper()}\nfunc helper() int {return 2}\n")
	fixtureFile(t, root, "broken/go.mod", "not a module\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	comparison, err := Collect(context.Background(), resolved, storage, review.ReviewScope{VerifyRepository: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Modules) != 2 {
		t.Fatalf("lost added/broken module: %+v", comparison.Modules)
	}
	if comparison.Scope.VerifyRepository {
		t.Fatal("retained comparison scope falsely claims repository verification is enabled")
	}
	var module Module
	for _, m := range comparison.Modules {
		if m.Path == "." {
			module = m
		}
	}
	if module.Before.SnapshotID == "" || module.After.SnapshotID == "" {
		t.Fatalf("missing side analysis: %+v", module)
	}
	for _, side := range []string{"before", "after"} {
		report, err := comparison.Report(module.ID, side)
		if err != nil {
			t.Fatal(err)
		}
		if report.UnitEvidence != nil || report.Trace != nil {
			t.Fatal("comparison executed target tests")
		}
		for _, cmd := range report.Commands {
			if cmd.Executable != "go" || len(cmd.Args) > 0 && cmd.Args[0] == "test" {
				t.Fatalf("unexpected command: %+v", cmd)
			}
		}
		if _, err := os.Stat(filepath.Join(report.Root, "target-ran")); !os.IsNotExist(err) {
			t.Fatal("target script/test ran")
		}
	}
	if err := Publish(storage, comparison); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, root, "app.go", "checkout has changed")
	reopened, err := OpenCurrent(storage, resolved.Repository)
	if err != nil {
		t.Fatal(err)
	}
	report, err := reopened.Report(module.ID, "before")
	if err != nil {
		t.Fatal(err)
	}
	if report.Files[0].Source != "package change\nfunc Entry() int {return helper()}\nfunc helper() int {return 1}\n" {
		t.Fatal("side read consulted live checkout")
	}
	oldID := reopened.ID
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Collect(ctx, resolved, storage, review.ReviewScope{}, nil); err == nil {
		t.Fatal("canceled collection succeeded")
	}
	current, err := OpenCurrent(storage, resolved.Repository)
	if err != nil || current.ID != oldID {
		t.Fatal("canceled replacement destroyed current comparison")
	}
}

func TestChangeProgressIsExplicitVersionedAndEvidenceBound(t *testing.T) {
	root, before, after := revisionFixture(t)
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: after})
	if err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	c, err := Collect(context.Background(), resolved, storage, review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := c.Files[0].Items[0].ID
	initial, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	if initial.Version != 0 || len(initial.Items) != 0 {
		t.Fatal("collection implicitly marked items")
	}
	next, err := c.UpdateState(StateAction{ComparisonID: c.ID, Version: 0, ItemIDs: []string{item}, Status: "reviewed"})
	if err != nil || next.Version != 1 || next.Items[item] != "reviewed" {
		t.Fatalf("state failed: %+v %v", next, err)
	}
	for _, action := range []StateAction{
		{ComparisonID: c.ID, Version: 0, ItemIDs: []string{item}, Status: "follow-up"},
		{ComparisonID: "foreign", Version: 1, ItemIDs: []string{item}, Status: "reviewed"},
		{ComparisonID: c.ID, Version: 1, ItemIDs: []string{"foreign"}, Status: "reviewed"},
		{ComparisonID: c.ID, Version: 1, ItemIDs: []string{item}, Status: "approved"},
	} {
		if _, err := c.UpdateState(action); err == nil {
			t.Fatalf("accepted stale/foreign/host approval state: %+v", action)
		}
	}
	undo, err := c.UpdateState(StateAction{ComparisonID: c.ID, Version: 1, ItemIDs: []string{item}, Status: "not-reviewed"})
	if err != nil || undo.Items[item] != "not-reviewed" {
		t.Fatal("explicit reversible state failed")
	}
}

func TestChangeCollectionRejectsEscapingLocalDependencies(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/escape\n\ngo 1.24\nreplace example.test/outside => /host/private\n")
	fixtureFile(t, root, "app.go", "package escape\nfunc Entry() {}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "input")
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: "HEAD", After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}

	c, err := Collect(context.Background(), resolved, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Modules) != 1 || c.Modules[0].After.Error == "" || c.Modules[0].After.SnapshotID != "" {
		t.Fatalf("host replacement was analyzed: %+v", c.Modules)
	}
}
