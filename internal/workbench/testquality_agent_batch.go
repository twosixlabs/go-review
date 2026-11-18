package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

type qualityEvidenceCall struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	AllPages  bool            `json:"allPages"`
	Fields    []string        `json:"fields"`
	GroupBy   string          `json:"groupBy"`
	Match     *struct {
		Field    string `json:"field"`
		Contains string `json:"contains"`
	} `json:"match"`
}

func qualityBatchAllowed(name string) bool {
	switch name {
	case "review_overview", "review_source", "review_search", "review_document", "review_evidence",
		"review_packages", "review_flow", "review_dataflow", "review_tests", "review_list_files",
		"review_read_file", "review_search_files", "review_progress", "quality_retrieve",
		"quality_facts", "quality_diagnostics":
		return true
	}
	return false
}

func qualityBatchTool(tools []intelligence.Tool) (intelligence.Tool, error) {
	var names []string
	for _, tool := range tools {
		if qualityBatchAllowed(tool.Function.Name) {
			names = append(names, tool.Function.Name)
		}
	}
	schema, err := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"calls"},
		"properties": map[string]any{"calls": map[string]any{
			"type": "array", "minItems": 1,
			"items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"tool", "arguments"},
				"properties": map[string]any{
					"tool":      map[string]any{"type": "string", "enum": names},
					"arguments": map[string]string{"type": "object"},
					"allPages":  map[string]string{"type": "boolean"},
					"fields":    map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
					"groupBy":   map[string]string{"type": "string"},
					"match": map[string]any{
						"type": "object", "additionalProperties": false, "required": []string{"field", "contains"},
						"properties": map[string]any{"field": map[string]string{"type": "string"}, "contains": map[string]string{"type": "string"}},
					},
				},
			},
		}},
	})
	return intelligence.Tool{Type: "function", Function: intelligence.Function{
		Name: "review_batch", Parameters: schema,
		Description: "Compose read-only evidence calls in one turn. Each result preserves its tool's response. For inventories, allPages follows nextOffset internally; match filters a string field (case-insensitive contains), fields selects item fields, and groupBy returns string-field counts instead of items. Field paths may use dots, e.g. identity.module. Filtering/grouping happens before results enter your context. Source reads must remain unfiltered, unprojected and single-page to preserve citation authority. Results are byte-bounded; narrow requests if the batch exceeds the allowance. Cannot execute diagnostics, delegate or record judgments.",
	}}, err
}

