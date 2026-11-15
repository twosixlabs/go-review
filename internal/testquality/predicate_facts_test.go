package testquality

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQualityFactsExplainImpossiblePredicateCombinations(t *testing.T) {
	for _, tc := range []struct {
		expression string
		exclusive  bool
	}{{"n<1||n>2", true}, {"n<=1||n>=1", false}, {"n<1||n>=1", true}, {"1>n||2<n", true}, {"n<3||n>1", false}} {
		t.Run(tc.expression, func(t *testing.T) {
			a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
			file := &a.Reports[0].Report.Files[0]
			file.Source = strings.ReplaceAll(file.Source, "n<1||n>2", tc.expression)
			file.SHA256 = Digest([]byte(file.Source))
			facts, err := AnalyzeFacts(a)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(facts[0].Predicates[0])
			if err != nil {
				t.Fatal(err)
			}
			var predicate struct {
				MutuallyExclusive [][2]string `json:"mutuallyExclusive"`
			}
			if err := json.Unmarshal(raw, &predicate); err != nil {
				t.Fatal(err)
			}
			if (len(predicate.MutuallyExclusive) == 1) != tc.exclusive {
				t.Fatalf("impossible combinations must be explicit rather than demanded as test cases: %s", raw)
			}
			if tc.exclusive && (predicate.MutuallyExclusive[0][0] != facts[0].Predicates[0].Alternatives[0].ID || predicate.MutuallyExclusive[0][1] != facts[0].Predicates[0].Alternatives[1].ID) {
				t.Fatal("combination lost retained predicate identities")
			}
		})
	}
}

func TestQualityFactsPredicateFloatInputsRemainUnknown(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(16777217)}`)
	file := &a.Reports[0].Report.Files[0]
	file.Source = strings.ReplaceAll(strings.ReplaceAll(file.Source, "n int", "n float32"), "n>2", "n>16777216")
	file.SHA256 = Digest([]byte(file.Source))
	facts, err := AnalyzeFacts(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, predicate := range facts[0].Predicates {
		for _, alternative := range predicate.Alternatives {
			for _, input := range alternative.Inputs {
				if input.Truth != "unknown" {
					t.Fatalf("untyped constant arithmetic replaced float32 semantics: %+v", input)
				}
			}
		}
	}
}

func TestQualityFactsExclusivePredicatesStayConservative(t *testing.T) {
	for _, tc := range []struct {
		name, signature, expression, mutation string
	}{
		{"float", "n float32", "n<1||n>2", ""},
		{"named", "n Number", "n<1||n>2", ""},
		{"different parameters", "n,m int", "n<1||m>2", ""},
		{"mutable", "n int", "n<1||n>2", "n++;"},
		{"larger expression", "n int", "n<1||n>2||n==42", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
			file := &a.Reports[0].Report.Files[0]
			file.Source = strings.ReplaceAll(file.Source, "n int", tc.signature)
			file.Source = strings.ReplaceAll(file.Source, "n<1||n>2", tc.expression)
			file.Source = strings.ReplaceAll(file.Source, "if n<1", tc.mutation+"if n<1")
			if tc.name == "named" {
				file.Source += "\ntype Number int\n"
			}
			if tc.name == "different parameters" {
				a.Reports[0].Report.TestSources[0].File.Source = strings.ReplaceAll(a.Reports[0].Report.TestSources[0].File.Source, "Classify(1)", "Classify(1,3)")
				a.Reports[0].Report.TestSources[0].File.SHA256 = Digest([]byte(a.Reports[0].Report.TestSources[0].File.Source))
			}
			file.SHA256 = Digest([]byte(file.Source))
			facts, err := AnalyzeFacts(a)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts[0].Predicates) == 0 {
				t.Fatal("unsupported relations must retain the actual predicates")
			}
			for _, predicate := range facts[0].Predicates {
				if len(predicate.MutuallyExclusive) != 0 {
					t.Fatalf("unsupported comparison fabricated impossibility: %+v", predicate)
				}
			}
		})
	}
}

func TestQualityFactsDistinguishPredicateAlternatives(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {
 for _,n:=range []int{1,2} {got,err:=Classify(n);if got!="accepted"||err!=nil {t.Fatal(got,err)}}
 got,err:=Classify(0);if got!=""||err!=ErrRange {t.Fatal(got,err)}
}`)
	if _, err := AnalyzeFacts(a); err != nil {
		t.Fatal(err)
	}
	rows := queryFacts(t, a)
	var facts struct {
		Predicates []struct {
			Expression   string
			Evidence     Citation
			Alternatives []struct {
				Expression string
				Inputs     []struct {
					Value          string
					Truth          string `json:"potentialTruth"`
					Call, Evidence Citation
				}
			}
		}
	}
	if err := json.Unmarshal(rows[0], &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.Predicates) == 0 {
		t.Fatal("missing source-derived predicate alternatives")
	}
	foundBelow, foundAbove := false, false
	for _, predicate := range facts.Predicates {
		if predicate.Evidence.FileID != "contract" || predicate.Evidence.StartLine != 5 {
			t.Fatalf("lost production source identity: %+v", predicate)
		}
		for _, alternative := range predicate.Alternatives {
			for _, input := range alternative.Inputs {
				want := "false"
				if alternative.Expression == "n < 1" && input.Value == "0" {
					want = "true"
					foundBelow = true
				}
				if alternative.Expression == "n > 2" {
					foundAbove = true
				}
				if input.Truth != want {
					t.Fatalf("%s with %s: %s, want %s", alternative.Expression, input.Value, input.Truth, want)
				}
				if input.Call.FileID != "tests" || input.Evidence.FileID != "tests" {
					t.Fatalf("input lost retained origin: %+v", input)
				}
			}
		}
	}
	if !foundBelow || !foundAbove {
		t.Fatal("separate OR alternatives were merged or omitted")
	}
}

