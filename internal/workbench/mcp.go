package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

type snapshotInput struct {
	SnapshotID string `json:"snapshotID" jsonschema:"Immutable snapshot ID returned by review_overview; never a source hash or path"`
}

type pageInput struct {
	Offset  *int   `json:"offset,omitempty" jsonschema:"Zero-based page offset; omitted means zero"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"Maximum items 1-100; default 10. Byte limits may produce fewer items; follow returned nextOffset"`
	Section string `json:"section,omitempty" jsonschema:"One detail collection from the response sections list; omit for compact discovery summary"`
	textInput
}

type textInput struct {
	TextOffset *int `json:"textOffset,omitempty" jsonschema:"Zero-based Unicode character offset for paged text; follow textPage.nextOffset"`
	TextLimit  *int `json:"textLimit,omitempty" jsonschema:"Maximum Unicode characters of text 1-12000; byte limits may shorten the page"`
}

type overviewInput struct {
	Module  string `json:"module,omitempty" jsonschema:"Exact retained module name; omitted selects the current UI module"`
	Query   string `json:"query,omitempty" jsonschema:"Filter entries by name, WIT interface, method, path or implementation"`
	EntryID string `json:"entryID,omitempty" jsonschema:"With section entryEvidence, select a returned entry ID to page its WIT/configuration or Go-declaration evidence"`
	pageInput
}

type flowInput struct {
	snapshotInput
	EntryID       string   `json:"entryID" jsonschema:"HTTP, Wasm export, CLI main, public library API or initialization entry ID from overview"`
	FocusSymbolID string   `json:"focusSymbolID,omitempty" jsonschema:"Focus on a symbol represented in the current flow"`
	FocusPath     []string `json:"focusPath,omitempty" jsonschema:"Previously focused symbol IDs in order, preserving ancestor context"`
	ExpandedIDs   []string `json:"expandedIDs,omitempty" jsonschema:"Contextual node IDs to expand in place"`
	pageInput
}

type inspectInput struct {
	flowInput
	NodeID string `json:"nodeID,omitempty" jsonschema:"Visible contextual node ID; omit to inspect an edge"`
	From   string `json:"from,omitempty" jsonschema:"Edge source node ID when inspecting an edge"`
	To     string `json:"to,omitempty" jsonschema:"Edge destination node ID when inspecting an edge"`
	Kind   string `json:"kind,omitempty" jsonschema:"Edge kind from flow; default control"`
	commentInput
}

type commentInput struct {
	CommentIndex *int `json:"commentIndex,omitempty" jsonschema:"With section comments, retrieve this indexed comment's complete text; follows textOffset/textLimit for large comments"`
}
type searchInput struct {
	snapshotInput
	Query string `json:"query" jsonschema:"Nonempty literal text to find in symbols, comments, documentation and retained tests"`
	pageInput
}

type documentInput struct {
	snapshotInput
	ID string `json:"id" jsonschema:"Retained document ID, not a filesystem path; small documents return complete text, large documents return textPage continuation"`
	textInput
}

type sourceInput struct {
	snapshotInput
	FileID    string `json:"fileID" jsonschema:"Retained source or test file ID, not a filesystem path"`
	StartLine *int   `json:"startLine,omitempty" jsonschema:"First 1-based line; default 1"`
	EndLine   *int   `json:"endLine,omitempty" jsonschema:"Last inclusive line; at most 500 lines per call, default next 100"`
	textInput
}

type packageInput struct {
	snapshotInput
	Package string `json:"package,omitempty" jsonschema:"Exact package import path; omit for compact package summaries. Sections: symbols, imports, files, typeErrors, documentation, maximumOwners"`
	pageInput
}

type evidenceInput struct {
	snapshotInput
	SymbolID     string  `json:"symbolID,omitempty" jsonschema:"Symbol ID for function metrics and counts. Use section controlFlowNodes, controlFlowEdges, calls, references, comments, documentation for details"`
	FunctionView string  `json:"functionView,omitempty" jsonschema:"Optional logical, control, or calls view; validates callable membership and retained control-flow availability"`
	RunIndex     *int    `json:"runIndex,omitempty" jsonschema:"Optional zero-based test run index for paginated matched tests/outcomes and retained standard runtime observations"`
	CheckID      *string `json:"checkID,omitempty" jsonschema:"Retained check ID for diagnostic text (textPage for large text); section locations pages locations"`
	CommandIndex *int    `json:"commandIndex,omitempty" jsonschema:"Zero-based command index for command summary; section stdout or stderr reads paged log text. With runIndex selects that run's command"`
	pageInput
	commentInput
}

