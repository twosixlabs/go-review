package workbench

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/russellbrasser/go-review/internal/changes"
)

type changeProposal struct {
	changes.StateAction
	Action    string `json:"action"`
	Directory string `json:"directory,omitempty"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Provider  string `json:"provider,omitempty"`
	PreviewID string `json:"previewID,omitempty"`
	JobID     string `json:"jobID,omitempty"`
}

func (s *Server) validateChangeProposal(ctx context.Context, p changeProposal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch p.Action {
	case "resolve":
		if p.Directory == "" {
			return fmt.Errorf("repository directory is required")
		}
		if p.Provider == "" && (p.Before == "" || p.After == "") {
			return fmt.Errorf("committed Before/After refs or PR/MR URL are required")
		}
		return nil
	case "collect", "cancel":
		s.mu.RLock()
		defer s.mu.RUnlock()
		if p.Action == "cancel" && (s.changeJob == nil || s.changeJob.ID != p.JobID) {
			return fmt.Errorf("no comparison collection is running")
		}
		if p.Action == "collect" && (s.changePreview == nil || s.changePreviewID != p.PreviewID || s.changePreview.Before.Commit != p.Before || s.changePreview.After.Commit != p.After) {
			return fmt.Errorf("resolve revision inputs and inspect their preview first")
		}
		return nil
	case "state":
		c, err := s.currentComparison(p.ComparisonID)
		if err != nil {
			return err
		}
		if err := c.ValidateStateAction(p.StateAction); err != nil {
			return err
		}
		state, err := c.State()
		if err != nil {
			return err
		}
		if state.Version != p.Version {
			return changes.ErrConflict
		}
		return nil
	default:
		return fmt.Errorf("unknown comparison proposal action")
	}
}

var navigateChangesSchema = json.RawMessage(`{"type":"object","properties":{
 "comparisonID":{"type":"string"},"operation":{"type":"string","enum":["overview","records","files","flows","record","source","flow","side"]},
 "perspective":{"type":"string","enum":["all","affected","shared","other"]},"status":{"type":"string"},"query":{"type":"string"},
 "recordID":{"type":"string"},"flowID":{"type":"string"},"fileID":{"type":"string"},"moduleID":{"type":"string"},
 "side":{"type":"string","enum":["before","after"]},"snapshotID":{"type":"string"},
 "view":{"type":"string"},"symbolID":{"type":"string"},"sourceFileID":{"type":"string"},"documentID":{"type":"string"},
 "section":{"type":"string"},"functionView":{"type":"string"},"package":{"type":"string"},"level":{"type":"string"},"componentID":{"type":"string"},"operationID":{"type":"string"},"group":{"type":"string"},
 "focusSymbolID":{"type":"string"},"focusPath":{"type":"array","items":{"type":"string"}},
 "expandedIDs":{"type":"array","items":{"type":"string"}}},
 "required":["comparisonID","operation"],"additionalProperties":false}`)

var changeProposalSchema = json.RawMessage(`{"type":"object","properties":{
 "action":{"type":"string","enum":["resolve","collect","cancel","state"]},"comparisonID":{"type":"string"},"version":{"type":"integer","minimum":0},"previewID":{"type":"string"},"jobID":{"type":"string"},
 "directory":{"type":"string"},"before":{"type":"string"},"after":{"type":"string"},"provider":{"type":"string"},
 "itemIDs":{"type":"array","items":{"type":"string"}},"status":{"type":"string","enum":["not-reviewed","reviewed","follow-up"]},
 "flowID":{"type":"string"},"considered":{"type":"boolean"}},
 "required":["action"],"additionalProperties":false}`)

const changeAgentInstructions = `
CHANGE REVIEW
Change Review is an additional local application-comparison capability, not a PR/MR approval process.
Use review_changes for complete Git inventory, unique changed items, affected flows, shared implementation, other changes,
per-side source witnesses and explicit local progress. Start overview; preserve comparisonID.
Each side/module snapshotID is separate from the ordinary Application snapshot. For flow or side operations always supply
comparisonID, moduleID, side and that module's snapshotID. Before may be a merge base; the target tip is separate.
Use side view evidence/dataflow/source/document/packages/architecture/search and exact returned IDs for drill-ins.
Discovery and association are independent: hidden helpers can affect several flows; unmapped material remains in inventory.
Missing counterpart is not proof of removal when side analysis failed. Potential source paths are not observed execution.
Use navigate_changes to show comparison views, not navigate_ui. It uses the same user-controlled navigation permission.
Use propose_change_action for resolving/fetching revision inputs, collection/refresh, cancellation or progress actions.
A proposal does nothing until the human explicitly confirms it in the host. Navigation permission never authorizes mutation.
Progress actions require exact displayed item IDs or separate flow consideration, plus the current state version.
Never describe local reviewed status as approval, completeness/correctness certification, or a provider action.
Cite retained changes as [change:comparisonID|recordID], or [change-source:comparisonID|fileID|before-or-after].
`
