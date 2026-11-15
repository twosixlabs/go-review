package testquality

import "testing"

func TestQualitySuiteDerivesParentContributorsFromValidatedConditions(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	suite := SuiteAssessment{Status: StatusInsufficient, Reason: "Partial", Expectations: []ExpectedBehavior{{
		ID: "output", Description: "Output check", Basis: "implementation-derived", State: "supported", Rationale: "Supported via a condition", Evidence: []Citation{a.Items[0].Declaration},
		Conditions: []ExpectedCondition{{ID: "normal", Description: "Normal input", State: "supported", Rationale: "Assertion present", Tests: []Identity{a.Items[0].Identity}, Evidence: []Citation{a.Items[0].Declaration}}},
	}}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err != nil {
		t.Fatal(err)
	}
	suite.Expectations[0].Conditions[0].Tests[0].SymbolID = "unknown"
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err == nil {
		t.Fatal("derived contributors bypassed identity validation")
	}
}