func qualityEvidenceBatch(ctx context.Context, args json.RawMessage, options testquality.JudgmentOptions, allowed map[string]bool, budget int) (json.RawMessage, []testquality.Citation, error) {
	var input struct {
		Calls []qualityEvidenceCall `json:"calls"`
	}
	if !json.Valid(args) {
		return nil, nil, fmt.Errorf("evidence batch must be valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, nil, fmt.Errorf("decode evidence batch: %w", err)
	}
	if len(input.Calls) == 0 || options.Read == nil {
		return nil, nil, fmt.Errorf("review_batch requires read-only calls and an evidence reader")
	}
	for _, call := range input.Calls {
		if !qualityBatchAllowed(call.Tool) || !allowed[call.Tool] {
			return nil, nil, fmt.Errorf("batch tool %q is not an available read-only evidence capability", call.Tool)
		}
		switch call.Tool {
		case "review_source", "review_document", "review_read_file":
			if call.AllPages || len(call.Fields) > 0 || call.Match != nil || call.GroupBy != "" {
				return nil, nil, fmt.Errorf("source tool %s must return its original source and citation without transformations", call.Tool)
			}
		}
		if call.Match != nil && call.Match.Field == "" {
			return nil, nil, fmt.Errorf("batch match requires a field")
		}
	}
	var results []map[string]any
	var seen []testquality.Citation
	for i, call := range input.Calls {
		data, citations, err := qualityEvidenceBatchCall(ctx, call, options.Read, budget)
		if err != nil {
			return nil, nil, fmt.Errorf("batch call %d (%s): %w", i+1, call.Tool, err)
		}
		results = append(results, map[string]any{"tool": call.Tool, "result": data})
		raw, err := json.Marshal(map[string]any{"results": results})
		if err != nil {
			return nil, nil, err
		}
		if len(raw) > budget {
			return nil, nil, fmt.Errorf("evidence batch exceeds %d bytes; narrow calls, match, fields or groupBy", budget)
		}
		seen = append(seen, citations...)
	}
	raw, err := json.Marshal(map[string]any{"results": results})
	return raw, seen, err
}

func qualityEvidenceBatchCall(ctx context.Context, call qualityEvidenceCall, read func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error), budget int) (json.RawMessage, []testquality.Citation, error) {
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, nil, fmt.Errorf("decode batch arguments: %w", err)
	}
	if arguments == nil {
		return nil, nil, fmt.Errorf("arguments must be a JSON object")
	}
	offset := 0
	if raw := arguments["offset"]; raw != nil {
		if err := json.Unmarshal(raw, &offset); err != nil {
			return nil, nil, fmt.Errorf("offset must be an integer: %w", err)
		}
	}
	items := []any{}
	groups := map[string]int{}
	matched := 0
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		args, err := json.Marshal(arguments)
		if err != nil {
			return nil, nil, err
		}
		raw, citations, err := read(ctx, call.Tool, args)
		if err != nil {
			return nil, nil, err
		}
		if !call.AllPages && len(call.Fields) == 0 && call.Match == nil && call.GroupBy == "" {
			if !json.Valid(raw) {
				return nil, nil, fmt.Errorf("evidence reader returned invalid JSON")
			}
			return raw, citations, nil
		}
		var listing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &listing); err != nil {
			return nil, nil, fmt.Errorf("inventory must return an object: %w", err)
		}
		if listing["items"] == nil && listing["data"] != nil {
			if err := json.Unmarshal(listing["data"], &listing); err != nil {
				return nil, nil, fmt.Errorf("inventory data must be an object: %w", err)
			}
		}
		if listing["items"] == nil {
			return nil, nil, fmt.Errorf("tool does not return a paged items inventory; use an ordinary batch call without transformations")
		}
		var entries []any
		if err := json.Unmarshal(listing["items"], &entries); err != nil {
			return nil, nil, fmt.Errorf("inventory items: %w", err)
		}
		for _, entry := range entries {
			if call.Match != nil {
				value, err := qualityEvidenceField(entry, call.Match.Field)
				if err != nil {
					return nil, nil, err
				}
				text, ok := value.(string)
				if !ok {
					return nil, nil, fmt.Errorf("match field %q is not a string", call.Match.Field)
				}
				if !strings.Contains(strings.ToLower(text), strings.ToLower(call.Match.Contains)) {
					continue
				}
			}
			matched++
			if call.GroupBy != "" {
				value, err := qualityEvidenceField(entry, call.GroupBy)
				if err != nil {
					return nil, nil, err
				}
				key, ok := value.(string)
				if !ok {
					return nil, nil, fmt.Errorf("groupBy field %q is not a string", call.GroupBy)
				}
				groups[key]++
				continue
			}
			if len(call.Fields) > 0 {
				selected := map[string]any{}
				for _, field := range call.Fields {
					value, err := qualityEvidenceField(entry, field)
					if err != nil {
						return nil, nil, err
					}
					selected[field] = value
				}
				entry = selected
			}
			items = append(items, entry)
		}
		var next *int
		if raw := listing["nextOffset"]; raw != nil {
			if err := json.Unmarshal(raw, &next); err != nil {
				return nil, nil, fmt.Errorf("inventory nextOffset: %w", err)
			}
		}
		result := map[string]any{"items": items, "matched": matched, "pagesRead": page, "nextOffset": next}
		if call.GroupBy != "" {
			delete(result, "items")
			result["groups"] = groups
		}
		data, err := json.Marshal(result)
		if err != nil {
			return nil, nil, err
		}
		if len(data) > budget {
			return nil, nil, fmt.Errorf("inventory exceeds %d bytes; narrow match, fields or groupBy", budget)
		}
		if !call.AllPages || next == nil {
			return data, nil, nil
		}
		if *next <= offset {
			return nil, nil, fmt.Errorf("inventory nextOffset %d does not advance beyond %d", *next, offset)
		}
		offset = *next
		arguments["offset"], err = json.Marshal(offset)
		if err != nil {
			return nil, nil, err
		}
	}
}

func qualityEvidenceField(item any, path string) (any, error) {
	value := item
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("inventory field %q is not an object path", path)
		}
		var found bool
		value, found = object[key]
		if !found {
			return nil, fmt.Errorf("inventory field %q does not exist; inspect an unprojected page first", path)
		}
	}
	return value, nil
}
