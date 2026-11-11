package changes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChangeDiscardRefusesUnownedAndSymlinkDirectories(t *testing.T) {
	storage := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(storage, "comparison-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	for _, directory := range []string{storage, outside, link, filepath.Join(storage, "current")} {
		if err := Discard(storage, &Comparison{Directory: directory}); err == nil {
			t.Fatalf("discard accepted unowned path %q", directory)
		}
	}
	owned, err := os.MkdirTemp(storage, "comparison-")
	if err != nil {
		t.Fatal(err)
	}
	if err := Discard(storage, &Comparison{Directory: owned}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("unpublished artifact retained: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory modified: %v", err)
	}
}
