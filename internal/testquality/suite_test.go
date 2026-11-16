package testquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

func TestQualitySuiteProviderFailureStopsGroupRequests(t *testing.T) {
	a := largeFactsAssessment()
	a.Version = EvidenceRubricVersion
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	_, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || requests.Load() != 1 {
		t.Fatalf("collective provider failure was hidden or repeated: requests=%d error=%v", requests.Load(), err)
	}
}

func TestQualitySuiteContextDoesNotTreatIndividualJudgmentsAsEvidence(t *testing.T) {
	a := factsAssessment("package example;import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	a.Items[0].Status = StatusScored
	value := 4
	a.Items[0].Ratings = []Rating{{Dimension: Craftsmanship, State: RatingAssessed, Value: &value, Rationale: "Prior judgment"}}
	a.Items[0].Findings = []Finding{{Title: "Prior subjective finding"}}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message
			Format   json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var initial judgmentContext
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
			return
		}
		if len(initial.Tests) != 1 || len(initial.Tests[0].Test.Ratings) != 0 || len(initial.Tests[0].Test.Findings) != 0 {
			t.Error("individual subjective judgments leaked into collective evidence")
		}
		content := "Evidence ready."
		if len(request.Format) > 0 {
			content = `{"ratings":[{"dimension":"behavior-adequacy","value":"unknown","rationale":"Contract unavailable.","evidence":[]},{"dimension":"conditions-usage-adequacy","value":"unknown","rationale":"Usage contract unavailable.","evidence":[]}],"expectations":[],"findings":[]}`
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	_, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, JudgmentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items[0].Ratings) != 1 || *a.Items[0].Ratings[0].Value != 4 || len(a.Items[0].Findings) != 1 {
		t.Fatal("collective projection changed retained individual results")
	}
}

func TestQualityRubricPartialIndividualJudgments(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	a.Version = "test-quality/3"
	a.Status = "partial"
	value := 3
	a.Items[0].Status = StatusInsufficient
	a.Items[0].Ratings = []Rating{
		{Dimension: "test-craftsmanship", State: RatingAssessed, Value: &value, Rationale: "Readable focused structure", Evidence: []Citation{a.Items[0].Declaration}},
		{Dimension: AssertionStrength, State: RatingUnknown, Rationale: "Expected behavior is unavailable"},
	}
	data, err := Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "overview"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Categories map[string]*float64 }
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Categories["test-craftsmanship"] == nil || *out.Categories["test-craftsmanship"] != 75 {
		t.Fatalf("supported craftsmanship was erased: %s", data)
	}
	if out.Categories["assertion-strength"] != nil {
		t.Fatal("unknown assertion evidence became a score")
	}
	if _, exists := out.Categories["behavior-paths"]; exists {
		t.Fatal("old per-test dimension leaked into new rubric")
	}
}

func TestQualitySuiteIsNotAnAverageOfTests(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	// The public saved projection must expose a distinct suite, not manufacture
	// it from individual scores. An unassessed suite remains visibly unknown.
	a.Version = "test-quality/3"
	data, err := Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "suite"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status  Status
		Ratings []Rating
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status == StatusScored {
		t.Fatalf("invented collective judgment: %s", data)
	}
}

