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
	ExpectationID     string          `json:"expectationID,omitempty"`
	ConditionID       string          `json:"conditionID,omitempty"`
	DiagnosticIndex   int             `json:"diagnosticIndex,omitempty"`
	RunIndex          int             `json:"runIndex,omitempty"`
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
	end := input.Offset
	encoded := []json.RawMessage{}
	// Reserve envelope/cursor bytes; a page may contain fewer rows than its
	// requested count, but its next offset always advances by returned rows.
	bytesLeft := (32 << 10) - 128
	for end < min(len(items), input.Offset+limit) {
		raw, err := json.Marshal(items[end])
		if err != nil {
			return nil, err
		}
		if len(raw)+1 > bytesLeft {
			if len(encoded) == 0 {
				return nil, fmt.Errorf("single test quality row exceeds 32 KiB; use its detail operation")
			}
			break
		}
		encoded = append(encoded, raw)
		bytesLeft -= len(raw) + 1
		end++
	}
	var next *int
	if end < len(items) {
		next = &end
	}
	return json.Marshal(map[string]any{"items": encoded, "total": len(items), "nextOffset": next})
}

// Query is the common read-only projection used by HTTP, MCP and chat. It
// cannot launch assessment or execution, and IDs resolve only in this archive.
// Collective/diagnostic listings omit nested collections, which have their
// own owner-qualified paged readers to keep large retained records reachable.
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
	if a.Version != EvidenceRubricVersion {
		return nil, fmt.Errorf("unsupported Test Quality version %q; start a new assessment", a.Version)
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 || input.DiagnosticIndex < 0 || input.RunIndex < 0 {
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
	case "suite":
		if a.Suite == nil {
			return json.Marshal(SuiteAssessment{Status: StatusUnreviewed, Reason: "Collective judgment has not been performed."})
		}
		return json.Marshal(map[string]any{"status": a.Suite.Status, "ratings": a.Suite.Ratings, "reason": a.Suite.Reason, "scopeCount": len(a.Suite.Scope), "expectationCount": len(a.Suite.Expectations)})
	case "diagnostics":
		records := []map[string]any{}
		for i, record := range a.Diagnostics {
			records = append(records, map[string]any{"diagnosticIndex": i, "request": record.Request, "status": record.Status, "error": record.Error, "sourceVerified": record.SourceVerified, "runCount": len(record.Runs), "artifactCount": len(record.Artifacts)})
		}
		return page(records, input)
	case "diagnostic-runs", "diagnostic-attempts", "diagnostic-artifacts":
		if input.DiagnosticIndex >= len(a.Diagnostics) {
			return nil, fmt.Errorf("diagnostic index is outside this assessment")
		}
		record := a.Diagnostics[input.DiagnosticIndex]
		if input.Operation == "diagnostic-artifacts" {
			return page(record.Artifacts, input)
		}
		if input.Operation == "diagnostic-attempts" {
			if input.RunIndex >= len(record.Runs) {
				return nil, fmt.Errorf("run index is outside this diagnostic")
			}
			return page(record.Runs[input.RunIndex].Attempts, input)
		}
		runs := []map[string]any{}
		for i, run := range record.Runs {
			commands := []map[string]any{}
			for _, command := range run.Commands {
				commands = append(commands, map[string]any{"executable": command.Executable, "args": command.Args, "exitCode": command.ExitCode})
			}
			runs = append(runs, map[string]any{"runIndex": i, "selection": run.Selection, "status": run.Status, "error": run.Error, "attemptCount": len(run.Attempts), "shuffleSeed": run.ShuffleSeed, "commands": commands})
		}
		return page(runs, input)
	case "expectations":
		rows := []map[string]any{}
		if a.Suite != nil {
			for _, e := range a.Suite.Expectations {
				rows = append(rows, map[string]any{"id": e.ID, "description": e.Description, "basis": e.Basis, "state": e.State, "conditionCount": len(e.Conditions), "testCount": len(e.Tests)})
			}
		}
		return page(rows, input)
	case "expectation", "conditions", "condition":
		if a.Suite != nil {
			for _, e := range a.Suite.Expectations {
				if e.ID != input.ExpectationID {
					continue
				}
				switch input.Operation {
				case "expectation":
					count := len(e.Conditions)
					e.Conditions = nil
					return json.Marshal(struct {
						ExpectedBehavior
						ConditionCount int `json:"conditionCount"`
					}{e, count})
				case "conditions":
					return page(e.Conditions, input)
				case "condition":
					for _, c := range e.Conditions {
						if c.ID == input.ConditionID {
							return json.Marshal(c)
						}
					}
					return nil, fmt.Errorf("condition ID is outside this expectation")
				}
			}
		}
		return nil, fmt.Errorf("expectation ID is outside this assessment")
	case "suite-findings":
		if a.Suite == nil {
			return page([]Finding{}, input)
		}
		return page(a.Suite.Findings, input)
	case "facts":
		facts := a.Facts
		selected := []TestFacts{}
		for _, fact := range facts {
			if input.SnapshotID != "" && fact.Identity.SnapshotID != input.SnapshotID || input.SymbolID != "" && fact.Identity.SymbolID != input.SymbolID {
				continue
			}
			selected = append(selected, fact)
		}
		return page(selected, input)
	case "overview":
		operations := []string{"modules", "unavailable", "findings", "tests", "test", "finding", "source", "evidence"}
		operations = append(operations, "facts", "suite", "expectations", "expectation", "conditions", "condition", "suite-findings", "diagnostics", "diagnostic-runs", "diagnostic-attempts", "diagnostic-artifacts")
		summary, err := AggregateVersion(a.Version, a.Items)
		if err != nil {
			return nil, err
		}
		summary.Partial = summary.Partial || len(a.Unavailable) > 0 || a.Status != "completed"
		categories := map[Dimension]*float64{}
		for _, d := range []Dimension{Craftsmanship, AssertionStrength} {
			sum, n := 0, 0
			for _, item := range a.Items {
				if item.Status != StatusScored && item.Status != StatusInsufficient {
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
		categories[BehaviorAdequacy], categories[ConditionAdequacy] = nil, nil
		if a.Suite != nil {
			for _, rating := range a.Suite.Ratings {
				if rating.State == RatingAssessed && rating.Value != nil {
					score := 25 * float64(*rating.Value)
					categories[rating.Dimension] = &score
				}
			}
		}
		return json.Marshal(map[string]any{"assessmentID": a.ID, "version": a.Version, "root": a.Root, "reviewID": a.ReviewID,
			"status": a.Status, "error": a.Error, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt, "provider": a.Provider, "model": a.Model,
			"summary": summary, "unit": "test", "categories": categories, "moduleCount": len(a.Reports), "unavailableModuleCount": len(a.Unavailable),
			"phase": a.Phase, "phaseMillis": a.PhaseMillis, "policy": a.Policy, "diagnosticReason": a.DiagnosticReason, "analysis": a.Analysis, "review": a.Review, "persistence": a.Persistence,
			"limitations": []string{"AI judgments, not measured facts. Production coverage is supporting context, not test quality.", "Scores equally weight assessed entries. Insufficient and unreviewed entries are unknown, not zero.", "Table cases and subtests belong to their enclosing test.", "Aggregate coverage does not identify which individual test or case executed a production path."},
			"operations":  operations})
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
			summary, err := AggregateVersion(a.Version, v)
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
	case "tests":
		type row struct {
			Identity     Identity `json:"identity"`
			Name         string   `json:"name"`
			Package      string   `json:"package"`
			Status       Status   `json:"status"`
			Summary      Summary  `json:"summary"`
			FindingCount int      `json:"findingCount"`
			Declaration  Citation `json:"declaration"`
			TestKind     string   `json:"testKind,omitempty"`
		}
		out := []row{}
		for _, item := range items {
			s, err := AggregateVersion(a.Version, []Responsibility{item})
			if err != nil {
				return nil, err
			}
			out = append(out, row{item.Identity, item.Name, item.Package, item.Status, s, len(item.Findings), item.Declaration, item.TestKind})
		}
		return page(out, input)
	case "test", "finding":
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