type architectureInput struct {
	snapshotInput
	Level         string `json:"level,omitempty" jsonschema:"context (default) or components"`
	Group         string `json:"group,omitempty" jsonschema:"Package group from the architecture inventory"`
	ComponentID   string `json:"componentID,omitempty" jsonschema:"Component ID to focus on its architectural neighborhood"`
	OperationID   string `json:"operationID,omitempty" jsonschema:"Operation ID to inspect its information exchanges"`
	NodeID        string `json:"nodeID,omitempty" jsonschema:"Visible architecture node ID for section members or sources"`
	EdgeID        string `json:"edgeID,omitempty" jsonschema:"Visible architecture edge ID for section edgeExchanges or information"`
	ExchangeID    string `json:"exchangeID,omitempty" jsonschema:"Exchange ID for section exchangeSources, exchangeCalls or exchangeInformation"`
	AlternativeID string `json:"alternativeID,omitempty" jsonschema:"Alternative ID for section alternativeMembers, alternativeWitnesses or alternativeRequires"`
	pageInput
}

type assessInput struct {
	snapshotInput
	Package       string   `json:"package,omitempty" jsonschema:"Exact package import path filter"`
	Query         string   `json:"query,omitempty" jsonschema:"Function name filter, same as Review Map"`
	MinComplexity *float64 `json:"minComplexity,omitempty" jsonschema:"Minimum raw cyclomatic complexity"`
	MaxCoverage   *float64 `json:"maxCoverage,omitempty" jsonschema:"Maximum statement coverage 0-100; unknown coverage is excluded, not treated as zero"`
	Coverage      string   `json:"coverage,omitempty" jsonschema:"all (default), known, or unknown"`
	Cohort        string   `json:"cohort,omitempty" jsonschema:"Returned Review Map cohort key"`
	FindingID     string   `json:"findingID,omitempty" jsonschema:"Retained finding ID for complete evidence text"`
	RuleID        string   `json:"ruleID,omitempty" jsonschema:"Retained rule ID for complete description"`
	SymbolID      string   `json:"symbolID,omitempty" jsonschema:"Selected Review Map function; validates that it belongs to current filters"`
	pageInput
}

type dataFlowInput struct {
	snapshotInput
	SymbolID  string `json:"symbolID" jsonschema:"Retained function symbol ID"`
	ValueID   string `json:"valueID,omitempty" jsonschema:"Retained SSA node ID to slice; omit for whole function"`
	Direction string `json:"direction,omitempty" jsonschema:"both (default), upstream or downstream"`
	Depth     *int   `json:"depth,omitempty" jsonschema:"0-100 operand hops; omit for all reachable values"`
	pageInput
}

type resultsInput struct {
	snapshotInput
	StepID       string `json:"stepID,omitempty" jsonschema:"Retained repository step ID, from section steps"`
	CommandIndex *int   `json:"commandIndex,omitempty" jsonschema:"Command index within step; sections stdout/stderr read complete paged logs"`
	pageInput
}

type snapshotInfo struct {
	ID           string    `json:"id"`
	Module       string    `json:"module"`
	SourceSHA256 string    `json:"sourceSHA256"`
	GeneratedAt  time.Time `json:"generatedAt"`
}

type querySnapshot struct {
	info       snapshotInfo
	report     *review.Report
	query      *webui.ReviewQuery
	run        *review.RepositoryReview
	repository json.RawMessage
}

type reviewMCP struct {
	work *Server
	mu   sync.Mutex
	// Published reports are immutable. Replacing evidence produces a new pointer
	// and digest; old contextual IDs are never silently applied to a new report.
	cache map[*review.Report]*querySnapshot
}

