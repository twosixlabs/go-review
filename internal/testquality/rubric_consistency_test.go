package testquality

import "testing"

func TestQualitySuiteDoesNotForceCompletePredicateMaps(t *testing.T) {
	a := qualityLiveAssessment(t, `func TestContract(t *testing.T) {Classify(1)}`)
	_, seen, err := batchContext(t.Context(), a, a.Items)
	if err != nil {
		t.Fatal(err)
	}
	alternatives := a.Facts[0].Predicates[0].Alternatives
	if len(alternatives) != 2 {
		t.Fatal("fixture must expose the two independent range alternatives")
	}
	citation := Citation{SnapshotID: a.Reports[0].SnapshotID, FileID: "contract", StartLine: 4, EndLine: 5}
	suite := SuiteAssessment{Status: StatusInsufficient, Reason: "Checking support unresolved", Expectations: []ExpectedBehavior{{
		ID: "range", Description: "Accepts or rejects inputs", Basis: "documented", State: "unresolved", Rationale: "Retained range contract", Evidence: []Citation{citation},
		Conditions: []ExpectedCondition{{ID: "below", Description: "Below the range", State: "unresolved", Rationale: "Support unresolved", Evidence: []Citation{citation}}},
	}}}
	if err := validateSuite(a, suite, seen); err != nil {
		t.Fatalf("an advisory map need not exhaust every source alternative: %v", err)
	}
	suite.Expectations[0].Conditions = append(suite.Expectations[0].Conditions, ExpectedCondition{ID: "above", Description: "Above the range", State: "unresolved", Rationale: "Support unresolved", Evidence: []Citation{citation}})
	if err := validateSuite(a, suite, seen); err != nil {
		t.Fatalf("accounting for a partition must not require inventing a supported/gap judgment: %v", err)
	}
	above := suite.Expectations[0]
	above.ID = "upper-range"
	above.Conditions = above.Conditions[1:]
	suite.Expectations[0].Conditions = suite.Expectations[0].Conditions[:1]
	suite.Expectations = append(suite.Expectations, above)
	if err := validateSuite(a, suite, seen); err != nil {
		t.Fatalf("complementary source alternatives may contribute to different behaviors: %v", err)
	}
}

func TestQualitySuitePreservesAdvisoryRatingsAlongsideGapOpinions(t *testing.T) {
	a := factsAssessment("package example; import \"testing\"\n// Contract: F returns one.\nfunc TestF(t *testing.T) {}\n")
	a.Items[0].Declaration.StartLine, a.Items[0].Declaration.EndLine = 3, 3
	d := &a.Reports[0].Report.TestSources[0].Declarations[0]
	d.Location.Line, d.Location.EndLine = 3, 3
	citation := Citation{SnapshotID: "snapshot", FileID: "tests", StartLine: 2, EndLine: 3}
	strong, weak := 3, 2
	suite := SuiteAssessment{Status: StatusScored, Ratings: []Rating{{Dimension: BehaviorAdequacy, State: RatingAssessed, Value: &strong, Rationale: "Judgment", Evidence: []Citation{citation}}, {Dimension: ConditionAdequacy, State: RatingAssessed, Value: &strong, Rationale: "Judgment", Evidence: []Citation{citation}}}, Expectations: []ExpectedBehavior{{ID: "output", Description: "Returns one", Basis: "documented", State: "unresolved", Rationale: "Partial contract support", Evidence: []Citation{citation}, Conditions: []ExpectedCondition{{ID: "normal", Description: "Normal input", State: "gap", Rationale: "No appropriate assertion", Evidence: []Citation{citation}}}}}}
	if err := validateSuite(a, suite, []Citation{citation}); err != nil {
		t.Fatalf("advisory gap opinions must not trigger forced rescoring: %v", err)
	}
	if *suite.Ratings[1].Value != strong || suite.Expectations[0].Conditions[0].State != "gap" {
		t.Fatal("admission changed the model's opinion")
	}
	suite.Ratings[1].Value = &weak
	if err := validateSuite(a, suite, []Citation{citation}); err != nil {
		t.Fatal(err)
	}
	suite.Ratings[1].State, suite.Ratings[1].Value = RatingNotApplicable, nil
	if err := validateSuite(a, suite, []Citation{citation}); err != nil {
		t.Fatalf("human-review guidance may contain debatable applicability judgments: %v", err)
	}
	suite.Ratings[1].State = RatingUnknown
	suite.Status, suite.Reason = StatusInsufficient, "Condition adequacy is unresolved."
	if err := validateSuite(a, suite, []Citation{citation}); err != nil {
		t.Fatal(err)
	}
}
