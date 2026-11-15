package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func factsAssessment(source string) *Assessment {
	file := review.File{ID: "tests", Path: "sample_test.go", Package: "example", Source: source, SHA256: Digest([]byte(source))}
	end := 2
	if at := strings.Index(source, "\nfunc verify"); at >= 0 {
		end = strings.Count(source[:at], "\n") + 1
	} else {
		end = strings.Count(strings.TrimSuffix(source, "\n"), "\n") + 1
	}
	id := Identity{SnapshotID: "snapshot", Module: "example", SymbolID: "test"}
	return &Assessment{ID: "facts", Version: EvidenceRubricVersion, Items: []Responsibility{{Identity: id, Name: "TestF", Package: "example", Status: StatusUnreviewed, Declaration: Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: 2, EndLine: end}}},
		Reports: []PinnedReport{{SnapshotID: "snapshot", Report: &review.Report{Module: "example", TestSources: []review.TestSource{{File: file, Declarations: []review.TestDeclaration{{ID: "test", Name: "TestF", Kind: "test", Location: review.Location{FileID: "tests", Line: 2, EndLine: end}}}}}}}}}
}

func queryFacts(t *testing.T, a *Assessment) []json.RawMessage {
	t.Helper()
	if _, err := AnalyzeFacts(a); err != nil {
		t.Fatal(err)
	}
	data, err := Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "facts"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Items []json.RawMessage }
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func TestQualityFactsChecks(t *testing.T) {
	for _, tc := range []struct{ name, body, helper, kind string }{
		{"checked-result", `got:=F(); if got!=1 {t.Fatal(got)}`, "", "conditional-failure"},
		{"log-only", `t.Log(F())`, "", ""},
		{"ineffective", `F(); if 1!=1 {t.Fatal("impossible")}`, "", "constant-ineffective"},
		{"helper", `verify(t,F())`, `func verify(t *testing.T,got int) {if got!=1 {t.Fatal(got)}}`, "conditional-failure"},
		{"dynamic-helper", `fn:=verify; fn(t,F())`, `func verify(t *testing.T,got int) {if got!=1 {t.Fatal(got)}}`, "unresolved"},
		{"shadowed", `{t:=struct{Fatal func(...any)}{Fatal:func(...any){}}; t.Fatal(F())}`, "", "unresolved"},
		{"assertion-constructor", `checker:=assert.New(t); checker.Equal(F(),1)`, "", "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package example\nfunc TestF(t *testing.T) {" + tc.body + "}\n"
			if tc.helper != "" {
				source += tc.helper + "\n"
			}
			// Insert the import without changing the independently specified declaration lines.
			source = strings.Replace(source, "package example", "package example; import \"testing\"", 1)
			if tc.name == "assertion-constructor" {
				source = strings.Replace(source, `import "testing"`, `import ("testing"; "github.com/stretchr/testify/assert")`, 1)
			}
			rows := queryFacts(t, factsAssessment(source))
			if len(rows) != 1 {
				t.Fatalf("facts do not preserve test identity: %s", rows)
			}
			var result struct {
				Checks []struct {
					Kind     string
					Evidence Citation
				}
				Limitations []string
			}
			if err := json.Unmarshal(rows[0], &result); err != nil {
				t.Fatal(err)
			}
			if tc.kind == "" && len(result.Checks) != 0 {
				t.Fatalf("logging became an assertion: %s", rows[0])
			}
			if tc.kind == "unresolved" {
				if !strings.Contains(strings.ToLower(strings.Join(result.Limitations, " ")), "unresolved") {
					t.Fatalf("ambiguous binding lost uncertainty: %s", rows[0])
				}
				if len(result.Checks) != 0 {
					t.Fatalf("ambiguous call became an established assertion: %s", rows[0])
				}
				return
			}

			if tc.kind != "" {
				found := false
				for _, check := range result.Checks {
					if check.Kind == tc.kind {
						found = true
					}
					if check.Evidence.SnapshotID != "snapshot" || check.Evidence.StartLine < 2 {
						t.Fatalf("unqualified evidence: %+v", check)
					}
				}
				if !found {
					t.Fatalf("missing %s checking site: %s", tc.kind, rows[0])
				}
			}
		})
	}
}

