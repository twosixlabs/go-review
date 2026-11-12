package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestMCPProgressiveDisclosureSchemas(t *testing.T) {
	text := strings.Repeat("Complete Unicode rationale λ 🧭\n", 2000)
	r := &review.Report{Module: "sample",
		Symbols: []review.Symbol{{ID: "f", Package: "sample", Name: "Work", Kind: "function", Cyclomatic: 43,
			Location: review.Location{FileID: "file", Line: 2}, Comments: []review.Comment{{Text: text}}}},
		Packages:  []review.Package{{Path: "sample", Imports: []string{"fmt", "strings"}}},
		Documents: []review.Document{{ID: "doc", Text: text}},
		Commands:  []review.Command{{Executable: "go", Stdout: text}}}
	work := New(r)
	work.review = &review.RepositoryReview{Status: "complete", Steps: []review.ReviewStep{{ID: "step", Status: "not-run", Title: "Verification", Detail: "Not authorized"}}}
	for i := range 50 {
		work.review.Modules = append(work.review.Modules, review.ReviewedModule{
			Report: &review.Report{Module: fmt.Sprintf("module-%d", i)}})
	}
	work.review.Modules = append(work.review.Modules, review.ReviewedModule{Report: r})
	server := work.newChatService().server()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "disclosure-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s error=%v result=%+v", name, err, result)
		}
		b, err := json.Marshal(result.StructuredContent)
		if err != nil || len(b) > 32768 {
			t.Fatalf("%s bytes=%d error=%v", name, len(b), err)
		}
		var output map[string]any
		if err := json.Unmarshal(b, &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	overview := call("review_overview", map[string]any{})
	var modules []string
	for offset := 0; ; {
		page := call("review_overview", map[string]any{"offset": offset, "limit": 100})
		for _, raw := range page["snapshots"].([]any) {
			modules = append(modules, raw.(map[string]any)["module"].(string))
		}
		next := page["snapshotPage"].(map[string]any)["nextOffset"]
		if next == nil {
			break
		}
		offset = int(next.(float64))
	}
	if len(modules) != 51 || modules[50] != "sample" {
		t.Fatalf("snapshot catalog lost or repeated modules: %v", modules)
	}
	id := overview["snapshot"].(map[string]any)["id"].(string)
	for _, args := range []map[string]any{
		{"id": "doc", "textOffset": 12000, "textLimit": 20},
	} {
		args["snapshotID"] = id
		doc := call("review_document", args)["data"].(map[string]any)
		if doc["text"] != string([]rune(text)[12000:12020]) {
			t.Fatal("text cursor fields not wired through MCP schema")
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"review_packages", map[string]any{"package": "sample", "section": "imports", "offset": 1}},
		{"review_evidence", map[string]any{"symbolID": "f", "section": "comments", "commentIndex": 0, "textLimit": 20}},
		{"review_evidence", map[string]any{"commandIndex": 0, "section": "stdout", "textLimit": 20}},
		{"review_assess", map[string]any{"section": "records", "minComplexity": 40}},
		{"review_dataflow", map[string]any{"symbolID": "f"}},
		{"review_results", map[string]any{"stepID": "step", "section": "detail"}},
	} {
		tc.args["snapshotID"] = id
		call(tc.name, tc.args)
	}
}

func TestMCPReadOnlyRetainedExploration(t *testing.T) {
	r := &review.Report{Module: "sample", SourceSHA256: "source",
		Documents: []review.Document{{ID: "doc", Path: "README.md", Text: "Service purpose."}}}
	s := New(r)
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 14 {
		t.Fatalf("tools=%d, want 14", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only", tool.Name)
		}
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	overview := call("review_overview", map[string]any{})
	if overview.IsError {
		t.Fatalf("overview: %+v", overview)
	}
	data, _ := json.Marshal(overview.StructuredContent)
	var result struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Snapshot.ID == "" {
		t.Fatalf("snapshot: %s (%v)", data, err)
	}
	doc := call("review_document", map[string]any{"snapshotID": result.Snapshot.ID, "id": "doc"})
	encoded, _ := json.Marshal(doc.StructuredContent)
	if doc.IsError || !strings.Contains(string(encoded), "Service purpose.") {
		t.Fatalf("document=%s error=%v", encoded, doc.IsError)
	}
	var concurrent sync.WaitGroup
	for range 4 {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "review_document",
				Arguments: map[string]any{"snapshotID": result.Snapshot.ID, "id": "doc"}})
			if err != nil || response == nil || response.IsError {
				t.Errorf("concurrent query: result=%+v error=%v", response, err)
			}
		}()
	}
	concurrent.Wait()
	for _, args := range []map[string]any{
		{"snapshotID": "stale", "id": "doc"},
		{"snapshotID": "", "id": "doc"},
		{"snapshotID": result.Snapshot.ID, "id": "/etc/passwd"},
	} {
		if !call("review_document", args).IsError {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if !call("review_overview", map[string]any{"limit": 0}).IsError {
		t.Fatal("explicit invalid page size accepted")
	}
	changed := *r
	changed.Documents = []review.Document{{ID: "doc", Text: "Changed evidence."}}
	s.mu.Lock()
	s.report = &changed
	s.mu.Unlock()
	if !call("review_document", map[string]any{"snapshotID": result.Snapshot.ID, "id": "doc"}).IsError {
		t.Fatal("old snapshot silently applied to replacement evidence")
	}
	s.mu.Lock()
	s.report = r
	s.mu.Unlock()
	if s.current() != r || s.reviewRunning {
		t.Fatal("MCP mutated the workbench")
	}
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://untrusted.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", response.StatusCode)
	}
}

