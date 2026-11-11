package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestAgentNavigationAssessAndEvidenceParity(t *testing.T) {
	r := &review.Report{Module: "sample", SourceSHA256: "sha", Packages: []review.Package{{Path: "sample"}},
		Symbols:      []review.Symbol{{ID: "f", Name: "Work", Qualified: "sample.Work", Package: "sample", Kind: "function", Cyclomatic: 12}},
		ControlFlows: []review.ControlFlow{{SymbolID: "f"}},
		Assessment: &review.Assessment{Version: "architecture-signals/1", SourceSHA256: "sha",
			Findings: []review.SignalFinding{{ID: "finding", Package: "sample", Summary: "Boundary"}},
			Checks:   []review.HygieneCheck{{ID: "check", Title: "Types", Status: "fail"}}}}
	s := New(r)
	s.review = &review.RepositoryReview{Modules: []review.ReviewedModule{{Report: r}}, Steps: []review.ReviewStep{{ID: "step", Status: "not-run"}}}
	snapshot, _, err := s.newChatService().snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range []navigation{{View: "review-map", ID: "f"}, {View: "signals", ID: "finding"},
		{View: "hygiene", ID: "check"}, {View: "results", ID: "step"}, {View: "scope"}, {View: "limits"},
		{View: "control", ID: "f"}, {View: "calls", ID: "f"}, {View: "dataflow", ID: "f"}} {
		if _, err := validateNavigation(context.Background(), snapshot, n, true); err != nil {
			t.Errorf("%s: %v", n.View, err)
		}
	}
	for _, n := range []navigation{{View: "signals", ID: "missing"}, {View: "hygiene", ID: "missing"}, {View: "results", ID: "missing"}, {View: "control", ID: "missing"}} {
		if _, err := validateNavigation(context.Background(), snapshot, n, true); err == nil {
			t.Errorf("accepted invalid %s selection", n.View)
		}
	}
}

func TestAgentRefreshesTheActualAssessAndEvidenceView(t *testing.T) {
	r := &review.Report{Module: "sample", SourceSHA256: "sha", Packages: []review.Package{{Path: "sample"}},
		Symbols:    []review.Symbol{{ID: "f", Name: "Work", Kind: "function", Package: "sample", Cyclomatic: 2}},
		Documents:  []review.Document{{ID: "doc", Text: "Purpose"}},
		Assessment: &review.Assessment{Version: "architecture-signals/1", SourceSHA256: "sha", Checks: []review.HygieneCheck{{ID: "check"}}}}
	server := New(r)
	server.review = &review.RepositoryReview{Modules: []review.ReviewedModule{{Report: r}}, Steps: []review.ReviewStep{{ID: "step"}}}
	service := server.newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Explained\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer model.Close()
	for _, tc := range []struct{ context, tool, key, value string }{
		{`{"kind":"review","symbolID":"f","coverage":"unknown"}`, "review_assess", "coverage", "unknown"},
		{`{"kind":"signals","section":"hygiene","checkID":"check"}`, "review_evidence", "checkID", "check"},
		{`{"kind":"dataflow","symbolID":"f"}`, "review_dataflow", "symbolID", "f"},
		{`{"kind":"symbol","symbolID":"f"}`, "review_evidence", "symbolID", "f"},
		{`{"workspacePanel":"review-results","stepID":"step"}`, "review_results", "stepID", "step"},
		{`{"workspacePanel":"scope-results"}`, "review_evidence", "", ""},
		{`{"documentID":"doc"}`, "review_document", "id", "doc"},
	} {
		t.Run(tc.context, func(t *testing.T) {
			var first *chatEvent
			err := server.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "fixture"},
				chatRequest{Message: "Explain this view", Context: json.RawMessage(tc.context)}, snapshot, service,
				func(event chatEvent) error {
					if first == nil && event.Type == "tool" {
						copy := event
						first = &copy
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if first == nil || first.Tool != tc.tool {
				t.Fatalf("wrong context refresh: %+v want %s", first, tc.tool)
			}
			if tc.key != "" && first.Args.(map[string]any)[tc.key] != tc.value {
				t.Fatalf("lost current view selection: %+v", first.Args)
			}
		})
	}
}
