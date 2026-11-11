package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeAdjacentOwnersAreSplitWithoutLosingChangedLines(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/split\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package split\nfunc First() int {return 1}\nfunc Second() int {return 2}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package split\nfunc First() int {return 3}\nfunc Second() int {return 4}\n")
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
	if len(c.Records) != 2 || len(c.Flows) != 2 {
		t.Fatalf("adjacent owners collapsed into unsupported material: records=%+v flows=%+v", c.Records, c.Flows)
	}
	beforeLines, afterLines := map[int]int{}, map[int]int{}
	for _, file := range c.Files {
		for _, item := range file.Items {
			for line := item.Before.Start; line < item.Before.Start+item.Before.Lines; line++ {
				beforeLines[line]++
			}
			for line := item.After.Start; line < item.After.Start+item.After.Lines; line++ {
				afterLines[line]++
			}
		}
	}
	for _, counts := range []map[int]int{beforeLines, afterLines} {
		if len(counts) != 2 || counts[2] != 1 || counts[3] != 1 {
			t.Fatalf("changed source line accounting is not exact: %v", counts)
		}
	}
}

func TestChangeUniqueSourceBackedRenamePreservesResponsibility(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/rename\n\ngo 1.24\n")
	fixtureFile(t, root, "old.go", "package rename\nfunc BeforeName(x int) int {return x+1}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "mv", "old.go", "new.go")
	fixtureFile(t, root, "new.go", "package rename\nfunc AfterName(x int) int {return x+1}\n")
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
	implementation := []Record{}
	for _, record := range c.Records {
		if record.Kind == "implementation" {
			implementation = append(implementation, record)
		}
	}
	if len(implementation) != 1 || implementation[0].Before == nil || implementation[0].After == nil || len(c.Flows) != 1 || c.Flows[0].Status != "matched" {
		t.Fatalf("unique unchanged declaration structure was not matched: records=%+v flows=%+v", c.Records, c.Flows)
	}
}
