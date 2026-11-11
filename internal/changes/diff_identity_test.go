package changes

import (
	"context"
	"strings"
	"testing"
)

func TestChangeRenameChainDiffsEachObjectPairNotPathUnion(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "a.txt", strings.Repeat("alpha source\n", 12))
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "mv", "a.txt", "b.txt")
	fixtureFile(t, root, "a.txt/child.txt", "new child\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := Inventory(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("rename inventory: %+v", files)
	}
	for _, file := range files {
		if file.After.Path == "b.txt" && (len(file.Items) != 1 || file.Items[0].Kind != "tree") {
			t.Fatalf("unchanged rename inherited another file's patch ranges: %+v", file)
		}
		if file.After.Path == "a.txt/child.txt" && (len(file.Items) != 1 || file.Items[0].Before.Lines != 0 || file.Items[0].After.Lines != 1) {
			t.Fatalf("added source does not describe its own blob pair: %+v", file)
		}
	}
}