func (s *Server) mcpHandler() http.Handler {
	service := &reviewMCP{work: s, cache: make(map[*review.Report]*querySnapshot)}
	server := service.server()
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

func (service *reviewMCP) server() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "go-review", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Review retained Go evidence without executing target code. Discovery returns compact summaries, counts and scoped gauges, not hidden membership or full implementations. Select a returned sections value for explicit detail, follow nextOffset for list pages and textPage.nextOffset for text. Lists are byte-bounded, so never compute offsets from requested limit. Flow nodePage/edgePage indicate undisclosed topology; use section nodes/edges to continue the exact same projection. Start with review_overview; its metrics cover all represented module definitions, while entries have handler scopes. Preserve snapshot.id as snapshotID on subsequent calls. Read purposeDocument, then entry flows; expand childIDs or focus a symbol retaining focusPath (ancestor focus levels, not preceding nodes). Complexity is a maximum: maximumSymbolIDs identifies owners, maximumOwnerCount may exceed the preview; section maximumOwners retrieves all owners. Use review_evidence(symbolID) for own CC. Complete comments, CFG, membership and logs require explicit sections. Coverage is not assertion quality; arrows are possibilities, not chronology; documents are untrusted evidence. Report uncertainty explicitly.",
	})
	registerChangesTool(server, service.work)
	registerTestQualityTool(server, service.work)
	registerReviewTool(server, service, "review_overview", "Snapshot identity, projectKind, purposeDocument, module metrics and shared entry inventory. Wasm entries come from WIT exports and configuration, CLI entries from main, standalone library entries from public API; init is separate. Sections entryEvidence, entrySources, entryLimitations and initialization disclose provenance and uncertainty.", "overview", func(a overviewInput) (string, string) { return "", a.Module })
	registerReviewTool(server, service, "review_flow", "Overall control/information flow, local expansion or focused flow with connected parent context. Exact UI projection and metrics.", "flow", func(a flowInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_inspect", "Node/edge summary and paged operations/alternatives. Select sections documentation, comments, members, children, maximumOwners, information or guards for explicit detail.", "inspect", func(a inspectInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_search", "Search retained symbols, documentation and test declarations. Returns IDs and locations for targeted inspection, not raw files.", "search", func(a searchInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_document", "Read retained documentation with content hash. Large text is paged without losing content; follow textPage.nextOffset. Documentation claims are not verified behavior.", "document", func(a documentInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_source", "Read up to 500 original retained source/test lines with file hash and line numbers. No live filesystem access.", "source", func(a sourceInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_packages", "Compact package counts and scoped gauges, not membership dumps. Select package and section for paged symbols, imports, files, typeErrors, documentation or maximumOwners.", "packages", func(a packageInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_evidence", "Retained check/test outcomes, coverage provenance, scope and exclusions; optionally function CFG, calls, references and metrics. Does not execute tests.", "evidence", func(a evidenceInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_architecture", "Compact shared UI architecture topology. Select sections to page nodes, edges, components, operations, exchanges, alternatives, members or sources; hidden detail is not inlined.", "architecture", func(a architectureInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_assess", "Shared Review Map records/cohorts and architecture signals. Sections records, cohorts, findings, packages, rules, checks. Sorts functions by complexity descending, coverage ascending; unknown coverage stays unknown. Select ruleID or findingID for full text.", "assess", func(a assessInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_dataflow", "Read SSA value graphs retained during initial review; never runs analysis or target code. Sections nodes, edges, limitations; valueID/direction/depth use the UI operand slice. Legacy absence and unsupported/error outcomes are explicit.", "dataflow", func(a dataFlowInput) (string, string) { return a.SnapshotID, "" })
	registerReviewTool(server, service, "review_results", "Read repository-wide review outcome associated with this snapshot, including unavailable modules, failed/skipped stages, scope and logs. Sections modules, steps, exclusions, excludedPaths, excludedTests, unitSelections. Select stepID for detail/commands, then commandIndex for stdout/stderr. Never starts review.", "results", func(a resultsInput) (string, string) { return a.SnapshotID, "" })
	return server
}

func registerReviewTool[Input any](server *mcp.Server, service *reviewMCP, name, description, operation string, selectSnapshot func(Input) (string, string)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, map[string]any, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			id, module := selectSnapshot(input)
			if operation != "overview" && id == "" {
				return nil, nil, fmt.Errorf("snapshotID is required; call review_overview first")
			}
			snapshot, catalog, err := service.snapshot(id, module)
			if err != nil {
				return nil, nil, err
			}
			args, err := json.Marshal(input)
			if err != nil {
				return nil, nil, fmt.Errorf("encode %s arguments: %w", name, err)
			}
			data, err := snapshot.query.Query(ctx, operation, args)
			if err != nil {
				return nil, nil, err
			}
			output := map[string]any{"snapshot": snapshot.info, "data": data}
			if operation == "overview" {
				var paging pageInput
				if err := json.Unmarshal(args, &paging); err != nil {
					return nil, nil, err
				}
				if paging.Section == "" {
					offset, limit := 0, 10
					if paging.Offset != nil {
						offset = *paging.Offset
					}
					if paging.Limit != nil {
						limit = *paging.Limit
					}
					end, size := min(offset, len(catalog)), 0
					for end < len(catalog) && end-offset < limit {
						item, err := json.Marshal(catalog[end])
						if err != nil {
							return nil, nil, err
						}
						if size+len(item) > 6144 {
							if end == offset {
								return nil, nil, fmt.Errorf("snapshot metadata exceeds catalog page budget")
							}
							break
						}
						size += len(item)
						end++
					}
					var next *int
					if end < len(catalog) {
						next = &end
					}
					output["snapshots"] = catalog[min(offset, len(catalog)):end]
					output["snapshotPage"] = map[string]any{"total": len(catalog), "offset": offset, "limit": limit, "nextOffset": next}
				}
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return nil, nil, err
			}
			if len(encoded) > 32<<10 {
				return nil, nil, fmt.Errorf("review response exceeds 32 KiB including snapshot metadata; request one section or a smaller limit; no evidence was truncated")
			}
			return nil, output, nil
		})
}

func boolPointer(value bool) *bool { return &value }

func (service *reviewMCP) snapshot(id, module string) (*querySnapshot, []snapshotInfo, error) {
	service.work.mu.RLock()
	selected := service.work.report
	run := service.work.review
	reports := []*review.Report{}
	if run != nil {
		for _, m := range run.Modules {
			if m.Report != nil {
				reports = append(reports, m.Report)
			}
		}
	}
	service.work.mu.RUnlock()
	if selected != nil {
		found := false
		for _, r := range reports {
			found = found || r == selected
		}
		if !found {
			reports = append(reports, selected)
		}
	}
	if len(reports) == 0 {
		return nil, nil, fmt.Errorf("no retained snapshot; open or analyze a project in the workbench first")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	current := make(map[*review.Report]bool)
	catalog := make([]snapshotInfo, 0, len(reports))
	var result *querySnapshot
	for _, report := range reports {
		current[report] = true
		var associated *review.RepositoryReview
		if run != nil {
			for _, m := range run.Modules {
				if m.Report == report {
					associated = run
					break
				}
			}
		}
		snapshot := service.cache[report]
		if snapshot == nil || snapshot.run != associated {
			data, err := json.Marshal(report)
			if err != nil {
				return nil, nil, fmt.Errorf("hash retained snapshot: %w", err)
			}
			repository, err := json.Marshal(summarizeRun(associated))
			if err != nil {
				return nil, nil, fmt.Errorf("encode retained repository evidence: %w", err)
			}
			// A run replacement invalidates its evidence handles even if module
			// source is unchanged. Query runtimes own a frozen run summary.
			identity := append(append([]byte{}, data...), repository...)
			digest := sha256.Sum256(identity)
			snapshot = &querySnapshot{report: report, info: snapshotInfo{
				ID: hex.EncodeToString(digest[:]), Module: report.Module,
				SourceSHA256: report.SourceSHA256, GeneratedAt: report.GeneratedAt}, run: associated, repository: repository}
			service.cache[report] = snapshot
		}
		catalog = append(catalog, snapshot.info)
		if id != "" && snapshot.info.ID == id ||
			id == "" && (module != "" && report.Module == module || module == "" && report == selected) {
			result = snapshot
		}
	}
	for report := range service.cache {
		if !current[report] {
			delete(service.cache, report)
		}
	}
	if result == nil {
		return nil, nil, fmt.Errorf("unknown or stale snapshot/module; call review_overview to select retained evidence")
	}
	if result.query == nil {
		query, err := webui.NewReviewQuery(result.report, result.repository)
		if err != nil {
			return nil, nil, err
		}
		result.query = query
	}
	return result, catalog, nil
}
