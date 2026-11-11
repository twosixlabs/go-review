package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlowCoverageUsesStandardProfiles(t *testing.T) {
	const source = `package source
func Choose(ok bool) int {
	if ok {
		return 10
	}
	return 20
}
`
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": source,
		"source_test.go": `package source
import "testing"
func TestPaths(t *testing.T) {
	if Choose(true)!=10 { t.Fatal("wrong value") }
}
func TestUnselected(t *testing.T) { t.Fatal("must not execute") }
`,
	}, nil)
	profile := filepath.Join(t.TempDir(), "coverage.out")
	if _, err := runGo(context.Background(), r, r.Root, true, "test", "-mod=readonly", "-count=1", "-vet=off", "-run=^TestPaths$", "-coverprofile="+profile, "."); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportCoverage(r, strings.NewReader(string(data)), profile); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, alternative := range r.SystemFlow.Alternatives {
		got[alternative.Label] = alternative.Execution
	}
	if got["true"] != "exercised" || got["false"] != "not-observed" {
		t.Fatalf("standard profile not understood: alternatives=%v\n%s", got, data)
	}
}

func TestFlowCoverageRealControlStructures(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": `package source
func Nested(outer, inner bool) int {
	if outer {
		if inner { return 1 }
		return 2
	}
	return 3
}
func Loop(n int) int {
	total:=0
	for i:=0;i<n;i++ { total++ }
	return total
}
func Fallthrough(n int) int {
	switch n {
	case 1:
		n++
		fallthrough
	case 2:
		return 2
	default:
		return 3
	}
}
func Select(ch chan int) int {
	select {
	case n:=<-ch:
		return n
	default:
		return 0
	}
}
func Empty(ok bool) int {
	if ok {} else { return 2 }
	return 1
}
`,
		"source_test.go": `package source
import "testing"
func TestPaths(t *testing.T) {
	if Nested(true,false)!=2 {t.Fatal("nested")}
	if Loop(1)!=1 {t.Fatal("loop")}
	if Fallthrough(1)!=2 {t.Fatal("fallthrough")}
	if Select(make(chan int))!=0 {t.Fatal("select")}
	if Empty(true)!=1 {t.Fatal("empty")}
}
`,
	}, nil)
	profile := filepath.Join(t.TempDir(), "coverage.out")
	if _, err := runGo(context.Background(), r, r.Root, true, "test", "-mod=readonly", "-count=1", "-vet=off", "-run=^TestPaths$", "-coverprofile="+profile, "."); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportCoverage(r, strings.NewReader(string(data)), profile); err != nil {
		t.Fatal(err)
	}
	type expectation struct{ function, decision, label, execution string }
	for _, want := range []expectation{
		{"Nested", "outer", "true", "exercised"},
		{"Nested", "outer", "false", "not-observed"},
		{"Nested", "inner", "true", "not-observed"},
		{"Nested", "inner", "false", "exercised"},
		{"Loop", "i < n", "true", "exercised"},
		{"Loop", "i < n", "false", "exercised"},
		{"Fallthrough", "1", "match", "exercised"},
		{"Empty", "ok", "true", "exercised"},
		{"Empty", "ok", "false", "not-observed"},
	} {
		id := symbol(t, r, want.function).ID
		found := false
		for _, a := range r.SystemFlow.Alternatives {
			if a.SymbolID == id && a.Decision == want.decision && a.Label == want.label {
				found = true
				if a.Execution != want.execution {
					t.Errorf("%s %s/%s = %s, want %s", want.function, want.decision, want.label, a.Execution, want.execution)
				}
			}
		}
		if !found {
			t.Errorf("missing %s %s/%s", want.function, want.decision, want.label)
		}
	}
	fallID := symbol(t, r, "Fallthrough").ID
	selectID := symbol(t, r, "Select").ID
	defaultSeen := false
	for _, a := range r.SystemFlow.Alternatives {
		if a.SymbolID == fallID && a.Decision == "2" && a.Label == "match" && a.Execution == "exercised" {
			t.Error("entering a case by fallthrough was confused with matching that case")
		}
		if a.SymbolID == selectID && a.Label == "default" {
			defaultSeen = a.Execution == "exercised"
		}
	}
	if !defaultSeen {
		t.Error("standard coverage did not establish the selected default")
	}
	if t.Failed() {
		t.Logf("profile:\n%s", data)
	}
}

func TestFlowCoverageInfersBranchAlternativesFromUniqueStatements(t *testing.T) {
	const source = `package source
type Request struct { OK bool }
func choose(r Request) int {
	if r.OK {
		return 10
	} else {
		return 20
	}
}
`
	for _, tc := range []struct {
		name                  string
		trueCount, falseCount int
		positive, negative    string
	}{
		{"true only", 1, 0, "exercised", "not-observed"},
		{"false only", 0, 1, "not-observed", "exercised"},
		{"both", 1, 1, "exercised", "exercised"},
		{"neither", 0, 0, "not-observed", "not-observed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeOperationSourceFixture(t, map[string]string{"source.go": source}, nil)
			profile := fmt.Sprintf("mode: count\nexample.test/source/source.go:5.3,5.12 1 %d\nexample.test/source/source.go:7.3,7.12 1 %d\n", tc.trueCount, tc.falseCount)
			if err := ImportCoverage(r, strings.NewReader(profile), "fixture"); err != nil {
				t.Fatal(err)
			}
			data := systemFlowJSON(t, r)
			got := map[string]string{}
			for _, a := range data.Alternatives {
				got[a.Label] = a.Execution
			}
			if got["true"] != tc.positive || got["false"] != tc.negative {
				t.Fatalf("branch evidence=%v, want true=%s false=%s", got, tc.positive, tc.negative)
			}
		})
	}
}

func TestFlowCoverageDoesNotTreatSharedSuccessorAsBothBranches(t *testing.T) {
	const source = `package source
func choose(ok bool) int {
	if ok {
		var a int
		_ = a
	} else {
		var b int
		_ = b
	}
	return 30
}
`
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": source}, nil)
	if err := ImportCoverage(r, strings.NewReader("mode: set\nexample.test/source/source.go:10.2,10.11 1 1\n"), "fixture"); err != nil {
		t.Fatal(err)
	}
	for _, a := range systemFlowJSON(t, r).Alternatives {
		if a.Execution == "exercised" {
			t.Fatalf("shared successor fabricated branch execution: %+v", a)
		}
	}
}

func TestFlowCoverageAbsentEvidenceIsUnknown(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
func choose(ok bool) int { if ok { return 1 }; return 2 }
`}, nil)
	alternatives := systemFlowJSON(t, r).Alternatives
	if len(alternatives) != 2 {
		t.Fatalf("expected two alternatives, got %+v", alternatives)
	}
	for _, a := range alternatives {
		if a.Execution != "unknown" {
			t.Fatalf("missing coverage claimed execution status: %+v", a)
		}
	}
}

