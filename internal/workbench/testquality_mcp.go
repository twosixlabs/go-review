package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func (s *Server) qualityEvidence(ctx context.Context, a *testquality.Assessment, retrieve testquality.RetrievalFunc) (testquality.JudgmentOptions, func(), error) {
	options := testquality.JudgmentOptions{Retrieve: retrieve}
	service := &reviewMCP{work: s, pinned: map[string]*querySnapshot{}}
	hasEvidence, hasDataflow, hasPackages := false, false, false
	for _, report := range a.Reports {
		service.pinned[report.SnapshotID] = &querySnapshot{report: report.Report, info: snapshotInfo{ID: report.SnapshotID, Module: report.Report.Module, SourceSHA256: report.Report.SourceSHA256}}
		hasEvidence = hasEvidence || len(report.Report.Symbols) > 0 || report.Report.UnitEvidence != nil || report.Report.Assessment != nil || len(report.Report.Commands) > 0
		hasDataflow = hasDataflow || report.Report.DataFlows != nil
		hasPackages = hasPackages || len(report.Report.Packages) > 0
	}
	st, ct := mcp.NewInMemoryTransports()
	server := service.server()
	registerQualityFileTools(server, a)
	type pinnedOverviewInput struct {
		snapshotInput
		overviewInput
	}
	registerReviewTool(server, service, "review_overview", "Read project purpose, module metrics and entry inventory from an assessment-pinned snapshot. Use snapshotID from the assigned scope.", "overview",
		func(input pinnedOverviewInput) (string, string) { return input.SnapshotID, "" })
	type testsInput struct {
		SnapshotID string `json:"snapshotID" jsonschema:"Assessment snapshot ID"`
		Package    string `json:"package,omitempty" jsonschema:"Exact Go import path; omit for all selected tests"`
		Query      string `json:"query,omitempty" jsonschema:"Optional test name or package text"`
		Offset     int    `json:"offset,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "review_tests", Description: "Discover SELECTED TESTS by exact package or name, including qualified identities, module-relative file paths and exact source ranges. Copy returned paths/IDs; do not guess filenames. Available to lead and children. This is the assessment selection, not production symbols or current grading progress. Listing does not grant recording authority; any agent may record any selected test after inspecting its complete source.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input testsInput) (*mcp.CallToolResult, map[string]any, error) {
			if service.pinned[input.SnapshotID] == nil {
				return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
			}
			raw, err := testquality.Query(ctx, a, testquality.QueryInput{AssessmentID: a.ID, Operation: "selected-tests", SnapshotID: input.SnapshotID, Package: input.Package, Query: input.Query, Offset: input.Offset, Limit: input.Limit})
			if err != nil {
				return nil, nil, err
			}
			var output map[string]any
			if err := json.Unmarshal(raw, &output); err != nil {
				return nil, nil, err
			}
			return nil, output, nil
		})
	type factsInput struct {
		SnapshotID string `json:"snapshotID" jsonschema:"Assessment-pinned snapshot ID"`
		SymbolID   string `json:"symbolID,omitempty" jsonschema:"Selected TEST declaration ID; omit to page all selected tests"`
		Offset     int    `json:"offset,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "quality_facts", Description: "Compute and read deterministic checking sites, helper limitations and input/predicate facts for selected TEST IDs. No execution; production function metrics use review_evidence.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input factsInput) (*mcp.CallToolResult, map[string]any, error) {
			if service.pinned[input.SnapshotID] == nil {
				return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
			}
			if _, err := testquality.AnalyzeFacts(a); err != nil {
				return nil, nil, err
			}
			data, err := testquality.Query(ctx, a, testquality.QueryInput{AssessmentID: a.ID, Operation: "facts", SnapshotID: input.SnapshotID, SymbolID: input.SymbolID, Offset: input.Offset, Limit: input.Limit})
			if err != nil {
				return nil, nil, err
			}
			var output map[string]any
			if err := json.Unmarshal(data, &output); err != nil {
				return nil, nil, err
			}
			return nil, output, nil
		})
	type diagnosticInput struct {
		SnapshotID      string `json:"snapshotID" jsonschema:"Assessment-pinned snapshot ID"`
		Operation       string `json:"operation" jsonschema:"diagnostics, diagnostic-runs, diagnostic-attempts, diagnostic-commands, or diagnostic-artifacts"`
		DiagnosticIndex int    `json:"diagnosticIndex,omitempty"`
		RunIndex        int    `json:"runIndex,omitempty"`
		Offset          int    `json:"offset,omitempty"`
		Limit           int    `json:"limit,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "quality_diagnostics", Description: "Read retained fresh diagnostic observations. Page runs, attempts, commands (including output), or artifacts by diagnosticIndex/runIndex. Never executes tests.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input diagnosticInput) (*mcp.CallToolResult, map[string]any, error) {
			if service.pinned[input.SnapshotID] == nil {
				return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
			}
			switch input.Operation {
			case "diagnostics", "diagnostic-runs", "diagnostic-attempts", "diagnostic-commands", "diagnostic-artifacts":
			default:
				return nil, nil, fmt.Errorf("unsupported diagnostic operation %q", input.Operation)
			}
			data, err := testquality.Query(ctx, a, testquality.QueryInput{
				AssessmentID: a.ID, Operation: input.Operation, DiagnosticIndex: input.DiagnosticIndex,
				RunIndex: input.RunIndex, Offset: input.Offset, Limit: input.Limit,
			})
			if err != nil {
				return nil, nil, err
			}
			var output map[string]any
			if err := json.Unmarshal(data, &output); err != nil {
				return nil, nil, err
			}
			return nil, output, nil
		})
	if retrieve != nil {
		mcp.AddTool(server, &mcp.Tool{Name: "quality_retrieve", Description: "Find hybrid candidates in this assessment's prepared snapshot. Candidates are not inspected source.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(ctx context.Context, _ *mcp.CallToolRequest, input retrievalInput) (*mcp.CallToolResult, map[string]any, error) {
				pinned := service.pinned[input.SnapshotID]
				if pinned == nil {
					return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
				}
				args, err := json.Marshal(input)
				if err != nil {
					return nil, nil, err
				}
				data, err := retrieve(ctx, pinned.report, input.SnapshotID, args)
				if err != nil {
					return nil, nil, err
				}
				var output map[string]any
				if err := json.Unmarshal(data, &output); err != nil {
					return nil, nil, fmt.Errorf("decode prepared retrieval result: %w", err)
				}
				if output == nil {
					return nil, nil, fmt.Errorf("prepared retrieval requires an object result")
				}
				return nil, output, nil
			})
	}
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		return options, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "go-review-test-quality", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		serverSession.Close()
		return options, nil, err
	}
	closeSession := func() { session.Close(); serverSession.Close() }
	inventory, err := session.ListTools(ctx, nil)
	if err != nil {
		closeSession()
		return options, nil, err
	}
	allowed := map[string]bool{}
	for _, tool := range inventory.Tools {
		switch tool.Name {
		case "review_overview", "review_source", "review_search", "review_document", "review_evidence", "review_packages", "review_flow", "review_dataflow", "quality_retrieve", "quality_facts", "quality_diagnostics", "review_tests", "review_list_files", "review_read_file", "review_search_files":
		default:
			continue
		}
		if tool.Name == "review_evidence" && !hasEvidence || tool.Name == "review_dataflow" && !hasDataflow || tool.Name == "review_packages" && !hasPackages {
			continue
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			closeSession()
			return options, nil, err
		}
		options.Tools = append(options.Tools, intelligence.Tool{Type: "function", Function: intelligence.Function{Name: tool.Name, Description: tool.Description, Parameters: schema}})
		allowed[tool.Name] = true
	}
	options.Read = func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
		if !allowed[name] {
			return nil, nil, fmt.Errorf("tool %q is not authorized for this assessment", name)
		}
		var input map[string]any
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, nil, err
		}
		if service.pinned[fmt.Sprint(input["snapshotID"])] == nil {
			return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
		if err != nil {
			return nil, nil, err
		}
		if result.IsError {
			var detail []string
			for _, content := range result.Content {
				if text, ok := content.(*mcp.TextContent); ok {
					detail = append(detail, text.Text)
				} else {
					encoded, err := json.Marshal(content)
					if err != nil {
						return nil, nil, fmt.Errorf("encode MCP %s diagnostic: %w", name, err)
					}
					detail = append(detail, string(encoded))
				}
			}
			return nil, nil, fmt.Errorf("MCP %s: %s", name, strings.Join(detail, "\n"))
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, nil, err
		}
		var citations []testquality.Citation
		if name == "review_read_file" {
			var source struct {
				Data struct {
					Citation *testquality.Citation `json:"citation"`
				} `json:"data"`
			}
			if err := json.Unmarshal(data, &source); err != nil {
				return nil, nil, err
			}
			if source.Data.Citation != nil {
				citations = append(citations, *source.Data.Citation)
			}
		}
		if name == "review_source" || name == "review_document" {
			var source struct {
				Data struct {
					FileID    string `json:"fileID"`
					ID        string `json:"id"`
					StartLine int    `json:"startLine"`
					EndLine   int    `json:"endLine"`
					Text      string `json:"text"`
					Page      *struct {
						Offset     int  `json:"offset"`
						NextOffset *int `json:"nextOffset"`
					} `json:"textPage"`
				} `json:"data"`
			}
			if err := json.Unmarshal(data, &source); err != nil {
				return nil, nil, err
			}
			id := fmt.Sprint(input["snapshotID"])
			report := service.pinned[id].report
			fileID, original, base := source.Data.FileID, "", source.Data.StartLine
			if name == "review_document" {
				fileID, base = source.Data.ID, 1
				documents := append([]review.Document(nil), report.Documents...)
				if report.EntryPoints != nil {
					documents = append(documents, report.EntryPoints.Sources...)
				}
				for _, document := range documents {
					if document.ID == fileID {
						original = document.Text
					}
				}
			} else {
				files := append(append([]review.File{}, report.Files...), report.SupportingFiles...)
				for _, test := range report.TestSources {
					files = append(files, test.File)
				}
				for _, file := range files {
					if file.ID == fileID {
						lines := strings.Split(file.Source, "\n")
						if base < 1 || source.Data.EndLine < base || source.Data.EndLine > len(lines) {
							return nil, nil, fmt.Errorf("MCP source returned invalid retained line bounds")
						}
						original = strings.Join(lines[base-1:source.Data.EndLine], "\n")
					}
				}
			}
			offset := 0
			if source.Data.Page != nil {
				offset = source.Data.Page.Offset
			}
			chars := []rune(original)
			end := offset + len([]rune(source.Data.Text))
			if base < 1 || offset < 0 || end > len(chars) || string(chars[offset:end]) != source.Data.Text {
				return nil, nil, fmt.Errorf("MCP text does not match pinned source")
			}
			if citation := qualityTextCitation(id, fileID, base, chars, offset, end); citation != nil {
				citations = append(citations, *citation)
			}
		}
		return data, citations, nil
	}
	return options, closeSession, nil
}
