package workbench

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
)

func TestChangeChatProposalsNeverAuthorizeMutation(t *testing.T) {
	c := &changes.Comparison{ID: "c", Directory: filepath.Join(t.TempDir(), "comparison-fixture"), Files: []changes.FileChange{{Items: []changes.Item{{ID: "item"}}}}, Flows: []changes.Flow{}}
	s := New(nil)
	s.comparison = c
	proposal := changeProposal{Action: "state", StateAction: changes.StateAction{ComparisonID: "c", ItemIDs: []string{"item"}, Status: "reviewed"}}
	if err := s.validateChangeProposal(context.Background(), proposal); err != nil {
		t.Fatal(err)
	}
	state, err := c.State()
	if err != nil || state.Version != 0 || len(state.Items) != 0 {
		t.Fatalf("proposal mutated state: %+v %v", state, err)
	}
	proposal.ComparisonID = "stale"
	if err := s.validateChangeProposal(context.Background(), proposal); err == nil {
		t.Fatal("stale progress proposal accepted")
	}
	proposal.ComparisonID = "c"
	proposal.ItemIDs = []string{"foreign"}
	if err := s.validateChangeProposal(context.Background(), proposal); err == nil {
		t.Fatal("foreign item accepted")
	}
}
