package testquality

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestQualityJudgmentBoundExpectationsRequireSourceBasis(t *testing.T) {
	raw, err := referenceSchema(modelJudgmentSchema(true), map[string]Citation{
		"contract-1": {SnapshotID: "owned", FileID: "contract", StartLine: 4, EndLine: 5},
		"check-1":    {SnapshotID: "owned", FileID: "tests", StartLine: 8, EndLine: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		refs  []string
		valid bool
	}{{"contract-basis", []string{"contract-1"}, true}, {"checks-not-contract", []string{"check-1"}, false}, {"missing-basis", []string{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := map[string]any{
				"ratings": []any{
					map[string]any{"dimension": BehaviorAdequacy, "value": "unknown", "rationale": "Partial evidence.", "evidence": []string{}},
					map[string]any{"dimension": ConditionAdequacy, "value": "unknown", "rationale": "Partial evidence.", "evidence": []string{}},
				},
				"expectations": []any{map[string]any{"id": "accepted", "description": "Returns accepted.", "basis": "documented", "evidence": tc.refs, "tests": []any{}, "state": "unresolved", "rationale": "Expected outcome.", "conditions": []any{}}},
				"findings":     []any{},
			}
			if err := resolved.Validate(proposal); (err == nil) != tc.valid {
				t.Fatalf("model could substitute checks for the source of an expectation: valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestQualityJudgmentModelSchemaBoundsRequiredDimensions(t *testing.T) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(modelJudgmentSchema(true), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	behavior := map[string]any{"dimension": BehaviorAdequacy, "value": "unknown", "rationale": "Contract unavailable.", "evidence": []Citation{}}
	condition := map[string]any{"dimension": ConditionAdequacy, "value": "unknown", "rationale": "Usage contract unavailable.", "evidence": []Citation{}}
	for _, tc := range []struct {
		name    string
		ratings []any
		valid   bool
	}{
		{"omitted-dimensions", []any{}, false},
		{"two-explicit-unknowns", []any{behavior, condition}, true},
		{"repeated-dimensions", []any{behavior, condition, behavior, condition}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := resolved.Validate(map[string]any{"ratings": tc.ratings, "expectations": []any{}, "findings": []any{}})
			if (err == nil) != tc.valid {
				t.Fatalf("model proposal dimension bounds: valid=%v error=%v", tc.valid, err)
			}
		})
	}
	t.Run("advice-without-redundant-fields", func(t *testing.T) {
		proposal := map[string]any{
			"ratings": []any{behavior, condition},
			"expectations": []any{map[string]any{
				"id": "output", "description": "Returns the expected value", "basis": "implementation-derived",
				"evidence": []Citation{}, "state": "gap", "rationale": "A plausible missing check",
				"conditions": []any{map[string]any{"id": "normal", "description": "Normal input", "evidence": []Citation{}, "state": "gap", "rationale": "No contributor identified"}},
			}},
			"findings": []any{map[string]any{"category": "assertions", "kind": "inference", "title": "Consider checking the result", "rationale": "A regression may escape the check", "evidence": []Citation{}}},
		}
		if err := resolved.Validate(proposal); err != nil {
			t.Fatalf("advisory records should not require empty contributor lists or boilerplate: %v", err)
		}
		expectation := proposal["expectations"].([]any)[0].(map[string]any)
		expectation["conditions"].([]any)[0].(map[string]any)["predicateID"] = "fabricated"
		if err := resolved.Validate(proposal); err == nil {
			t.Fatal("new model submissions should use citations, not invented predicate identifiers")
		}
	})
	for _, field := range []string{"category", "title", "rationale"} {
		t.Run("empty-finding-"+field, func(t *testing.T) {
			finding := map[string]any{"category": "test-craftsmanship", "kind": "suggestion", "title": "Improve checks", "rationale": "Missing outcome property.", "consequence": "Regression can pass.", "suggestion": "Check returned state.", "evidence": []Citation{}}
			finding[field] = ""
			if err := resolved.Validate(map[string]any{"ratings": []any{behavior, condition}, "expectations": []any{}, "findings": []any{finding}}); err == nil {
				t.Fatalf("model grammar admitted a finding the host rejects: empty %s", field)
			}
		})
	}
}