func TestQualitySuiteLeavesCheckingInterpretationToTheReviewer(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {t.Log(F())}\n")
	a.Version = EvidenceRubricVersion
	suite := SuiteAssessment{Status: StatusInsufficient, Reason: "Partial", Expectations: []ExpectedBehavior{{ID: "behavior", Description: "Verifies output", Basis: "implementation-derived", State: "supported", Rationale: "Claim", Evidence: []Citation{a.Items[0].Declaration}, Tests: []Identity{a.Items[0].Identity}}}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err != nil {
		t.Fatalf("static checking-site recognition must not gate advisory opinions: %v", err)
	}

	suite.Expectations[0].Tests = nil
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err != nil {
		t.Fatalf("source-cited advice must not require duplicate contributor metadata: %v", err)
	}
	suite.Expectations[0].Tests = []Identity{{SymbolID: "invented"}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err == nil {
		t.Fatal("advisory interpretation bypassed retained test identity validation")
	}
	suite.Expectations[0].Tests = nil
	suite.Expectations[0].Scope = []Identity{{SymbolID: "invented"}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err == nil {
		t.Fatal("group scope bypassed retained test identity validation")
	}
}

// The provider double exercises bounded delivery and retention, not model
// accuracy. Sending the whole scope at once must fail this configured budget.
func TestQualitySuiteLargeScopeKeepsEveryGroupAndDirectOverallJudgment(t *testing.T) {
	var bodies strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&bodies, "func TestCase%d(t *testing.T) {got,err:=Classify(1);if got!=\"accepted\"||err!=nil {t.Fatal(got,err)}}\n", i)
	}
	a := qualityLiveAssessment(t, bodies.String())
	inspected := map[Identity]bool{}
	var failFirst atomic.Bool
	var readWholeSource atomic.Bool
	sourceEnd := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message
			Format   json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var initial struct {
			Tests  []testContext
			Groups []json.RawMessage
			Shared []json.RawMessage
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
			return
		}
		for _, test := range initial.Tests {
			inspected[test.Test.Identity] = true
		}
		if readWholeSource.Load() && len(request.Format) == 0 && len(initial.Tests) > 0 {
			args, _ := json.Marshal(map[string]any{"snapshotID": initial.Tests[0].Test.Identity.SnapshotID, "operation": "source",
				"args": map[string]any{"fileID": "contract", "startLine": 1, "endLine": sourceEnd, "textLimit": 12000}})
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0, "id": "source", "function": map[string]string{"name": "read_evidence", "arguments": string(args)}}},
			}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			return
		}
		if len(initial.Groups) > 0 {
			source, _ := json.Marshal(initial.Shared)
			if !strings.Contains(string(source), "func Classify") {
				t.Error("overall review inherited opinions without retained source evidence")
			}
		}
		content := "Evidence ready."
		if len(request.Format) > 0 {
			var receipt struct{ References map[string]Citation }
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Error(err)
				return
			}
			ref := ""
			for id, c := range receipt.References {
				if c.FileID == "contract" && (!readWholeSource.Load() || c.StartLine == 1 && c.EndLine == sourceEnd) {
					ref = id
					break
				}
			}
			if ref == "" {
				t.Error("retained contract was lost")
				return
			}
			value := "weak"
			expectations := []any{map[string]any{
				"id": "accepted", "description": "Acceptance behavior", "basis": "documented",
				"state": "supported", "rationale": "Acceptance is checked.", "evidence": []string{ref}, "conditions": []any{},
			}}
			if len(initial.Groups) > 0 {
				value = "strong"
				expectations = []any{}
			}
			ratings := []any{}
			for _, dimension := range []Dimension{BehaviorAdequacy, ConditionAdequacy} {
				ratings = append(ratings, map[string]any{"dimension": dimension, "value": value, "rationale": "Advisory interpretation", "evidence": []string{ref}})
			}
			raw, _ := json.Marshal(map[string]any{"ratings": ratings, "expectations": expectations, "findings": []any{}})
			content = string(raw)
			if failFirst.Load() && len(initial.Tests) > 0 && initial.Tests[0].Test.Identity == a.Items[0].Identity {
				content = `{"ratings":[]}`
			}
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	result, err := AssessSuite(t.Context(), a, intelligence.Config{
		Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2, ContextBytes: 65536,
	}, JudgmentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected) != 128 || len(result.Scope) != 128 || len(result.Expectations) < 2 {
		t.Fatalf("large scope was dropped: inspected=%d scope=%d expectations=%d", len(inspected), len(result.Scope), len(result.Expectations))
	}
	ids := map[string]bool{}
	for _, expectation := range result.Expectations {
		if ids[expectation.ID] {
			t.Fatal("group-local identities collided in the retained suite")
		}
		ids[expectation.ID] = true
		raw, _ := json.Marshal(expectation)
		var provenance struct{ Scope []Identity }
		if err := json.Unmarshal(raw, &provenance); err != nil {
			t.Fatal(err)
		}
		if len(provenance.Scope) == 0 || len(provenance.Scope) >= 128 {
			t.Fatal("a group observation lost its selected-test scope or claimed the whole suite")
		}
	}
	for _, rating := range result.Ratings {
		if rating.Value == nil || *rating.Value != 3 {
			t.Fatal("overall judgment was replaced by an average of group opinions")
		}
	}
	failFirst.Store(true)
	partial, err := AssessSuite(t.Context(), a, intelligence.Config{
		Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2, ContextBytes: 65536,
	}, JudgmentOptions{})
	if err == nil || partial.Status != StatusInsufficient || len(partial.Expectations) == 0 || len(partial.Scope) != 128 {
		t.Fatal("one failed group erased other completed advice or became a successful complete result")
	}
	failFirst.Store(false)
	checkpointErr := errors.New("fixture checkpoint denied")
	requests := a.Review.Requests
	partial, err = AssessSuite(t.Context(), a, intelligence.Config{
		Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2, ContextBytes: 65536,
	}, JudgmentOptions{CheckpointSuite: func(SuiteAssessment) error { return checkpointErr }})
	if !errors.Is(err, checkpointErr) || len(partial.Expectations) == 0 || a.Review.Requests-requests > 2 {
		t.Fatal("checkpoint failure lost its cause, discarded completed work or continued inference")
	}
	bodies.Reset()
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&bodies, "func TestCase%d(t *testing.T) {got,err:=Classify(1);if got!=\"accepted\"||err!=nil {t.Fatal(got,err)}}\n", i)
	}
	source := qualityLiveAssessment(t, bodies.String()).Reports[0].Report
	file := &source.Files[0]
	file.Source = strings.Replace(file.Source, "func Classify", strings.Repeat("// "+strings.Repeat("\u00e9", 80)+"\n", 90)+"func Classify", 1)
	file.SHA256 = Digest([]byte(file.Source))
	sourceEnd = len(strings.Split(file.Source, "\n"))
	a, err = (Store{Directory: t.TempDir()}).Create(t.Context(), &review.RepositoryReview{
		Root: source.Root, Modules: []review.ReviewedModule{{Report: source}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	readWholeSource.Store(true)
	query, err := webui.NewReviewQuery(source)
	if err != nil {
		t.Fatal(err)
	}
	options := JudgmentOptions{Tools: []intelligence.Tool{{Type: "function", Function: intelligence.Function{
		Name: "read_evidence", Parameters: json.RawMessage(readJudgmentSchema),
	}}}, Read: func(ctx context.Context, _ string, raw json.RawMessage) (json.RawMessage, []Citation, error) {
		var input EvidenceInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, nil, err
		}
		data, err := query.Query(ctx, input.Operation, input.Args)
		if err != nil {
			return nil, nil, err
		}
		var citation Citation
		if err := json.Unmarshal(data, &citation); err != nil {
			return nil, nil, err
		}
		citation.SnapshotID = input.SnapshotID
		return data, []Citation{citation}, nil
	}}
	if _, err := AssessSuite(t.Context(), a, intelligence.Config{
		Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2, ContextBytes: 65536,
	}, options); err != nil {
		t.Fatal(err)
	}
}
