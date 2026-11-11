package workbench

import (
	"encoding/json"
	"fmt"
)

// UI context selects a read-only query, never a command. Its retained identifiers
// are validated by the same MCP handler as ordinary model-initiated queries.
func chatRefresh(info snapshotInfo, raw json.RawMessage) (string, map[string]any, error) {
	current := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return "", nil, fmt.Errorf("invalid UI context: %w", err)
		}
	}
	args := map[string]any{"snapshotID": info.ID, "limit": 3}
	copyFields := func(keys ...string) {
		for _, key := range keys {
			if value, ok := current[key]; ok && value != nil {
				if text, ok := value.(string); ok && text == "" {
					continue
				}
				args[key] = value
			}
		}
	}
	name := "review_overview"
	switch {
	case current["workspacePanel"] == "review-results":
		name = "review_results"
		copyFields("stepID")
	case current["workspacePanel"] == "scope-results" || current["workspacePanel"] == "evidence":
		name = "review_evidence"
	case current["documentID"] != nil && current["documentID"] != "":
		name = "review_document"
		args["id"] = current["documentID"]
		delete(args, "limit")
	case current["entryID"] != nil && current["entryID"] != "":
		name = "review_flow"
		copyFields("entryID", "focusSymbolID", "focusPath", "expandedIDs")
	case current["kind"] == "review":
		name = "review_assess"
		copyFields("package", "query", "minComplexity", "maxCoverage", "coverage", "cohort", "symbolID")
	case current["kind"] == "signals":
		if current["section"] == "hygiene" {
			name = "review_evidence"
			copyFields("checkID")
		} else {
			name = "review_assess"
			copyFields("package", "findingID")
		}
	case current["kind"] == "dataflow":
		name = "review_dataflow"
		copyFields("symbolID", "valueID", "direction", "depth")
	case current["kind"] == "logical" || current["kind"] == "symbol":
		name = "review_evidence"
		copyFields("symbolID")
		args["functionView"] = "logical"
		if current["kind"] == "symbol" {
			args["functionView"] = "calls"
			if current["control"] == true {
				args["functionView"] = "control"
			}
		}
	case current["kind"] == "package":
		name = "review_packages"
		copyFields("package")
	case current["kind"] == "packages":
		name = "review_packages"
	case current["kind"] == "system-flow":
		name = "review_architecture"
		copyFields("level", "componentID", "operationID", "group")
	}
	if name == "review_overview" {
		delete(args, "snapshotID")
		args["module"] = info.Module
	}
	return name, args, nil
}
