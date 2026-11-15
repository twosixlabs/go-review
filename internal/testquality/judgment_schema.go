package testquality

import "encoding/json"

// The model proposes advice; the host binds metadata, ratings and provenance.
func modelJudgmentSchema(collective bool) json.RawMessage {
	text := func() map[string]any { return map[string]any{"type": "string"} }
	nonemptyText := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	array := func(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	citation := object(map[string]any{"snapshotID": text(), "fileID": text(), "startLine": map[string]any{"type": "integer", "minimum": 1}, "endLine": map[string]any{"type": "integer", "minimum": 1}}, "snapshotID", "fileID", "startLine", "endLine")
	identity := object(map[string]any{"snapshotID": text(), "module": text(), "symbolID": text()}, "snapshotID", "module", "symbolID")
	dimensions := []Dimension{Craftsmanship, AssertionStrength}
	if collective {
		dimensions = []Dimension{BehaviorAdequacy, ConditionAdequacy}
	}
	labels := []string{"absent", "weak", "material-issues", "strong", "convincing", "unknown"}
	if collective {
		labels = append(labels, "not-applicable")
	}
	rating := object(map[string]any{
		"dimension": map[string]any{"type": "string", "enum": dimensions},
		"value":     map[string]any{"type": "string", "enum": labels},
		"rationale": nonemptyText(), "evidence": array(citation),
	}, "dimension", "value", "rationale", "evidence")
	finding := object(map[string]any{
		"category": nonemptyText(), "kind": map[string]any{"type": "string", "enum": []string{"observation", "inference", "suggestion"}},
		"title": nonemptyText(), "rationale": nonemptyText(), "consequence": text(), "suggestion": text(), "evidence": array(citation),
	}, "category", "kind", "title", "rationale", "evidence")
	properties := map[string]any{
		"ratings":  map[string]any{"type": "array", "items": rating, "minItems": 2, "maxItems": 2},
		"findings": array(finding),
	}
	var schema map[string]any
	if collective {
		conditionProperties := map[string]any{
			"id": text(), "description": text(), "evidence": array(citation), "tests": array(identity),
			"state": map[string]any{"type": "string", "enum": []string{"supported", "gap", "unresolved"}}, "rationale": text(),
		}
		condition := object(conditionProperties, "id", "description", "evidence", "state", "rationale")
		expectationProperties := map[string]any{}
		for key, value := range conditionProperties {
			expectationProperties[key] = value
		}
		expectationProperties["basis"] = map[string]any{"type": "string", "enum": []string{"documented", "implementation-derived"}}
		expectationProperties["conditions"] = array(condition)
		properties["expectations"] = array(object(expectationProperties, "id", "description", "basis", "evidence", "conditions", "state", "rationale"))
		schema = object(properties, "ratings", "expectations", "findings")
	} else {
		properties["identity"] = identity
		item := object(properties, "identity", "ratings", "findings")
		schema = object(map[string]any{"items": array(item)}, "items")
	}
	raw, _ := json.Marshal(schema)
	return raw
}
