package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestQualityJudgmentStopsRepeatedInvalidSubmissions(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int             `json:"max_tokens"`
			Format    json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.MaxTokens > 4096 {
			t.Errorf("bounded judgment requested %d output tokens", request.MaxTokens)
		}
		calls.Add(1)
		content := "Evidence review complete."
		if len(request.Format) > 0 {
			content = `{"ratings":[]}`
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer provider.Close()
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	_, err := AssessSuite(context.Background(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 8})
	if err == nil {
		t.Fatal("invalid semantic judgment accepted")
	}
	if calls.Load() != 3 {
		t.Fatalf("repeated invalid submissions spent %d requests instead of stopping after correction", calls.Load())
	}
}

func TestQualityJudgmentMalformedJSONKeepsCorrectionLocation(t *testing.T) {
	_, err := expandCitationReferences(json.RawMessage(`{"items":[{"ratings":[{"value":"strong"}}]}]}`), nil)
	if err == nil || !strings.Contains(err.Error(), "byte") || !strings.Contains(err.Error(), `strong`) {
		t.Fatalf("syntax correction lost the exact malformed location: %v", err)
	}
}

func TestQualityJudgmentFinalSubmissionErrorKeepsSchemaPath(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		fields     []string
	}{
		{"missing-rating-value", `{"ratings":[{"dimension":"behavior-adequacy","rationale":"Output checked","evidence":[]}],"expectations":[],"findings":[]}`, []string{"ratings", "value"}},
		{"schema-instead-of-data", `{"type":"object","properties":{}}`, []string{"submission schema", "validating root", "type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				args := tc.args
				raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": args}, "finish_reason": "stop"}}})
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}))
			defer provider.Close()
			a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
			_, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 1})
			for _, field := range tc.fields {
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("final submission lost its actionable schema error: %v", err)
				}
			}
		})
	}
}

func TestQualityJudgmentRecordsRejectedSubmissionTrace(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{}\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer provider.Close()
	var trace []ReviewTrace
	options := JudgmentOptions{Trace: func(event ReviewTrace) error { trace = append(trace, event); return nil }}
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	_, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 1}, options)
	found := false
	for _, event := range trace {
		found = found || event.Kind == "validation" && strings.Contains(event.Error, "submission schema") && string(event.Args) == "{}"
	}
	if err == nil || !found {
		t.Fatalf("failure lost request/response/validation provenance: %v %+v", err, trace)
	}
}

func TestQualityJudgmentStructuredSubmissionAfterNaturalEvidenceCompletion(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Format   json.RawMessage        `json:"response_format"`
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		content := "Evidence review complete."
		if len(request.Format) > 0 {
			var receipt struct{ References map[string]Citation }
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Error(err)
				return
			}
			ref := ""
			for id, citation := range receipt.References {
				if citation == a.Items[0].Declaration {
					ref = id
					break
				}
			}
			if ref == "" {
				t.Error("test source reference unavailable")
				return
			}
			proposal, err := json.Marshal(map[string]any{
				"ratings": []any{
					map[string]any{"dimension": BehaviorAdequacy, "value": "absent", "rationale": "No checks present.", "evidence": []string{ref}},
					map[string]any{"dimension": ConditionAdequacy, "value": "unknown", "rationale": "Usage contract is unavailable.", "evidence": []Citation{}},
				},
				"expectations": []ExpectedBehavior{}, "findings": []Finding{},
			})
			if err != nil {
				t.Error(err)
				return
			}
			content = string(proposal)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	result, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, JudgmentOptions{})
	if err != nil || result.Status != StatusInsufficient || calls.Load() != 2 {
		t.Fatalf("natural evidence completion was rejected or accepted as the result: %+v %v calls=%d", result, err, calls.Load())
	}
	if !strings.Contains(result.Reason, "Usage contract is unavailable.") || len(result.Ratings) != 2 || result.Ratings[0].Value == nil || *result.Ratings[0].Value != 0 || result.Ratings[1].Value != nil {
		t.Fatalf("collective partial status lost supported evidence or invented an unknown rating: %+v", result)
	}
}

