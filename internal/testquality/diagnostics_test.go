package testquality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestQualityDiagnosticPolicy(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	r := a.Reports[0].Report
	r.SourceSHA256 = "source"
	r.UnitEvidence = &review.UnitEvidence{SourceSHA256: "source", Runs: []review.UnitTestRun{{Selection: review.UnitTestSelection{Package: "example", Tests: []string{"TestF"}}, MatchedTests: []string{"TestF"}, Outcomes: map[string]string{"example::TestF": "pass"}}}}
	request := DiagnosticRequest{Kind: "repeat", Tests: []Identity{a.Items[0].Identity}, Repetitions: 2, Reason: "Check retained reliability risk", Evidence: []Citation{a.Items[0].Declaration}}
	policy := DiagnosticPolicy{AllowExecution: true, MaxCommands: 8, MaxRepetitions: 3}
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request, request}); err == nil {
		t.Fatal("redundant diagnostic commands accepted")
	}
	policy.AllowExecution = false
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request}); err == nil {
		t.Fatal("execution opt-out was broadened")
	}
	policy.AllowExecution = true
	request.Tests[0].SymbolID = "unreviewed"
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request}); err == nil {
		t.Fatal("unreviewed test was authorized")
	}
	request.Tests[0] = a.Items[0].Identity
	r.UnitEvidence.SourceSHA256 = "different"
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request}); err == nil {
		t.Fatal("stale reviewed pool was authorized")
	}
}

func TestQualityDiagnosticPlanDoesNotReportExecutionExhaustion(t *testing.T) {
	a := factsAssessment(`package example; import ("testing"; "time")
func TestF(t *testing.T) { time.Sleep(time.Millisecond) }
func TestSecond(t *testing.T) { time.Sleep(time.Millisecond) }
`)
	r := a.Reports[0].Report
	a.Items[0].Declaration.EndLine = 2
	second := a.Items[0]
	second.Identity.SymbolID, second.Name = "second", "TestSecond"
	second.Declaration.StartLine, second.Declaration.EndLine = 3, 3
	a.Items = append(a.Items, second)
	r.TestSources[0].Declarations[0].Location.EndLine = 2
	r.TestSources[0].Declarations = append(r.TestSources[0].Declarations, review.TestDeclaration{
		ID: "second", Name: "TestSecond", Kind: "test",
		Location: review.Location{FileID: "tests", Line: 3, EndLine: 3},
	})
	r.SourceSHA256 = "source"
	r.UnitEvidence = &review.UnitEvidence{SourceSHA256: "source", Runs: []review.UnitTestRun{{
		Selection:    review.UnitTestSelection{Package: "example"},
		MatchedTests: []string{"TestF", "TestSecond"},
	}}}
	policy := DefaultDiagnosticPolicy(true)
	policy.MaxCommands = 1
	requests, reason, err := PlanDiagnostics(a, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one planned diagnostic, got %d: %s", len(requests), reason)
	}
	if strings.Contains(strings.ToLower(reason), "exhausted") || strings.Contains(strings.ToLower(reason), "unresolved") {
		t.Fatalf("planning must not claim execution or risk outcomes before diagnostics run: %s", reason)
	}
	if !strings.Contains(reason, "1 command") || !strings.Contains(reason, "not selected") {
		t.Fatalf("partial selection must explain its command limit and omitted candidates: %s", reason)
	}
}

func TestQualityDiagnosticCollectorRetainsAttemptsAndScope(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "unreviewed-ran")
	failureMarker := filepath.Join(t.TempDir(), "fail-reviewed-test")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package example
import ("testing"; "os")
func TestAllowed(t *testing.T) {if _,err:=os.Stat(FAIL_MARKER);err==nil {t.Fatal("requested test failure")};if 1+1!=2 {t.Fatal("wrong")}}
func TestUnreviewed(t *testing.T) {os.WriteFile(MARKER,[]byte("ran"),0600)}
`
	raw, _ := json.Marshal(failureMarker)
	source = strings.ReplaceAll(source, "FAIL_MARKER", string(raw))
	raw, _ = json.Marshal(marker)
	source = strings.ReplaceAll(source, "MARKER", string(raw))
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package example\nfunc F() int {return 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := review.Analyze(t.Context(), review.Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := review.CollectUnitEvidence(t.Context(), r, review.UnitEvidenceOptions{Selections: []review.UnitTestSelection{{Package: "example", Tests: []string{"TestAllowed"}}}, ArtifactDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// Partial retained flow projections must remain unchanged too.
	r.OperationSources = nil
	r.SystemFlow = review.BuildSystemFlow(r)
	store := Store{Directory: t.TempDir()}
	a, err := store.Create(t.Context(), &review.RepositoryReview{Root: root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var selected Responsibility
	for _, item := range a.Items {
		if item.Name == "TestAllowed" {
			selected = item
		}
	}
	request := DiagnosticRequest{Kind: "repeat", Tests: []Identity{selected.Identity}, Repetitions: 2, Reason: "Repeat authorized retained test", Evidence: []Citation{selected.Declaration}}
	before, _ := json.Marshal(a.Reports[0].Report)
	record, err := RunDiagnostic(context.Background(), a, DiagnosticPolicy{AllowExecution: true, MaxCommands: 8, MaxRepetitions: 3}, request, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(a.Reports[0].Report)
	if string(before) != string(after) {
		t.Fatal("diagnostics mutated pinned original evidence")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unreviewed test ran")
	}
	if len(record.Runs) != 1 || len(record.Runs[0].Attempts) != 2 {
		t.Fatalf("fresh attempts collapsed: %+v", record)
	}
	if err := os.WriteFile(failureMarker, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	record, err = RunDiagnostic(t.Context(), a, DiagnosticPolicy{AllowExecution: true, MaxCommands: 8, MaxRepetitions: 3}, request, t.TempDir())
	if err == nil || record.Status != "partial" || !record.SourceVerified || len(record.Runs) == 0 || len(record.Artifacts) == 0 {
		t.Fatalf("test failure must retain verified source provenance and its actual artifacts: record=%+v error=%v", record, err)
	}
}
