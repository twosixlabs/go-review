package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

func files(r *review.Report) []review.File {
	out := append(append([]review.File{}, r.Files...), r.SupportingFiles...)
	for _, t := range r.TestSources {
		out = append(out, t.File)
	}
	for _, d := range r.Documents {
		out = append(out, review.File{ID: d.ID, Path: d.Path, Source: d.Text, SHA256: d.SHA256})
	}
	return out
}

func pinned(a *Assessment, id string) (*review.Report, error) {
	for _, r := range a.Reports {
		if r.SnapshotID == id {
			return r.Report, nil
		}
	}
	return nil, fmt.Errorf("unknown assessment snapshot %q", id)
}

type EvidenceInput struct {
	SnapshotID string          `json:"snapshotID"`
	Operation  string          `json:"operation"`
	Args       json.RawMessage `json:"args"`
}

// Evidence never reads the live checkout or raw artifact paths. The source
// response is line-bounded, making accepted citations reproducible after reload.
func Evidence(ctx context.Context, a *Assessment, input EvidenceInput) (json.RawMessage, error) {
	r, err := pinned(a, input.SnapshotID)
	if err != nil {
		return nil, err
	}
	var args struct {
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
		FileID    string `json:"fileID"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		SymbolID  string `json:"symbolID"`
		Package   string `json:"package"`
		TestName  string `json:"testName"`
	}
	if len(input.Args) == 0 {
		input.Args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(input.Args, &args); err != nil {
		return nil, err
	}
	if input.Operation == "symbols" {
		type symbol struct {
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Package  string          `json:"package"`
			Location review.Location `json:"location"`
		}
		out := []symbol{}
		for _, s := range r.Symbols {
			if args.Package == "" || s.Package == args.Package {
				out = append(out, symbol{s.ID, s.Name, s.Package, s.Location})
			}
		}
		return page(out, QueryInput{Offset: args.Offset, Limit: args.Limit})
	}
	if input.Operation == "test-runs" {
		if args.TestName == "" {
			return nil, fmt.Errorf("test-runs requires testName")
		}
		if r.UnitEvidence == nil {
			return json.Marshal(map[string]any{"status": "not-collected", "items": []any{}, "nextOffset": nil,
				"limitations": []string{"No retained test execution. Assess source without inventing observed outcomes."}})
		}
		type outcome struct {
			RunIndex          int    `json:"runIndex"`
			Test              string `json:"test"`
			Outcome           string `json:"outcome"`
			RunStatus         string `json:"runStatus"`
			Error             string `json:"error,omitempty"`
			ScopeTests        int    `json:"scopeTests"`
			CoverageCollected bool   `json:"coverageCollected"`
		}
		out := []outcome{}
		for i, run := range r.UnitEvidence.Runs {
			if args.Package != "" && run.Selection.Package != "" && run.Selection.Package != strings.TrimSuffix(args.Package, "_test") {
				continue
			}
			names := []string{}
			for name := range run.Outcomes {
				pkg, test, qualified := strings.Cut(name, "::")
				matches := qualified && (args.Package == "" || pkg == strings.TrimSuffix(args.Package, "_test")) && (test == args.TestName || strings.HasPrefix(test, args.TestName+"/"))
				if matches || !qualified && (name == args.TestName || strings.HasPrefix(name, args.TestName+"/")) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				out = append(out, outcome{i, name, run.Outcomes[name], run.Status, run.Error, len(run.MatchedTests), run.CoveragePath != ""})
			}
		}
		rows, err := page(out, QueryInput{Offset: args.Offset, Limit: args.Limit})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"results": rows, "sourceSHA256": r.UnitEvidence.SourceSHA256,
			"limitations": append([]string{"Run coverage is not individual case-to-path attribution. No reported outcome is unknown, not a failure."}, r.UnitEvidence.Limitations...)})
	}
	if input.Operation == "coverage" {
		if r.Coverage == nil {
			return json.Marshal(map[string]any{"status": "not-collected", "limitations": []string{"No retained statement coverage. Missing is unknown, not zero."}})
		}
		var owner *review.Symbol
		for i := range r.Symbols {
			if r.Symbols[i].ID == args.SymbolID {
				owner = &r.Symbols[i]
				break
			}
		}
		if owner == nil {
			return nil, fmt.Errorf("coverage requires retained symbolID")
		}
		blocks := []review.CoverageBlock{}
		for _, b := range r.Coverage.Blocks {
			if b.FileID == owner.FileID && b.EndLine >= owner.Line && b.StartLine <= owner.EndLine {
				blocks = append(blocks, b)
			}
		}
		blockPage, err := page(blocks, QueryInput{Offset: args.Offset, Limit: args.Limit})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"status": "retained", "snapshotID": input.SnapshotID, "sourceSHA256": r.SourceSHA256,
			"sourceVerified": r.Coverage.SourceVerified, "profileSHA256": r.Coverage.SHA256, "origin": r.Coverage.Origin, "mode": r.Coverage.Mode,
			"blocks": blockPage, "limitations": []string{"Statement blocks overlap the selected declaration; closures belong to that declaration.", "Aggregate coverage does not attribute execution to an individual test or case. Inspect evidence runs, selections and outcomes; sourceVerified=false means freshness is unverified."}})
	}
	if input.Operation == "catalog" {
		if args.Offset < 0 || args.Limit < 0 || args.Limit > 100 {
			return nil, fmt.Errorf("invalid catalog page")
		}
		if args.Limit == 0 {
			args.Limit = 30
		}
		type entry struct {
			ID      string `json:"id"`
			Path    string `json:"path"`
			Package string `json:"package"`
			Lines   int    `json:"lines"`
			Tests   bool   `json:"tests"`
		}
		all := files(r)
		if args.Offset > len(all) {
			return nil, fmt.Errorf("catalog offset out of range")
		}
		end := min(len(all), args.Offset+args.Limit)
		entries := []entry{}
		for _, f := range all[args.Offset:end] {
			entries = append(entries, entry{f.ID, f.Path, f.Package, len(strings.Split(f.Source, "\n")), strings.HasSuffix(f.Path, "_test.go")})
		}
		var next *int
		if end < len(all) {
			next = &end
		}
		return json.Marshal(map[string]any{"items": entries, "total": len(all), "nextOffset": next})
	}
	if input.Operation == "source" {
		for _, f := range files(r) {
			if f.ID == args.FileID {
				lines := strings.Split(f.Source, "\n")
				if args.StartLine == 0 {
					args.StartLine = 1
				}
				if args.EndLine == 0 {
					args.EndLine = min(len(lines), args.StartLine+99)
				}
				if args.StartLine < 1 || args.EndLine < args.StartLine || args.EndLine > len(lines) {
					return nil, fmt.Errorf("invalid retained source range")
				}
				text := strings.Join(lines[args.StartLine-1:args.EndLine], "\n")
				return json.Marshal(map[string]any{"snapshotID": input.SnapshotID, "fileID": f.ID, "path": f.Path, "sha256": f.SHA256, "startLine": args.StartLine, "endLine": args.EndLine, "text": text, "totalLines": len(lines)})
			}
		}
		return nil, fmt.Errorf("unknown retained source file")
	}
	switch input.Operation {
	case "search", "evidence", "dataflow", "logical", "document", "packages":
	default:
		return nil, fmt.Errorf("unsupported assessment evidence operation")
	}
	q, err := webui.NewReviewQuery(r)
	if err != nil {
		return nil, err
	}
	return q.Query(ctx, input.Operation, input.Args)
}

func strictJSON(text string, v any) error {
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

type RetrievalFunc func(context.Context, *review.Report, string, json.RawMessage) (json.RawMessage, error)
