package changes

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeContextFindsHiddenSharedHelpersWithoutDuplicatingInventory(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/context\n\ngo 1.24\n")
	fixtureFile(t, root, "app.go", "package context\nfunc First() int {return helper()}\nfunc Second() int {return helper()}\nfunc helper() int {return 1}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "app.go", "package context\nfunc First() int {return helper()}\nfunc Second() int {return helper()}\nfunc helper() int {return 2}\n")
	fixtureFile(t, root, "notes.md", "# Supporting change\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "after")
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), resolved, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flows) != 2 {
		t.Fatalf("hidden helper callers absent: %+v", c.Flows)
	}
	var shared Record
	seen := map[string]int{}
	for _, record := range c.Records {
		for _, id := range record.ItemIDs {
			seen[id]++
		}
		if record.Title == "example.test/context.helper" {
			shared = record
		}
	}
	if len(shared.FlowIDs) != 2 || shared.Before == nil || shared.After == nil {
		t.Fatalf("shared implementation not represented once: %+v", c.Records)
	}
	for _, file := range c.Files {
		for _, item := range file.Items {
			if seen[item.ID] != 1 {
				t.Fatalf("item %s accounted %d times", item.ID, seen[item.ID])
			}
		}
	}
	for _, flow := range c.Flows {
		found := false
		for _, association := range flow.Associations {
			if association.RecordID == shared.ID && len(association.Witness) > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("flow has no retrievable entry-to-helper witness: %+v", flow)
		}
	}
	state, err := c.UpdateState(StateAction{ComparisonID: c.ID, Version: 0, ItemIDs: shared.ItemIDs, Status: "reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Flows) != 0 {
		t.Fatal("reviewing shared implementation implicitly considered caller consequences")
	}
	yes := true
	state, err = c.UpdateState(StateAction{ComparisonID: c.ID, Version: state.Version, FlowID: c.Flows[0].ID, Considered: &yes})
	if err != nil || len(state.Flows) != 1 {
		t.Fatalf("explicit flow consideration failed: %+v %v", state, err)
	}
}
