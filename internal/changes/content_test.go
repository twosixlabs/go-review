package changes

import (
	"context"
	"testing"
)

func TestChangeInventoryDistinguishesLFSAndSourceContainingBinaryDiagnosticWords(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "notes.txt", "before\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "notes.txt", "Binary files are an example in this text.\n")
	fixtureFile(t, root, "asset.dat", "version https://git-lfs.github.com/spec/v1\noid sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nsize 20000000\n")
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
	for _, file := range files {
		switch file.After.Path {
		case "notes.txt":
			if file.Content != "text" || file.Items[0].Kind != "text" {
				t.Errorf("source text mistaken for binary Git output: %+v", file)
			}
		case "asset.dat":
			if file.Content != "lfs-pointer" {
				t.Errorf("LFS pointer presented as fetched content: %+v", file)
			}
		}
	}
}
