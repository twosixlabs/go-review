package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestQueryFindingsRetainQualifiedScopeAndFilters(t *testing.T) {
	r := testReport(t.TempDir())
	other := testReport(t.TempDir())
	other.Module = "other"
	other.Symbols[0].Package = "other/service"
	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}, {Report: other}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Items[0].Findings = []Finding{{Title: "Return value checked", Category: "strength", Kind: "observation", Rationale: "Assertion checks behavior."}}
	raw, err := Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "findings", Category: "strength", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total int `json:"total"`
		Items []struct {
			Identity Identity `json:"identity"`
			Title    string   `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Identity != a.Items[0].Identity {
		t.Fatalf("scope lost: %s", raw)
	}
	raw, err = Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "findings", SnapshotID: a.Items[1].Identity.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Total != 0 {
		t.Fatalf("cross-snapshot findings leaked: %s %v", raw, err)
	}
}

func TestQueryCollectiveDetailsRemainPageable(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
	behavior := ExpectedBehavior{ID: "range", Description: "Accept values in range", Basis: "documented", State: "supported"}
	for i := range 800 {
		behavior.Conditions = append(behavior.Conditions, ExpectedCondition{ID: fmt.Sprint(i), Description: "A retained condition", State: "supported", Rationale: strings.Repeat("evidence ", 12)})
	}
	a.Suite = &SuiteAssessment{Status: StatusScored, Expectations: []ExpectedBehavior{behavior}, Findings: []Finding{{Title: "Collective observation", Rationale: "Retained suite finding"}}}
	list, err := Query(t.Context(), a, QueryInput{AssessmentID: a.ID, Operation: "expectations", Limit: 1})
	if err != nil {
		t.Fatalf("one expectation must be navigable even with many conditions: %v", err)
	}
	if strings.Contains(string(list), `"conditions":[`) {
		t.Fatal("expectation listing eagerly embedded all condition details")
	}
	for _, tc := range []struct {
		args, want string
	}{
		{`"operation":"expectation","expectationID":"range"`, `"id":"range"`},
		{`"operation":"conditions","expectationID":"range","offset":799,"limit":1`, `"id":"799"`},
		{`"operation":"condition","expectationID":"range","conditionID":"799"`, `"id":"799"`},
		{`"operation":"suite-findings","limit":1`, `"Collective observation"`},
	} {
		var input QueryInput
		if err := json.Unmarshal([]byte(`{"assessmentID":"`+a.ID+`",`+tc.args+`}`), &input); err != nil {
			t.Fatal(err)
		}
		raw, err := Query(t.Context(), a, input)
		if err != nil || !strings.Contains(string(raw), tc.want) {
			t.Fatalf("retained collective detail unavailable for %s: %s %v", tc.args, raw, err)
		}
	}
}

func TestQueryDiagnosticDetailsRemainPageable(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
	run := review.UnitTestRun{Status: "partial", Error: "A repeated attempt failed"}
	for i := range 800 {
		run.Attempts = append(run.Attempts, review.UnitTestAttempt{Test: "qualityfixture::TestContract", Attempt: i + 1, Outcome: "fail"})
	}
	a.Diagnostics = []DiagnosticRecord{{Status: "partial", SourceVerified: true, Runs: []review.UnitTestRun{run}, Artifacts: []Artifact{{Path: "diagnostic/events", SHA256: "retained-hash"}}}}
	raw, err := Query(t.Context(), a, QueryInput{AssessmentID: a.ID, Operation: "diagnostics", Limit: 1})
	if err != nil {
		t.Fatalf("one diagnostic must be navigable regardless of retained attempt count: %v", err)
	}
	if strings.Contains(string(raw), `"attempts":[`) {
		t.Fatal("diagnostic listing eagerly embedded every attempt")
	}
	for _, tc := range []struct {
		args, want string
	}{
		{`"operation":"diagnostic-runs","diagnosticIndex":0`, `"A repeated attempt failed"`},
		{`"operation":"diagnostic-attempts","diagnosticIndex":0,"runIndex":0,"offset":799,"limit":1`, `"attempt":800`},
		{`"operation":"diagnostic-artifacts","diagnosticIndex":0`, `"retained-hash"`},
	} {
		var input QueryInput
		if err := json.Unmarshal([]byte(`{"assessmentID":"`+a.ID+`",`+tc.args+`}`), &input); err != nil {
			t.Fatal(err)
		}
		raw, err := Query(t.Context(), a, input)
		if err != nil || !strings.Contains(string(raw), tc.want) {
			t.Fatalf("retained diagnostic detail unavailable for %s: %s %v", tc.args, raw, err)
		}
	}
	for _, input := range []QueryInput{
		{AssessmentID: a.ID, Operation: "diagnostic-runs", DiagnosticIndex: -1},
		{AssessmentID: a.ID, Operation: "diagnostic-runs", DiagnosticIndex: 1},
		{AssessmentID: a.ID, Operation: "diagnostic-attempts", RunIndex: 1},
	} {
		if _, err := Query(t.Context(), a, input); err == nil {
			t.Fatalf("out-of-owner diagnostic detail accepted: %+v", input)
		}
	}
}

func TestQueryPagesBoundEncodedBytes(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
	a.Suite = &SuiteAssessment{}
	for i := range 12 {
		a.Suite.Expectations = append(a.Suite.Expectations, ExpectedBehavior{ID: fmt.Sprint(i), Description: strings.Repeat("long retained description ", 180)})
	}
	offset := 0
	for {
		raw, err := Query(t.Context(), a, QueryInput{AssessmentID: a.ID, Operation: "expectations", Offset: offset, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Items      []struct{ ID string }
			NextOffset *int
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 32<<10 || len(result.Items) == 0 {
			t.Fatal("page is oversized or made no progress")
		}
		for _, item := range result.Items {
			if item.ID != fmt.Sprint(offset) {
				t.Fatal("byte-bound pagination skipped or duplicated retained rows")
			}
			offset++
		}
		if result.NextOffset == nil {
			break
		}
		if *result.NextOffset != offset {
			t.Fatal("page cursor does not track returned rows")
		}
	}
	if offset != 12 {
		t.Fatal("not all retained rows were reachable")
	}
}
