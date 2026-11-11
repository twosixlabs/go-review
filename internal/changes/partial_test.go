package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangePartialAnalysisDoesNotProveEntryRemoval(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/partial\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package partial\nfunc Entry() {}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package partial\nfunc Other() { missing() }\n")
	gitFixture(t, root, "commit", "-qam", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), r, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, flow := range c.Flows {
		if flow.Before != nil {
			found = true
			if flow.Status != "after-unavailable" {
				t.Fatalf("partial After analysis treated missing counterpart as proven removal: %+v", flow)
			}
		}
	}
	if !found {
		t.Fatal("partial failure erased available Before flow evidence")
	}
}
