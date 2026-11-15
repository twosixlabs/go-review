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
	if _, err := testquality.AnalyzeFacts(a); err != nil {
		return options, nil, err
	}
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
	type factsInput struct {
		SnapshotID string `json:"snapshotID" jsonschema:"Assessment-pinned snapshot ID"`
		SymbolID   string `json:"symbolID,omitempty" jsonschema:"Selected TEST declaration ID; omit to page all selected tests"`
		Offset     int    `json:"offset,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "quality_facts", Description: "Read deterministic checking sites, helper limitations and input/predicate facts for selected TEST IDs. Initial facts are already supplied. No execution; production function metrics use review_evidence.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input factsInput) (*mcp.CallToolResult, map[string]any, error) {
			if service.pinned[input.SnapshotID] == nil {
				return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
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
		case "review_source", "review_search", "review_document", "review_evidence", "review_packages", "review_dataflow", "quality_retrieve", "quality_facts":
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
				for _, document := range report.Documents {
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
			startLine := base + strings.Count(string(chars[:offset]), "\n")
			if offset > 0 && chars[offset-1] != '\n' {
				startLine++
			}
			endLine := base + strings.Count(string(chars[:end]), "\n")
			if source.Data.Page != nil && source.Data.Page.NextOffset != nil {
				endLine--
			}
			if startLine <= endLine {
				citations = append(citations, testquality.Citation{SnapshotID: id, FileID: fileID, StartLine: startLine, EndLine: endLine})
			}
		}
		return data, citations, nil
	}
	return options, closeSession, nil
}
