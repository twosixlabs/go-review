package changes

import (
	"path/filepath"
	"testing"
)

func TestChangeFlowConsiderationRequiresKnownFlowAndExplicitSeparateAction(t *testing.T) {
	c := &Comparison{ID: "c", Directory: filepath.Join(t.TempDir(), "comparison-fixture"), Flows: []Flow{{ID: "flow"}}}
	considered := true
	for _, action := range []StateAction{
		{ComparisonID: c.ID, FlowID: "flow"},
		{ComparisonID: c.ID, FlowID: "foreign", Considered: &considered},
		{ComparisonID: c.ID, FlowID: "flow", Considered: &considered, Status: "approved"},
	} {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("invalid action panicked: %v", recovered)
				}
			}()
			if _, err := c.UpdateState(action); err == nil {
				t.Errorf("accepted invalid flow action: %+v", action)
			}
		}()
	}
	state, err := c.UpdateState(StateAction{ComparisonID: c.ID, FlowID: "flow", Considered: &considered})
	if err != nil || !state.Flows["flow"] {
		t.Fatalf("explicit flow consideration failed: %+v %v", state, err)
	}
}
