package testquality

import (
	"go/constant"
	"go/parser"
	"testing"
)

func TestQualityFactsInvalidConstantsAreUnknown(t *testing.T) {
	for _, source := range []string{`-"text"`, `+true`, `1i<2i`} {
		t.Run(source, func(t *testing.T) {
			expr, err := parser.ParseExpr(source)
			if err != nil {
				t.Fatal(err)
			}
			if value := constantExpression(expr); value.Kind() != constant.Unknown {
				t.Fatalf("invalid constant treated as fact: %s", value)
			}
		})
	}
}

func TestQualityFactsExampleOutputAndExactDeclaration(t *testing.T) {
	source := "package example; import \"fmt\"\nfunc ExampleF() {\nfmt.Println(F())\n// Output: 1\n}\n"
	a := factsAssessment(source)
	a.Items[0].Name, a.Items[0].TestKind = "ExampleF", "example"
	d := &a.Reports[0].Report.TestSources[0].Declarations[0]
	d.Name, d.Kind = "ExampleF", "example"
	facts, err := AnalyzeFacts(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[0].Checks) != 1 || facts[0].Checks[0].Kind != "example-output" {
		t.Fatalf("executable example output contract was lost: %+v", facts)
	}
	stale := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	stale.Items[0].Identity.SymbolID = "different-declaration"
	facts, err = AnalyzeFacts(stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[0].Checks) != 0 || len(facts[0].Limitations) == 0 {
		t.Fatal("unrelated retained declaration adopted by name")
	}
}
