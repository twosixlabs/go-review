package changes

import (
	"os"
	"testing"
)

func TestChangeReleaseInputsRequiresRecordedStorageOwnership(t *testing.T) {
	storage := t.TempDir()
	outside, err := os.MkdirTemp(t.TempDir(), "revision-input-")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseInputs(Resolved{ObjectDirectory: outside}); err == nil {
		t.Fatal("prefix alone authorized deleting unowned input")
	}
	owned, err := os.MkdirTemp(storage, "revision-input-")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseInputs(Resolved{ObjectDirectory: owned}); err == nil {
		t.Fatal("input without a recorded owner was deleted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("unowned input was modified")
	}
}
