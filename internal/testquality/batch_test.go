package testquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestQualityBatchContextScopesDiagnosticSummariesWithoutRepeatingCommandOutput(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {if got,err:=Classify(1);got!="accepted"||err!=nil {t.Fatal(got,err)}}`)
	unrelated := a.Items[0].Identity
	unrelated.SymbolID = "another-test"
	a.Diagnostics = []DiagnosticRecord{
		{Request: DiagnosticRequest{Tests: []Identity{unrelated}}, Status: "pass"},
		{Request: DiagnosticRequest{Tests: []Identity{a.Items[0].Identity}, Reason: "Repeat selected test"}, Status: "partial", Error: "second attempt failed", SourceVerified: true,
			Runs: []review.UnitTestRun{{Commands: []review.Command{{Stdout: strings.Repeat("verbose-test-event\n", 20000)}}}}},
	}
	before, _ := json.Marshal(a.Diagnostics)
	initial, _, err := batchContext(t.Context(), a, a.Items)
	if err != nil {
		t.Fatal(err)
	}
	for i := range initial.Tests {
		initial.Tests[i].Facts.Predicates = nil
	}
	raw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Diagnostics []struct {
			Index          int               `json:"diagnosticIndex"`
			Request        DiagnosticRequest `json:"request"`
			Status         string            `json:"status"`
			Error          string            `json:"error"`
			SourceVerified bool              `json:"sourceVerified"`
		}
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Index != 1 ||
		projection.Diagnostics[0].Request.Tests[0] != a.Items[0].Identity ||
		projection.Diagnostics[0].Status != "partial" || projection.Diagnostics[0].Error != "second attempt failed" || !projection.Diagnostics[0].SourceVerified {
		t.Fatalf("diagnostic scope, result or drill-down identity lost (%d records, %d bytes)", len(projection.Diagnostics), len(raw))
	}
	if len(raw) >= 16<<10 || strings.Contains(string(raw), "verbose-test-event") {
		t.Fatalf("raw diagnostic output inflated selected-test context to %d bytes", len(raw))
	}
	after, _ := json.Marshal(a.Diagnostics)
	if string(before) != string(after) {
		t.Fatal("prompt projection changed retained diagnostic evidence")
	}
}

type qualityRequestTransport func(*http.Request) (*http.Response, error)

func (f qualityRequestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestQualityBatchRequestsFollowOwnerDeadline(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {if got,err:=Classify(1);got!="accepted"||err!=nil {t.Fatal(got,err)}}`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	want, _ := ctx.Deadline()
	var got time.Time
	original := http.DefaultTransport
	http.DefaultTransport = qualityRequestTransport(func(r *http.Request) (*http.Response, error) {
		got, _ = r.Context().Deadline()
		return nil, errors.New("stop after inspecting request deadline")
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	_, _ = AssessBatch(ctx, a, a.Items, intelligence.Config{Enabled: true, BaseURL: "http://127.0.0.1:1", Model: "fixture"})
	if !got.Equal(want) {
		t.Fatalf("review request replaced owner deadline %s with %s", want, got)
	}
}

func TestQualityBatchProviderFailureDoesNotBecomeInsufficientEvidence(t *testing.T) {
	a := largeFactsAssessment()
	a.Version = EvidenceRubricVersion
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	results, err := AssessBatch(t.Context(), a, a.Items, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("provider failure must be returned to the job owner, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("provider failure must stop subsequent batches, got %d requests", requests)
	}
	for _, item := range results {
		if item.Status != StatusUnreviewed {
			t.Fatalf("provider failure became an evidence judgment: %+v", item)
		}
	}
}

func TestQualityBatchRequestCancellation(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
	ready := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer provider.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := AssessBatch(ctx, a, a.Items, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
		done <- err
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment did not reach provider")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation was lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight assessment ignored cancellation")
	}
}

func TestQualitySavedPromptSize(t *testing.T) {
	directory, id := os.Getenv("GO_REVIEW_QUALITY_PROMPT_DIRECTORY"), os.Getenv("GO_REVIEW_QUALITY_PROMPT_ID")
	if directory == "" || id == "" {
		t.Skip("opt-in read-only saved-archive prompt measurement")
	}
	a, err := (Store{Directory: directory}).Load(id)
	if err != nil {
		t.Fatal(err)
	}
	items := append([]Responsibility{}, a.Items...)
	for i := range items {
		if items[i].Status != StatusExcluded {
			items[i].Status = StatusUnreviewed
		}
	}
	batches, err := PlanBatches(a, items, intelligence.Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldTotal, newTotal, oldMax, newMax := 0, 0, 0, 0
	for _, batch := range batches {
		initial, _, err := batchContext(t.Context(), a, batch)
		if err != nil {
			t.Fatal(err)
		}
		for i := range initial.Tests {
			initial.Tests[i].Facts.Predicates = nil
		}
		raw, err := json.Marshal(initial)
		if err != nil {
			t.Fatal(err)
		}
		var legacy map[string]any
		if err := json.Unmarshal(raw, &legacy); err != nil {
			t.Fatal(err)
		}
		legacy["diagnostics"] = a.Diagnostics
		oldRaw, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		oldTotal += len(oldRaw)
		newTotal += len(raw)
		oldMax = max(oldMax, len(oldRaw))
		newMax = max(newMax, len(raw))
	}
	t.Logf("read-only saved assessment %s: tests=%d batches=%d; initial-context total old=%d new=%d bytes; largest old=%d new=%d bytes; no model requests", id, len(items), len(batches), oldTotal, newTotal, oldMax, newMax)
}

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
