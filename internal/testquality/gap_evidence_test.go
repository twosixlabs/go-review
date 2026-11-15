package testquality

import "testing"

func TestQualitySuiteGapConditionsReuseInspectedBehaviorBasis(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {}\n")
	suite := SuiteAssessment{Status: StatusInsufficient, Reason: "Partial", Expectations: []ExpectedBehavior{{ID: "output", Description: "Output check", Basis: "implementation-derived", State: "gap", Rationale: "No assertion", Evidence: []Citation{a.Items[0].Declaration}, Conditions: []ExpectedCondition{{ID: "normal", Description: "Normal input", State: "gap", Rationale: "No assertion"}}}}}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err != nil {
		t.Fatal(err)
	}
	a.Unavailable = []string{"module unavailable"}
	if err := validateSuite(a, suite, []Citation{a.Items[0].Declaration}); err != nil {
		t.Fatalf("partial scope may still provide advisory gap observations: %v", err)
	}
	if len(a.Unavailable) != 1 || suite.Status != StatusInsufficient {
		t.Fatal("accepting advisory observations erased partial-scope provenance")
	}
}
