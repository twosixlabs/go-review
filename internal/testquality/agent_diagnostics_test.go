package testquality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestQualityDiagnosticAgentRejectsWithoutExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Assessment, *DiagnosticRequest)
		want   string
	}{
		{"disabled", func(a *Assessment, _ *DiagnosticRequest) { a.Policy.AllowExecution = false }, "disabled"},
		{"invalid-policy", func(a *Assessment, _ *DiagnosticRequest) { a.Policy.MaxCommands = 0 }, "bounds"},
		{"repetitions", func(a *Assessment, r *DiagnosticRequest) {
			a.Policy.MaxRepetitions = 1
			r.Repetitions = 2
		}, "repetition"},
		{"unknown-target", func(_ *Assessment, r *DiagnosticRequest) { r.Tests[0].SymbolID = "unknown" }, "target"},
		{"invalid-evidence", func(_ *Assessment, r *DiagnosticRequest) { r.Evidence[0].EndLine = 1000 }, "evidence"},
		{"unsupported-kind", func(_ *Assessment, r *DiagnosticRequest) { r.Kind = "shell" }, "kind"},
		{"prior-command", func(a *Assessment, r *DiagnosticRequest) {
			a.Policy.MaxCommands = 1
			prior := *r
			prior.Kind = "race"
			a.Diagnostics = []DiagnosticRecord{{Request: prior, Status: "partial"}}
		}, "command budget"},
		{"prior-duplicate", func(a *Assessment, r *DiagnosticRequest) {
			a.Diagnostics = []DiagnosticRecord{{Request: *r, Status: "failed"}}
		}, "redundant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
			a.Policy = DefaultDiagnosticPolicy(true)
			report := a.Reports[0].Report
			report.SourceSHA256 = "source"
			report.UnitEvidence = &review.UnitEvidence{SourceSHA256: "source", Runs: []review.UnitTestRun{{
				Selection: review.UnitTestSelection{Package: "example"}, MatchedTests: []string{"TestF"},
			}}}
			request := DiagnosticRequest{Kind: "rerun", Tests: []Identity{a.Items[0].Identity}, Repetitions: 1,
				Reason: "Inspect reviewed behavior", Evidence: []Citation{a.Items[0].Declaration}}
			tc.change(a, &request)
			before, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			record, err := RunAgentDiagnostic(t.Context(), a, request, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q rejection, got record=%+v error=%v", tc.want, record, err)
			}
			if record.Status != "failed" || record.Error != err.Error() || !reflect.DeepEqual(record.Request, request) || len(record.Runs) != 0 || len(record.Artifacts) != 0 {
				t.Fatalf("rejection must retain the request and explicit error without execution: %+v", record)
			}
			after, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("rejected diagnostic mutated assessment")
			}
		})
	}
}

func TestQualityDiagnosticAgentMissingAssessment(t *testing.T) {
	record, err := RunAgentDiagnostic(t.Context(), nil, DiagnosticRequest{Kind: "rerun"}, "")
	if err == nil || record.Status != "failed" || record.Error != err.Error() {
		t.Fatalf("missing assessment must return an explicit failure: %+v, %v", record, err)
	}
}

func TestQualityDiagnosticAgentExecutesScopedFixture(t *testing.T) {
	relative, err := os.MkdirTemp(".", ".agent-diagnostic-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(relative); err != nil {
			t.Error(err)
		}
	})
	root, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "module")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"go.mod":    "module example\n\ngo 1.24.0\n",
		"sample.go": "package example\n",
		"sample_test.go": `package example
import ("os"; "testing")
func TestAllowed(t *testing.T) {
 if err := os.WriteFile("allowed-ran", []byte("ran"), 0600); err != nil {t.Fatal(err)}
 if _, err := os.Stat("fail"); err == nil {t.Fatal("fixture failure")}
}
func TestUnreviewed(t *testing.T) {os.WriteFile("unreviewed-ran", []byte("ran"), 0600)}
`,
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := review.Analyze(t.Context(), review.Options{Dir: module})
	if err != nil {
		t.Fatal(err)
	}
	if err := review.CollectUnitEvidence(t.Context(), report, review.UnitEvidenceOptions{
		Selections:  []review.UnitTestSelection{{Package: "example", Tests: []string{"TestAllowed"}}},
		ArtifactDir: filepath.Join(root, "initial"),
	}); err != nil {
		t.Fatal(err)
	}
	store := Store{Directory: filepath.Join(root, "archive")}
	a, err := store.Create(t.Context(), &review.RepositoryReview{Root: module, Modules: []review.ReviewedModule{{Report: report}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Policy = DefaultDiagnosticPolicy(true)
	var selected Responsibility
	for _, item := range a.Items {
		if item.Name == "TestAllowed" {
			selected = item
		}
	}
	if selected.Name == "" {
		t.Fatal("fixture has no reviewed test")
	}
	request := DiagnosticRequest{Kind: "repeat", Tests: []Identity{selected.Identity}, Repetitions: 2,
		Reason: "Inspect repeated reviewed behavior", Evidence: []Citation{selected.Declaration}}
	// A spare backing-array entry detects appending to caller-owned diagnostics.
	prior := request
	prior.Kind, prior.Repetitions = "rerun", 1
	a.Diagnostics = make([]DiagnosticRecord, 1, 2)
	a.Diagnostics[0] = DiagnosticRecord{Request: prior, Status: "pass"}
	backing := a.Diagnostics[:2]
	backing[1] = DiagnosticRecord{Status: "caller-owned sentinel"}
	for _, failure := range []bool{false, true} {
		if err := os.Remove(filepath.Join(module, "allowed-ran")); err != nil {
			t.Fatal(err)
		}
		if failure {
			if err := os.WriteFile(filepath.Join(module, "fail"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		before, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		record, err := RunAgentDiagnostic(context.Background(), a, request, filepath.Join(root, "diagnostic"))
		if failure {
			if err == nil || record.Status != "partial" || record.Error == "" {
				t.Fatalf("execution failure was swallowed: %+v, %v", record, err)
			}
		} else if err != nil || record.Status != "pass" {
			t.Fatalf("scoped execution failed: %+v, %v", record, err)
		}
		if !record.SourceVerified || len(record.Runs) != 1 || len(record.Runs[0].Attempts) != 2 || len(record.Artifacts) == 0 || !reflect.DeepEqual(record.Request, request) {
			t.Fatalf("fresh evidence not returned: %+v", record)
		}
		if _, err := os.Stat(filepath.Join(module, "allowed-ran")); err != nil {
			t.Fatalf("requested fixture did not execute: %v", err)
		}
		if _, err := os.Stat(filepath.Join(module, "unreviewed-ran")); !os.IsNotExist(err) {
			t.Fatalf("unreviewed fixture executed: %v", err)
		}
		after, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) || backing[1].Status != "caller-owned sentinel" {
			t.Fatal("diagnostic mutated caller-owned assessment or backing array")
		}
	}
}
