package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type QueryInput struct {
	AssessmentID      string          `json:"assessmentID"`
	Operation         string          `json:"operation"`
	SnapshotID        string          `json:"snapshotID,omitempty"`
	Module            string          `json:"module,omitempty"`
	Package           string          `json:"package,omitempty"`
	SymbolID          string          `json:"symbolID,omitempty"`
	Category          string          `json:"category,omitempty"`
	Status            string          `json:"status,omitempty"`
	Query             string          `json:"query,omitempty"`
	FindingIndex      int             `json:"findingIndex,omitempty"`
	FileID            string          `json:"fileID,omitempty"`
	StartLine         int             `json:"startLine,omitempty"`
	EndLine           int             `json:"endLine,omitempty"`
	Offset            int             `json:"offset,omitempty"`
	Limit             int             `json:"limit,omitempty"`
	EvidenceOperation string          `json:"evidenceOperation,omitempty"`
	Args              json.RawMessage `json:"args,omitempty"`
}

func page[T any](items []T, input QueryInput) (json.RawMessage, error) {
	if input.Offset < 0 || input.Offset > len(items) || input.Limit < 0 || input.Limit > 100 {
		return nil, fmt.Errorf("invalid page; limit 1-100")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	end := min(len(items), input.Offset+limit)
	var next *int
	if end < len(items) {
		next = &end
	}
	return json.Marshal(map[string]any{"items": items[input.Offset:end], "total": len(items), "nextOffset": next})
}

// Query is the common read-only projection used by HTTP, MCP and chat. It
// cannot launch assessment or execution, and IDs resolve only in this archive.
func Query(ctx context.Context, a *Assessment, input QueryInput) (json.RawMessage, error) {
	data, err := query(ctx, a, input)
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<10 {
		return nil, fmt.Errorf("test quality result exceeds 32 KiB; request a smaller page or source range")
	}
	return data, nil
}

func query(ctx context.Context, a *Assessment, input QueryInput) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.AssessmentID != a.ID {
		return nil, fmt.Errorf("assessment identity mismatch")
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
		return nil, fmt.Errorf("invalid pagination")
	}
	items := []Responsibility{}
	for _, item := range a.Items {
		if input.SnapshotID != "" && item.Identity.SnapshotID != input.SnapshotID || input.Module != "" && item.Identity.Module != input.Module || input.Package != "" && item.Package != input.Package || input.SymbolID != "" && item.Identity.SymbolID != input.SymbolID || input.Status != "" && string(item.Status) != input.Status {
			continue
		}
		if input.Query != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Package), strings.ToLower(input.Query)) {
			continue
		}
		if input.Category != "" {
			matched := false
			for _, f := range item.Findings {
				if f.Category == input.Category {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		items = append(items, item)
	}
	switch input.Operation {
	case "overview":
		summary, err := Aggregate(a.Items)
		if err != nil {
			return nil, err
		}
		summary.Partial = summary.Partial || len(a.Unavailable) > 0 || a.Status != "completed"
		categories := map[Dimension]*float64{}
		for _, d := range []Dimension{AssertionStrength, RepresentativeCases, BehaviorPaths} {
			sum, n := 0, 0
			for _, item := range a.Items {
				if item.Status != StatusScored {
					continue
				}
				for _, r := range item.Ratings {
					if r.Dimension == d && r.State == RatingAssessed && r.Value != nil {
						sum += *r.Value
						n++
					}
				}
			}
			var value *float64
			if n > 0 {
				v := 25 * float64(sum) / float64(n)
				value = &v
			}
			categories[d] = value
		}
		return json.Marshal(map[string]any{"assessmentID": a.ID, "version": a.Version, "root": a.Root, "reviewID": a.ReviewID,
			"status": a.Status, "error": a.Error, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt, "provider": a.Provider, "model": a.Model,
			"summary": summary, "categories": categories, "moduleCount": len(a.Reports), "unavailableModuleCount": len(a.Unavailable),
			"limitations": []string{"AI judgments, not measured facts. Coverage is statement coverage scoped to its retained run.", "Unknown and unreviewed functions do not contribute zeros. Scores weight scored functions equally; applicable dimensions equally within each function.", "Closures contribute to their enclosing declaration. Failed/unavailable modules are outside the scored population."},
			"operations":  []string{"modules", "unavailable", "findings", "responsibilities", "responsibility", "finding", "source", "evidence"}})
	case "findings":
		type row struct {
			Identity     Identity `json:"identity"`
			Name         string   `json:"name"`
			Category     string   `json:"category"`
			Kind         string   `json:"kind"`
			Title        string   `json:"title"`
			FindingIndex int      `json:"findingIndex"`
		}
		out := []row{}
		for _, item := range items {
			for i, f := range item.Findings {
				if input.Category != "" && f.Category != input.Category {
					continue
				}
				out = append(out, row{item.Identity, item.Name, f.Category, f.Kind, f.Title, i})
			}
		}
		return page(out, input)
	case "modules":
		type group struct {
			SnapshotID string  `json:"snapshotID"`
			Module     string  `json:"module"`
			Package    string  `json:"package"`
			Summary    Summary `json:"summary"`
			Findings   int     `json:"findings"`
		}
		groups := map[string][]Responsibility{}
		for _, item := range items {
			key := item.Identity.SnapshotID + "\n" + item.Package
			groups[key] = append(groups[key], item)
		}
		keys := []string{}
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []group{}
		for _, k := range keys {
			v := groups[k]
			summary, err := Aggregate(v)
			if err != nil {
				return nil, err
			}
			n := 0
			for _, item := range v {
				n += len(item.Findings)
			}
			out = append(out, group{v[0].Identity.SnapshotID, v[0].Identity.Module, v[0].Package, summary, n})
		}
		return page(out, input)
	case "unavailable":
		return page(a.Unavailable, input)
	case "responsibilities":
		type row struct {
			Identity     Identity `json:"identity"`
			Name         string   `json:"name"`
			Package      string   `json:"package"`
			Status       Status   `json:"status"`
			Summary      Summary  `json:"summary"`
			FindingCount int      `json:"findingCount"`
		}
		out := []row{}
		for _, item := range items {
			s, err := Aggregate([]Responsibility{item})
			if err != nil {
				return nil, err
			}
			out = append(out, row{item.Identity, item.Name, item.Package, item.Status, s, len(item.Findings)})
		}
		return page(out, input)
	case "responsibility", "finding":
		if input.SnapshotID == "" || input.SymbolID == "" || len(items) != 1 {
			return nil, fmt.Errorf("select one qualified snapshotID and symbolID")
		}
		if input.Operation == "finding" {
			if input.FindingIndex < 0 || input.FindingIndex >= len(items[0].Findings) {
				return nil, fmt.Errorf("unknown finding index")
			}
			return json.Marshal(items[0].Findings[input.FindingIndex])
		}
		return json.Marshal(items[0])
	case "source":
		args, _ := json.Marshal(input)
		return Evidence(ctx, a, EvidenceInput{SnapshotID: input.SnapshotID, Operation: "source", Args: args})
	case "evidence":
		return Evidence(ctx, a, EvidenceInput{SnapshotID: input.SnapshotID, Operation: input.EvidenceOperation, Args: input.Args})
	default:
		return nil, fmt.Errorf("unknown test quality operation")
	}
}
