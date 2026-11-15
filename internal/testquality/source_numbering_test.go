package testquality

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQualityJudgmentSourceHasExactLineNumbers(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\n// Contract: F returns one.\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	a.Items[0].Declaration.StartLine, a.Items[0].Declaration.EndLine = 3, 3
	d := &a.Reports[0].Report.TestSources[0].Declarations[0]
	d.Location.Line, d.Location.EndLine = 3, 3
	raw, actual, err := a.sourcePage(t.Context(), Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: 2, EndLine: 3})
	if err != nil {
		t.Fatal(err)
	}

	var source struct {
		Text         string
		LineNumbered bool
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if !source.LineNumbered || !strings.Contains(source.Text, "2: // Contract: F returns one.") || !strings.Contains(source.Text, "3: func TestF") {
		t.Fatalf("model must not guess citation line offsets: %s", raw)
	}
	if actual.StartLine != 2 || actual.EndLine != 3 {
		t.Fatal("line-numbering changed admitted original source range")
	}
}

func TestQualityJudgmentSourceAllowsLongIndividualLine(t *testing.T) {
	long := strings.Repeat("x", 40000)
	a := factsAssessment("package example\n// " + long + "\n")
	raw, actual, err := a.sourcePage(t.Context(), Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: 2, EndLine: 2})
	if err != nil || actual.EndLine != 2 || !strings.Contains(string(raw), long) {
		t.Fatalf("long retained source line unavailable: %v", err)
	}
}

func TestQualityJudgmentSourceKeepsFullRequestedRange(t *testing.T) {
	source := "package example\n" + strings.Repeat("// retained line\n", 600)
	a := factsAssessment(source)
	raw, actual, err := a.sourcePage(t.Context(), Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: 1, EndLine: 601})
	if err != nil || actual.EndLine != 601 || !strings.Contains(string(raw), "601: // retained line") {
		t.Fatalf("requested source range was shortened: end=%d error=%v", actual.EndLine, err)
	}
}
