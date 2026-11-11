package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeFunctionValueAssociationRetainsReferenceWithoutInventingInvocation(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/reference\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package reference\nfunc Callback() func() int {return helper}\nfunc helper() int {return 1}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package reference\nfunc Callback() func() int {return helper}\nfunc helper() int {return 2}\n")
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
	if len(c.Flows) != 1 {
		t.Fatalf("function-value source association missing: %+v", c.Flows)
	}
	for _, association := range c.Flows[0].Associations {
		if len(association.Witness) != 1 || association.Witness[0].Kind != "source-reference" || association.Quality != "potential-reference-impact" {
			t.Fatalf("reference treated as invocation: %+v", association)
		}
	}
}
