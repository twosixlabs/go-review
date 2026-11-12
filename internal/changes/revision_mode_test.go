package changes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChangeLocalMergeBaseAndExactRevisions(t *testing.T) {
	root, base, source := revisionFixture(t)
	if err := os.Chmod(filepath.Join(root, "mode.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "checkout", "-qb", "target", base)
	fixtureFile(t, root, "target.txt", "target\n")
	gitFixture(t, root, "add", "target.txt")
	gitFixture(t, root, "commit", "-qm", "target")
	target := gitFixture(t, root, "rev-parse", "HEAD")
	for _, mode := range []struct {
		mergeBase bool
		before    string
	}{{true, base}, {false, target}} {
		result, err := Resolve(context.Background(), Request{Directory: root, Before: "target", After: source, MergeBase: mode.mergeBase})
		if err != nil {
			t.Fatal(err)
		}
		if result.Before.Commit != mode.before || result.After.Commit != source {
			t.Fatalf("incorrect local revisions for mergeBase=%v: %+v", mode.mergeBase, result)
		}
		if mode.mergeBase && (result.Target == nil || result.Target.Commit != target) || !mode.mergeBase && result.Target != nil {
			t.Fatalf("incorrect target for mergeBase=%v: %+v", mode.mergeBase, result.Target)
		}
	}
}