func TestQualityFactsPredicateInputsStayUnknownWhenMutableOrDynamic(t *testing.T) {
	for _, body := range []string{
		`n:=0;n=3;Classify(n)`,
		`for _,n:=range inputs() {Classify(n)}`,
		`var n int;Classify(n)`,
		`values:=[]int{0};values[0]=3;for _,n:=range values {Classify(n)}`,
		`values:=[]int{0};change(values);for _,n:=range values {Classify(n)}`,
		`values:=[]int{0};var alias=values;alias[0]=3;for _,n:=range values {Classify(n)}`,
		`values:=[]int{0};p:=&values[0];*p=3;for _,n:=range values {Classify(n)}`,
		`values:=[]int{0};nested:=[][]int{values};nested[0][0]=3;for _,n:=range values {Classify(n)}`,
		`values:=[]int{0};alias:=func()[]int{return values}();alias[0]=3;for _,n:=range values {Classify(n)}`,
	} {
		t.Run(body, func(t *testing.T) {
			a := qualityLiveAssessment(t, "func TestContract(t *testing.T) {"+body+"}")
			if _, err := AnalyzeFacts(a); err != nil {
				t.Fatal(err)
			}
			rows := queryFacts(t, a)
			var facts struct {
				Predicates []struct {
					Alternatives []struct {
						Inputs []struct {
							Truth string `json:"potentialTruth"`
						}
					}
				}
			}
			if err := json.Unmarshal(rows[0], &facts); err != nil {
				t.Fatal(err)
			}
			if len(facts.Predicates) == 0 {
				t.Fatal("unsupported mapping silently omitted")
			}
			for _, predicate := range facts.Predicates {
				for _, alternative := range predicate.Alternatives {
					for _, input := range alternative.Inputs {
						if input.Truth != "unknown" {
							t.Fatalf("unresolved input became %s", input.Truth)
						}
					}
				}
			}
		})
	}
}
