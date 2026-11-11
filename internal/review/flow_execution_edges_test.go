package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlowExecutionSkippedAndUnmatchedTestsAreNotPassingEvidence(t *testing.T) {
	for _, tc := range []struct{ name, extra, body string }{
		{"skipped", "", "t.Skip(\"not exercised\")"},
		{"TestMain prevents selection", "func TestMain(m *testing.M) {os.Exit(0)}", "Run()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeOperationSourceFixture(t, map[string]string{
				"source.go":      "package source\nfunc Run(){}\n",
				"source_test.go": "package source\nimport (\"os\";\"testing\")\nvar _=os.Exit\n" + tc.extra + "\nfunc TestWanted(t *testing.T){" + tc.body + "}\n",
			}, nil)
			r.Coverage = &Coverage{Origin: "old-profile", Blocks: []CoverageBlock{}}
			err := CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{ArtifactDir: t.TempDir(), Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestWanted"}}}})
			if err == nil || r.UnitEvidence.Runs[0].Status == "pass" {
				t.Fatalf("missing execution was reported as passing evidence: %+v %v", r.UnitEvidence, err)
			}
			if r.Coverage != nil && r.Coverage.Origin == "old-profile" {
				t.Fatal("old profile was attributed to the failed new selection")
			}
		})
	}
}

func TestFlowExecutionSourceChangesWithdrawJoinedCoverage(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": "package source\nfunc Run()int{return 1}\n",
		"source_test.go": `package source
import ("os";"testing")
func TestEdit(t *testing.T) {
	if Run()!=1 {t.Fatal("bad")}
	if err:=os.WriteFile("source.go",[]byte("package source\nfunc Run()int{return 2}\n"),0600);err!=nil {t.Fatal(err)}
}
`}, nil)
	err := CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{ArtifactDir: t.TempDir(), Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestEdit"}}}})
	if err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("source changes did not invalidate execution join: %v", err)
	}
	if r.Coverage != nil || r.UnitEvidence.Runs[0].Status != "stale" {
		t.Fatal("stale coverage joined the architectural snapshot")
	}
}

func TestFlowExecutionOptionalRuntimeArtifactIsStandardGoTrace(t *testing.T) {
	r, _ := flowExecutionFixture(t)
	err := CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{ArtifactDir: t.TempDir(), RuntimeTrace: true, Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestSelected"}}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.UnitEvidence.Runs[0].RuntimeTracePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 16 || !strings.HasPrefix(string(data[:16]), "go 1.") || !strings.Contains(string(data[:16]), "trace") {
		t.Fatalf("not a standard Go execution trace: %q", data[:min(len(data), 16)])
	}
}

func TestFlowExecutionExcludedAndCanceledSelectionsDoNotExecute(t *testing.T) {
	r, marker := flowExecutionFixture(t)
	r.Scope.ExcludeTests = []string{"^TestSelected$"}
	if err := CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{ArtifactDir: t.TempDir(), Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestSelected"}}}}); err == nil {
		t.Fatal("excluded selection was accepted")
	}
	r.Scope.ExcludeTests = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CollectUnitEvidence(ctx, r, UnitEvidenceOptions{ArtifactDir: t.TempDir(), Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestSelected"}}}}); err == nil {
		t.Fatal("canceled selection was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("excluded/canceled test executed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "coverage.out")); !os.IsNotExist(err) {
		t.Fatal("artifacts escaped into the target")
	}
}
