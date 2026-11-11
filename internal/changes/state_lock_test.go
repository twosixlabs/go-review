package changes

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChangeStateLockIsExclusiveAndReleasedByProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	release, err := acquireStateLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := acquireStateLock(path); !errors.Is(err, ErrConflict) {
		if another != nil {
			_ = another()
		}
		t.Fatalf("concurrent state writer was not rejected: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestChangeStateLockHelper$")
	command.Env = append(os.Environ(), "GO_REVIEW_LOCK_HELPER="+path)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock process helper: %v %s", err, data)
	}
	release, err = acquireStateLock(path)
	if err != nil {
		t.Fatalf("exited writer stranded future progress updates: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestChangeStateLockHelper(t *testing.T) {
	path := os.Getenv("GO_REVIEW_LOCK_HELPER")
	if path == "" {
		t.Skip("only runs as a child process")
	}
	if _, err := acquireStateLock(path); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip unlock/defer; the operating system must release ownership.
	os.Exit(0)
}
