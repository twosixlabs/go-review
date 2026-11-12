package testquality

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

func TestAgentValidatesRatingsAndCitations(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			store := Store{Directory: t.TempDir()}
			r := testReport(t.TempDir())
			a, err := store.Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			item := a.Items[0]
			cite := Citation{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}
			if bad {
				cite.FileID = "forged"
			}
			ratings := []Rating{rating(AssertionStrength, 2), rating(RepresentativeCases, 2), rating(BehaviorPaths, 2)}
			for i := range ratings {
				ratings[i].Evidence = []Citation{cite}
			}
			payload, _ := json.Marshal(map[string]any{"status": "scored", "ratings": ratings, "findings": []Finding{}})
			round := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round++
				var body struct {
					Messages []intelligence.Message `json:"messages"`
					Tools    []intelligence.Tool    `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Tools) != 2 {
					t.Errorf("tools = %d", len(body.Tools))
				}
				name, args := "submit_assessment", string(payload)
				if round == 1 {
					name = "read_evidence"
					read, _ := json.Marshal(EvidenceInput{SnapshotID: item.Identity.SnapshotID, Operation: "source", Args: json.RawMessage(`{"fileID":"t","startLine":1,"endLine":3}`)})
					args = string(read)
				}
				out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint(round), "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", out)
			}))
			defer provider.Close()
			got, err := Assess(context.Background(), a, item, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "test", MaxSteps: 2})
			if bad {
				if err == nil {
					t.Fatal("forged citation accepted")
				}

				return
			}
			if err != nil || got.Status != StatusScored || len(got.Ratings) != 3 {
				t.Fatalf("assessment: %+v %v", got, err)
			}
		})
	}
}

func TestAgentRejectsScoringWithoutInspectingTests(t *testing.T) {
	store := Store{Directory: t.TempDir()}
	r := testReport(t.TempDir())
	a, err := store.Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	item := a.Items[0]
	ratings := []Rating{rating(AssertionStrength, 3), rating(RepresentativeCases, 3), rating(BehaviorPaths, 3)}
	for i := range ratings {
		ratings[i].Evidence = []Citation{{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}}
	}
	payload, _ := json.Marshal(map[string]any{"status": "scored", "ratings": ratings})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "submit", "function": map[string]string{"name": "submit_assessment", "arguments": string(payload)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", out)
	}))
	defer provider.Close()
	if _, err := Assess(context.Background(), a, item, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "test", MaxSteps: 1}); err == nil {
		t.Fatal("scored tests without inspecting their source")
	}
}