func TestMCPProjectEntriesAndContractNavigation(t *testing.T) {
	for _, kind := range []string{"wasm", "cli", "library"} {
		t.Run(kind, func(t *testing.T) {
			entryKind := kind
			if kind == "cli" {
				entryKind = "main"
			}
			r := &review.Report{Module: "sample", SourceSHA256: "source",
				Symbols: []review.Symbol{{ID: "implementation", Name: "Work", Kind: "function", Package: "sample"}},
				EntryPoints: &review.EntryPointInventory{Kind: kind,
					Entries: []review.EntryPoint{{ID: "entry", Kind: entryKind, Name: "Work", SymbolID: "implementation",
						Resolution: "resolved", Evidence: []review.EntryPointEvidence{{Kind: "wit-export", SourceID: "wit", Line: 2}}}},
					Sources: []review.Document{{ID: "wit", Path: "wit/world.wit", Text: "world app { export work: func(); }"}}}}
			r.SystemFlow = review.BuildSystemFlow(r)
			service := New(r).newChatService()
			snapshot, _, err := service.snapshot("", "")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			ss, cs := mcp.NewInMemoryTransports()
			serverSession, err := service.server().Connect(ctx, ss, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "entry-tests", Version: "1"}, nil)
			clientSession, err := client.Connect(ctx, cs, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()
			for _, call := range []struct {
				name string
				args map[string]any
			}{
				{"review_overview", map[string]any{"module": "sample"}},
				{"review_overview", map[string]any{"module": "sample", "section": "entryEvidence", "entryID": "entry"}},
				{"review_document", map[string]any{"snapshotID": snapshot.info.ID, "id": "wit"}},
				{"review_flow", map[string]any{"snapshotID": snapshot.info.ID, "entryID": "entry"}},
			} {
				result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
				if err != nil || result.IsError {
					t.Fatalf("%s result=%+v error=%v", call.name, result, err)
				}
				b, err := json.Marshal(result.StructuredContent)
				if err != nil || len(b) > 32768 {
					t.Fatalf("entry response bytes=%d error=%v", len(b), err)
				}
			}
			for _, nav := range []navigation{{View: "document", ID: "wit"}, {View: "flow", EntryID: "entry"}} {
				if _, err := validateNavigation(ctx, snapshot, nav, true); err != nil {
					t.Fatalf("shared %s entry navigation rejected: %v", kind, err)
				}
			}
		})
	}
}

func TestMCPMissingSnapshot(t *testing.T) {
	httpServer := httptest.NewServer(New(nil).Handler())
	defer httpServer.Close()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "review_overview", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatalf("empty snapshot: result=%+v error=%v", result, err)
	}
}

func TestMCPSnapshotSelectionDoesNotFollowUI(t *testing.T) {
	first := &review.Report{Module: "first", SourceSHA256: "first"}
	second := &review.Report{Module: "second", SourceSHA256: "second"}
	work := &Server{report: first, review: &review.RepositoryReview{
		Modules: []review.ReviewedModule{{Report: first}, {Report: second}},
	}}
	service := &reviewMCP{work: work, cache: make(map[*review.Report]*querySnapshot)}
	selected, catalog, err := service.snapshot("", "second")
	if err != nil || len(catalog) != 2 || selected.report != second || work.current() != first {
		t.Fatalf("module selection mutated UI or failed: %+v %v", selected, err)
	}
	id := selected.info.ID
	work.mu.Lock()
	work.report = second
	work.mu.Unlock()
	pinned, _, err := service.snapshot(id, "")
	if err != nil || pinned.report != second {
		t.Fatalf("pinned selection failed: %v", err)
	}
	if _, _, err := service.snapshot("", "missing"); err == nil {
		t.Fatal("unknown module accepted")
	}
}