func TestQualityJudgmentReservesCorrectionWithinRequestBudget(t *testing.T) {
	var calls, submissions int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Format json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		delta := map[string]any{}
		if len(request.Format) == 0 {
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprint(calls), "function": map[string]string{"name": "inspect", "arguments": fmt.Sprintf(`{"page":%d}`, calls)}}}
		} else {
			submissions++
			delta["content"] = fmt.Sprintf(`{"ok":%t}`, submissions > 1)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	a := factsAssessment("package example;import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	options := JudgmentOptions{
		Tools: []intelligence.Tool{{Type: "function", Function: intelligence.Function{Name: "inspect", Parameters: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer"}}}`)}}},
		Read: func(context.Context, string, json.RawMessage) (json.RawMessage, []Citation, error) {
			return json.RawMessage(`{"source":"retained evidence"}`), nil, nil
		},
	}
	_, err := reviewJudgment(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 8}, "Review retained evidence.", map[string]any{}, nil, "submit_suite", json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`), options, func(raw json.RawMessage, _ []Citation) error {
		var proposal struct{ OK bool }
		if err := json.Unmarshal(raw, &proposal); err != nil {
			return err
		}
		if !proposal.OK {
			return fmt.Errorf("missing supported judgment")
		}
		return nil
	})
	if err != nil || submissions != 2 || calls > 8 {
		t.Fatalf("investigation consumed the bounded correction opportunity: %v submissions=%d calls=%d", err, submissions, calls)
	}
}

func TestQualityJudgmentRoutesRubricAndSourceReceiptsToFinalPhase(t *testing.T) {
	const rubric = "fixture grading rubric"
	citation := Citation{SnapshotID: "owned", FileID: "contract", StartLine: 4, EndLine: 5}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message `json:"messages"`
			Format   json.RawMessage        `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := "Evidence gathering complete."
		if len(request.Format) == 0 {
			if request.Messages[0].Content == rubric {
				t.Error("grading rubric was sent during evidence gathering")
			}
		} else {
			if !strings.HasPrefix(request.Messages[0].Content, rubric) {
				t.Error("final judgment did not receive its grading rubric")
			}
			for _, message := range request.Messages {
				if message.Role == "assistant" || message.Role == "tool" {
					t.Error("final grading inherited provisional investigation conversation rather than evidence data")
				}
			}
			var references struct {
				InspectedSource []Citation `json:"inspectedSource"`
			}
			err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &references)
			if err != nil || len(references.InspectedSource) != 1 || references.InspectedSource[0] != citation {
				t.Errorf("final judgment lost explicit inspected-source references: %+v %v", references, err)
			}
			content = `{"ok":true}`
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	a := factsAssessment("package example;import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	result, err := reviewJudgment(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, rubric, map[string]any{"retained": "source"}, []Citation{citation}, "submit_suite", json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), JudgmentOptions{}, func(raw json.RawMessage, _ []Citation) error {
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("missing structured judgment")
		}
		return nil
	})
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("phase-separated submission failed: %s %v", result, err)
	}
}