func TestQualityFactsMemoizesSharedHelpers(t *testing.T) {
	a := largeFactsAssessment()
	rows := queryFacts(t, a)
	if len(rows) != 20 || a.Analysis.ParsedSources != 1 || a.Analysis.HelpersAnalyzed != 1 {
		t.Fatalf("shared analysis was repeated: rows=%d metrics=%+v", len(rows), a.Analysis)
	}
	queryFacts(t, a)
	if a.Analysis.ParsedSources != 1 || a.Analysis.HelpersAnalyzed != 1 {
		t.Fatal("reading facts reparsed unchanged source")
	}
}

func TestQualityExecutionFactsQualifiedKeys(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	a.Reports[0].Report.UnitEvidence = &review.UnitEvidence{Runs: []review.UnitTestRun{{
		Selection: review.UnitTestSelection{Package: "example", Tests: []string{"TestF"}},
		Status:    "pass", MatchedTests: []string{"TestF"}, Outcomes: map[string]string{"example::TestF": "pass", "example::TestF/valid": "pass", "other::TestF": "fail"},
	}}}
	data, err := Evidence(context.Background(), a, EvidenceInput{SnapshotID: "snapshot", Operation: "test-runs", Args: json.RawMessage(`{"package":"example","testName":"TestF"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Results struct{ Items []struct{ Test string } }
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results.Items) != 2 {
		t.Fatalf("retained qualified outcomes disappeared or leaked: %s", data)
	}
	for _, row := range result.Results.Items {
		if strings.HasPrefix(row.Test, "other::") {
			t.Fatal("cross-package outcome leaked")
		}
	}
}

func TestQualityFactsLiteralCasesAndStaticRisks(t *testing.T) {
	a := factsAssessment(`package example; import ("testing"; "time")
func TestF(t *testing.T) {
 cases:=[]struct{name string; input int}{{"valid",1},{"invalid",0}}
 for _,tc:=range cases {t.Run(tc.name,func(t *testing.T){if F(tc.input)!=1 {t.Error("wrong")}})}
 t.Cleanup(func(){})
 time.Sleep(time.Millisecond)
 for _,x:=range generateCases() {t.Log(x)}
}
`)
	rows := queryFacts(t, a)
	text := string(rows[0])
	for _, want := range []string{"valid", "invalid", "cleanup", "sleep", "dynamic-case-data"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s evidence: %s", want, text)
		}
	}
	if strings.Contains(text, `"provenance":"observed"`) {
		t.Fatal("static risk reported as a runtime observation")
	}
}

func largeFactsAssessment() *Assessment {
	var source strings.Builder
	source.WriteString("package example; import \"testing\"\n")
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "func Test%d(t *testing.T) {verify(t,%d)}\n", i, i)
	}
	source.WriteString("func verify(t *testing.T,n int) {if n<0 {t.Fatal(n)}}\n")
	a := factsAssessment(source.String())
	a.Items = nil
	a.Reports[0].Report.TestSources[0].Declarations = nil
	for n := 0; n < 32; n++ {
		id := Identity{SnapshotID: "snapshot", Module: "example", SymbolID: fmt.Sprint(n)}
		a.Items = append(a.Items, Responsibility{Identity: id, Name: fmt.Sprintf("Test%d", n), Package: "example", Status: StatusUnreviewed, Declaration: Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: n + 2, EndLine: n + 2}})
		a.Reports[0].Report.TestSources[0].Declarations = append(a.Reports[0].Report.TestSources[0].Declarations, review.TestDeclaration{ID: id.SymbolID, Name: fmt.Sprintf("Test%d", n), Kind: "test", Location: review.Location{FileID: "tests", Line: n + 2, EndLine: n + 2}})
	}
	return a
}

func BenchmarkQualityFactsLargeSuite(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Query(context.Background(), largeFactsAssessment(), QueryInput{AssessmentID: "facts", Operation: "facts", Limit: 100}); err != nil {
			b.Fatal(err)
		}
	}
}