func systemFlowJSON(t *testing.T, r *Report) struct {
	Alternatives []struct{ Label, Execution, Reason string } `json:"alternatives"`
} {
	t.Helper()
	var result struct {
		Alternatives []struct{ Label, Execution, Reason string } `json:"alternatives"`
	}
	// Reuse JSON assertions so the failing test diagnoses missing published
	// behavior rather than requiring types that have not been implemented yet.
	data, err := json.Marshal(r.SystemFlow)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFlowCoverageCollapsesHousekeepingButPreservesMeaningfulAlternatives(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
import "fmt"
func Housekeeping(ok bool) int {
	if ok { n:=1; _=n } else { n:=2; _=n }
	return 0
}
func Exchange(ok bool) {
	if ok { fmt.Println("event") }
}
`}, nil)
	housekeeping := symbol(t, r, "Housekeeping").ID
	exchange := symbol(t, r, "Exchange").ID
	exchangeArms := 0
	for _, a := range r.SystemFlow.Alternatives {
		if a.SymbolID == housekeeping {
			t.Error("local housekeeping is presented as an architectural alternative")
		}
		if a.SymbolID == exchange {
			exchangeArms++
		}
	}
	if exchangeArms != 2 {
		t.Errorf("an exchange-changing decision lost its alternatives: %d", exchangeArms)
	}
}