func TestQualityJudgmentBoundReferencesExpandOnlyInspectedEvidence(t *testing.T) {
	citation := Citation{SnapshotID: "owned", FileID: "contract", StartLine: 4, EndLine: 5}
	for _, tc := range []struct {
		reference string
		valid     bool
	}{{"source-1", true}, {"outside", false}} {
		t.Run(tc.reference, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Format json.RawMessage `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				content := "Evidence ready."
				if len(request.Format) > 0 {
					raw, _ := json.Marshal(map[string]any{"evidence": []string{tc.reference}})
					content = string(raw)
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}))
			defer provider.Close()
			a := factsAssessment("package example;import \"testing\"\nfunc TestF(t *testing.T) {}\n")
			accepted := false
			_, err := reviewJudgment(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2}, "Review retained evidence.", map[string]any{}, []Citation{citation}, "submit_suite", json.RawMessage(`{"type":"object","properties":{"evidence":{"type":"array","items":{"type":"object","properties":{"snapshotID":{"type":"string"},"fileID":{"type":"string"},"startLine":{"type":"integer"},"endLine":{"type":"integer"}},"required":["snapshotID","fileID","startLine","endLine"]}}},"required":["evidence"]}`), JudgmentOptions{}, func(raw json.RawMessage, _ []Citation) error {
				var result struct{ Evidence []Citation }
				if err := json.Unmarshal(raw, &result); err != nil {
					return err
				}
				if len(result.Evidence) != 1 || result.Evidence[0] != citation {
					return fmt.Errorf("wrong retained citation")
				}
				accepted = true
				return nil
			})
			if (err == nil) != tc.valid || accepted != tc.valid {
				t.Fatalf("bound evidence admission: accepted=%v error=%v", accepted, err)
			}
		})
	}
}

func TestQualityJudgmentFindingsNeedEvidenceNotBoilerplate(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	item := a.Items[0]
	item.Status, item.Reason = StatusInsufficient, "Contract unknown"
	item.Findings = []Finding{{Category: "reliability", Kind: "guess", Title: "Claim", Evidence: []Citation{item.Declaration}}}
	if err := validateIndividual(item, []Citation{item.Declaration}); err == nil {
		t.Fatal("unchecked partial finding accepted")
	}
	item.Findings[0].Kind = "inference"
	item.Findings[0].Rationale = "The inspected check may miss an outcome."
	if err := validateIndividual(item, []Citation{item.Declaration}); err != nil {
		t.Fatalf("useful advisory findings do not need mandatory consequence/suggestion text: %v", err)
	}
}

func TestQualityJudgmentDoesNotDemandContractForAHighOpinion(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	item := a.Items[0]
	item.Status = StatusScored
	value := 3
	for _, dimension := range []Dimension{Craftsmanship, AssertionStrength} {
		item.Ratings = append(item.Ratings, Rating{Dimension: dimension, State: RatingAssessed, Value: &value, Rationale: "Checks the observed result", Evidence: []Citation{item.Declaration}})
	}
	if err := validateIndividual(item, []Citation{item.Declaration}); err != nil {
		t.Fatalf("score-dependent evidence requirements must not force an advisory opinion: %v", err)
	}
	item.Ratings[1].Evidence[0].FileID = "invented"
	if err := validateIndividual(item, []Citation{item.Declaration}); err == nil {
		t.Fatal("advisory scores bypassed citation provenance")
	}
}

func TestQualityJudgmentStopsRepeatedEvidenceRequests(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		args := `{"snapshotID":"snapshot","operation":"source","args":{"fileID":"tests","startLine":2,"endLine":2}}`
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "read", "function": map[string]string{"name": "read_evidence", "arguments": args}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	_, err := AssessSuite(t.Context(), a, intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 8})
	if err == nil {
		t.Fatal("repeated evidence requests treated as a judgment")
	}
	if calls.Load() > 2 {
		t.Fatalf("repeated source requests made %d provider calls", calls.Load())
	}
}

func TestQualitySuiteDocumentedBasisRequiresActualDocumentation(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	suite := SuiteAssessment{Status: StatusInsufficient, Reason: "Partial", Expectations: []ExpectedBehavior{{ID: "behavior", Description: "Documented return", Basis: "documented", State: "supported", Rationale: "Claim", Evidence: []Citation{a.Items[0].Declaration}, Tests: []Identity{a.Items[0].Identity}}}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err == nil {
		t.Fatal("uncommented test code became documented contract evidence")
	}
	document := "Contract: F returns one."
	a = factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	a.Reports[0].Report.Documents = []review.Document{{ID: "contract", Path: "contract.md", Text: document, SHA256: Digest([]byte(document))}}
	original := a.Items[0].Declaration
	contract := Citation{SnapshotID: original.SnapshotID, FileID: "contract", StartLine: 1, EndLine: 1}
	suite.Expectations[0].Evidence = []Citation{contract, original}
	initial, seen, err := batchContext(t.Context(), a, a.Items)
	if err != nil {
		t.Fatal(err)
	}
	included := false
	for _, source := range initial.Shared {
		if strings.Contains(string(source), "Contract: F returns one.") {
			included = true
		}
	}
	if !included {
		t.Fatal("shared retained document was omitted from collective context")
	}
	if err := validateSuite(a, suite, seen); err != nil {
		t.Fatal(err)
	}
}

func TestQualityFactsPreserveValidFactsAlongsideSyntaxLimit(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\nfunc broken(\n")
	a.Items[0].Declaration.EndLine = 2
	a.Reports[0].Report.TestSources[0].Declarations[0].Location.EndLine = 2
	facts, err := AnalyzeFacts(a)
	if err != nil {
		t.Fatalf("whole retained analysis lost for partial syntax: %v", err)
	}
	if len(facts[0].Checks) != 1 {
		t.Fatal("available deterministic facts erased")
	}
	found := false
	for _, limit := range facts[0].Limitations {
		if len(limit) > 0 && limit[:min(len(limit), 6)] == "Syntax" {
			found = true
		}
	}
	if !found {
		t.Fatal("partial syntax evidence limit hidden")
	}
}
