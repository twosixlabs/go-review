package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewResolvesRepositoryFromANestedModule(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, ".git", "gitdir: elsewhere\n")
	writeReviewFixture(t, root, "server/go.mod", "module example.com/server\n")
	got, err := resolveReviewRoot(filepath.Join(root, "server"))
	want, canonicalErr := filepath.EvalSymlinks(root)
	if err != nil || canonicalErr != nil || got != want {
		t.Fatalf("nested module review did not discover repository: %s %v", got, err)
	}
}

func TestCanceledReviewRetainsCompletedSourceEvidence(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.com/cancel\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package cancel\n\nfunc F() {}\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := ReviewRepository(ctx, root, t.TempDir(), func(p ReviewProgress) {
		if p.Phase == "Go vet" {
			cancel()
		}
	})
	if err != nil || run.Status != "canceled" || run.Modules[0].Report == nil {
		t.Fatalf("cancellation lost source evidence: %+v %v", run, err)
	}
	for _, command := range run.Modules[0].Report.Commands {
		if len(command.Args) > 0 && command.Args[0] == "test" {
			t.Fatal("canceled review executed target tests")
		}
	}
	if _, err := os.Stat(filepath.Join(run.Directory, "review.json")); err != nil {
		t.Fatal("canceled run was not retained:", err)
	}
}

func TestReviewWithdrawsExecutionEvidenceWhenTestsEditSources(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.com/edited\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package edited\n\nfunc F() int { return 7 }\n")
	writeReviewFixture(t, root, "app_test.go", `package edited
import ("os";"testing")
func TestEdit(t *testing.T) {
	if err:=os.WriteFile("app.go",[]byte("package edited\n\nfunc F() int { return 9 }\n"),0600); err!=nil {t.Fatal(err)}
	if F()!=7 {t.Fatal("changed implementation")}
}
`)
	saveFixtureReviewScope(t, root, ReviewScope{UnitSelections: []UnitTestSelection{{Package: "example.com/edited", Tests: []string{"TestEdit"}}}})
	run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := run.Modules[0].Report
	if run.Status != "attention" || r.Coverage != nil || r.Trace != nil {
		t.Fatal("execution evidence from changed sources remained valid")
	}
	for _, c := range r.Assessment.Checks {
		if c.ID == "tests" && (c.Status != "error" || !strings.Contains(c.Detail, "Source changed")) {
			t.Fatalf("source mismatch lost: %+v", c)
		}
	}
}
