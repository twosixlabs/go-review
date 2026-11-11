package changes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeOversizedBlobsRemainInventoriedWithoutUnboundedPatches(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "changed.txt", "before\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	large := strings.Repeat("x", (16<<20)+1)
	fixtureFile(t, root, "changed.txt", large)
	fixtureFile(t, root, "added.txt", large)
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	resolved, err := Resolve(t.Context(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), resolved, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 2 {
		t.Fatalf("oversized files disappeared from inventory: %+v", c.Files)
	}
	for _, file := range c.Files {
		if file.Content != "oversized" || len(file.Items) != 1 || file.Items[0].Kind != "tree" {
			t.Fatalf("oversized content presented as fully diffed text: %+v", file)
		}
		patch, err := os.ReadFile(filepath.Join(c.Directory, "patches", file.ID))
		if err != nil || len(patch) > 2048 || !strings.Contains(string(patch), "16 MiB") {
			t.Fatalf("unbounded or unexplained patch: %d bytes, %v", len(patch), err)
		}
		if _, err := os.Stat(filepath.Join(c.Directory, "blobs", file.After.OID)); !os.IsNotExist(err) {
			t.Fatalf("oversized raw blob retained: %v", err)
		}
	}
}
