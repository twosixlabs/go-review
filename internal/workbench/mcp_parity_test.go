package workbench

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestMCPParityUsesRetainedGraphsAndBindsRepositoryRun(t *testing.T) {
	r := &review.Report{Module: "sample", Root: "/unavailable/checkout", SourceSHA256: "sha",
		Symbols: []review.Symbol{{ID: "f", Name: "Work", Qualified: "sample.Work", Kind: "function", Package: "sample", Cyclomatic: 12}},
		DataFlows: &review.DataFlowEvidence{SourceSHA256: "sha", Results: []review.DataFlowResult{{SymbolID: "f", Status: "available", Graph: &review.DataFlow{
			SymbolID: "f", SourceSHA256: "sha", Nodes: []review.DataNode{{ID: "input", Kind: "parameter"}, {ID: "return", Kind: "return"}},
			Edges: []review.DataEdge{{From: "input", To: "return"}}}}}}}
	s := New(r)
	s.review = &review.RepositoryReview{Status: "complete", Modules: []review.ReviewedModule{{Report: r}, {Directory: "/failed", Error: "Module unavailable"}},
		Steps: []review.ReviewStep{{ID: "step", Status: "not-run", Detail: "Not authorized", Commands: []review.Command{{Stdout: "Complete output"}}}}}
	service := s.newChatService()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	server, err := service.server().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		payload, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	overview := call("review_overview", map[string]any{})
	id := overview["snapshot"].(map[string]any)["id"].(string)
	graph := call("review_dataflow", map[string]any{"snapshotID": id, "symbolID": "f", "valueID": "return", "direction": "upstream"})["data"].(map[string]any)
	if graph["status"] != "available" || len(graph["nodes"].([]any)) != 2 || len(graph["edges"].([]any)) != 1 {
		t.Fatalf("lost retained topology: %v", graph)
	}
	records := call("review_assess", map[string]any{"snapshotID": id, "section": "records", "coverage": "unknown", "minComplexity": 10})["data"].(map[string]any)
	if records["total"] != float64(1) {
		t.Fatalf("wrong shared Review Map population: %v", records)
	}
	result := call("review_results", map[string]any{"snapshotID": id, "stepID": "step", "section": "detail"})["data"].(map[string]any)
	if result["detail"] != "Not authorized" {
		t.Fatalf("lost skipped-stage reason: %v", result)
	}
	log := call("review_results", map[string]any{"snapshotID": id, "stepID": "step", "commandIndex": 0, "section": "stdout"})["data"].(map[string]any)
	if log["stdout"] != "Complete output" {
		t.Fatal("lost repository command log")
	}
	// Replacing only the run must invalidate its old references, even when the
	// selected immutable module report remains the same pointer.
	s.mu.Lock()
	s.review = &review.RepositoryReview{Status: "canceled", Modules: []review.ReviewedModule{{Report: r}}}
	s.mu.Unlock()
	next := call("review_overview", map[string]any{})
	if next["snapshot"].(map[string]any)["id"] == id {
		t.Fatal("run replacement retained stale evidence identity")
	}
	stale, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "review_results", Arguments: map[string]any{"snapshotID": id}})
	if err == nil && !stale.IsError {
		t.Fatal("old run reference accepted after replacement")
	}
	if s.current() != r || s.reviewRunning {
		t.Fatal("MCP changed selection or started execution")
	}
}
