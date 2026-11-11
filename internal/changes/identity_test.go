package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeEquivalentEvidenceIgnoresPresentationLabelsAndOwnedPaths(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/identity\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package identity\nfunc Entry() int {return 1}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package identity\nfunc Entry() int {return 2}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	first, err := Collect(context.Background(), r, storage, review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Before.Label = "another label"
	r.After.Label = "same head, another label"
	second, err := Collect(context.Background(), r, storage, review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Modules[0].After.SnapshotID != second.Modules[0].After.SnapshotID {
		t.Fatalf("equivalent evidence changed identity: %s vs %s", first.ID, second.ID)
	}
	item := first.Files[0].Items[0].ID
	if _, err := first.UpdateState(StateAction{ComparisonID: first.ID, ItemIDs: []string{item}, Status: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	state, err := second.State()
	if err != nil || state.Items[item] != "reviewed" {
		t.Fatalf("equivalent evidence lost exact local state: %+v %v", state, err)
	}
}
