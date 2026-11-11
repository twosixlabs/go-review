package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeClosureAndCallCorrespondenceUsesOwningSourceContext(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/occurrences\n\ngo 1.24\n")
	beforeSource := "package occurrences\nfunc Entry() func() int {\n f:=func() int {return 1}\n helper(1); helper(1); helper(2); go helper(3)\n return f\n}\nfunc helper(v int){}\n"
	fixtureFile(t, root, "app.go", beforeSource)
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package occurrences\nfunc Entry() func() int {\n f:=func() int {return 2}\n helper(1); helper(1); helper(2); go helper(3)\n return f\n}\nfunc helper(v int){}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), r, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var closure Record
	for _, record := range c.Records {
		if record.Before != nil && record.Before.Kind == "closure" {
			closure = record
		}
	}
	if closure.Before == nil || closure.After == nil || len(closure.FlowIDs) != 1 {
		t.Fatalf("unique local callback declaration lost its owner/context: %+v", c.Records)
	}
	callPairs := 0
	for _, pair := range c.Correspondences {
		if pair.Kind == "call" {
			callPairs++
		}
	}
	if callPairs != 2 {
		t.Fatalf("expected only unique helper(2) and async helper(3), not traversal pairing of repeated helper(1): %+v", c.Correspondences)
	}
}
