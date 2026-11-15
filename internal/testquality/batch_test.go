package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

func TestQualityBatchContextSelectsCalledProductionBeyondFilePrefix(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {got,err:=Classify(1);if got!="accepted"||err!=nil {t.Fatal(got,err)}}`)
	file := &a.Reports[0].Report.Files[0]
	file.Source = strings.Repeat("// unrelated preamble\n", 250) + file.Source
	file.SHA256 = Digest([]byte(file.Source))
	initial, _, err := batchContext(t.Context(), a, a.Items)
	if err != nil {
		t.Fatal(err)
	}
	text, err := json.Marshal(initial.Shared)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "func Classify") || !strings.Contains(string(text), "Contract:") {
		t.Fatal("called implementation and its contract are missing")
	}
	if strings.Contains(string(text), "unrelated preamble") {
		t.Fatal("irrelevant file prefix consumed the evidence budget")
	}
}

func TestQualityBatchHostOwnsKnownMetadataAndDerivedStatus(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {got,err:=Classify(1);if got!="accepted"||err!=nil {t.Fatal(got,err)}}`)
	item := a.Items[0]
	value := 3
	evidence := []Citation{item.Declaration, {SnapshotID: item.Identity.SnapshotID, FileID: "contract", StartLine: 4, EndLine: 5}}
	ratings := []Rating{{Dimension: Craftsmanship, State: RatingAssessed, Value: &value, Rationale: "Focused test", Evidence: evidence}, {Dimension: AssertionStrength, State: RatingAssessed, Value: &value, Rationale: "Contract outputs asserted", Evidence: evidence}}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Format   json.RawMessage        `json:"response_format"`
			Messages []intelligence.Message `json:"messages"`
			Tools    []intelligence.Tool    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var initial judgmentContext
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
			return
		}
		if len(initial.Tests) != 1 || len(initial.Tests[0].Facts.Checks) == 0 || len(initial.Tests[0].Facts.Predicates) != 0 {
			t.Error("individual context must retain checking evidence without collective input-partition facts")
		}
		for _, tool := range request.Tools {
			if tool.Function.Name == "quality_facts" {
				t.Error("already supplied facts are unnecessarily reread with collective partitions")
			}
		}
		content := "Evidence review complete."
		if len(request.Format) > 0 {
			var receipt struct{ References map[string]Citation }
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Error(err)
				return
			}
			refs := []string{}
			for _, citation := range evidence {
				found := ""
				for id, source := range receipt.References {
					if source.SnapshotID == citation.SnapshotID && source.FileID == citation.FileID && source.StartLine <= citation.StartLine && source.EndLine >= citation.EndLine {
						found = id
						break
					}
				}
				if found == "" {
					t.Error("fixture citation is not inspected")
					return
				}
				refs = append(refs, found)
			}
			wireRatings := []any{}
			for _, rating := range ratings {
				wire := map[string]any{"dimension": rating.Dimension, "value": "unknown", "rationale": rating.Rationale, "evidence": refs}
				if rating.State == RatingAssessed {
					wire["value"] = "strong"
				}
				wireRatings = append(wireRatings, wire)
			}
			raw, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"identity": item.Identity, "ratings": wireRatings, "findings": []Finding{}}}})
			content = string(raw)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	options := JudgmentOptions{Tools: []intelligence.Tool{{Function: intelligence.Function{Name: "quality_facts"}}}, Read: func(context.Context, string, json.RawMessage) (json.RawMessage, []Citation, error) {
		return nil, nil, fmt.Errorf("unexpected duplicate facts read")
	}}
	results, err := AssessBatch(t.Context(), a, a.Items, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, options)
	if err != nil || len(results) != 1 || results[0].Status != StatusScored || results[0].Name != item.Name || results[0].Declaration != item.Declaration {
		t.Fatalf("model was required to reconstruct host metadata/status: %+v %v", results, err)
	}
	if len(a.Facts[0].Predicates) == 0 || len(options.Tools) != 1 {
		t.Fatal("individual projection changed retained collective facts or shared MCP tools")
	}
	ratings[1].State, ratings[1].Value, ratings[1].Rationale = RatingUnknown, nil, "Expected interaction contract is unavailable."
	results, err = AssessBatch(t.Context(), a, a.Items, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, JudgmentOptions{})
	if err != nil || len(results) != 1 || results[0].Status != StatusInsufficient || !strings.Contains(results[0].Reason, "Expected interaction contract is unavailable.") || len(results[0].Ratings) != 2 || results[0].Ratings[0].Value == nil || *results[0].Ratings[0].Value != 3 || results[0].Ratings[1].Value != nil {
		t.Fatalf("derived partial status erased a supported judgment or invented an unknown score: %+v %v", results, err)
	}
}

func TestQualityBatchShortRelatedTests(t *testing.T) {
	a := largeFactsAssessment()
	a.Version = EvidenceRubricVersion
	batches := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []intelligence.Message
			Format   json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		var initial struct {
			Tests []struct{ Test Responsibility }
		}
		if err := json.Unmarshal([]byte(input.Messages[1].Content), &initial); err != nil {
			t.Error(err)
		}
		if len(initial.Tests) > 8 {
			t.Errorf("unbounded grading batch: %d", len(initial.Tests))
		}
		content := "Evidence review complete."
		if len(input.Format) > 0 {
			batches++
			var receipt struct{ References map[string]Citation }
			if err := json.Unmarshal([]byte(input.Messages[len(input.Messages)-1].Content), &receipt); err != nil {
				t.Error(err)
				return
			}
			results := []any{}
			for _, context := range initial.Tests {
				item := context.Test
				ref := ""
				for id, citation := range receipt.References {
					if citation == item.Declaration {
						ref = id
						break
					}
				}
				if ref == "" {
					t.Error("retained test reference unavailable")
					return
				}
				ratings := []any{
					map[string]any{"dimension": Craftsmanship, "value": "strong", "rationale": "Focused structure", "evidence": []string{ref}},
					map[string]any{"dimension": AssertionStrength, "value": "strong", "rationale": "Source-backed checks", "evidence": []string{ref}},
				}
				results = append(results, map[string]any{"identity": item.Identity, "ratings": ratings, "findings": []Finding{}})
			}
			raw, _ := json.Marshal(map[string]any{"items": results})
			content = string(raw)
		}
		event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", event)
	}))
	defer provider.Close()
	results, err := AssessBatch(context.Background(), a, a.Items, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if batches != 4 || len(results) != 32 {
		t.Fatalf("related batching lost: batches=%d results=%d", batches, len(results))
	}
}
