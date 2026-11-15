package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestRetrievalSharedHTTPAndMCP(t *testing.T) {
	embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models/configured" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("unconfigured endpoint %s", r.URL)
			w.WriteHeader(500)
			return
		}
		var input struct {
			Model string
			Input []string
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Model != "configured" {
			t.Errorf("unconfigured model %s", input.Model)
		}
		data := []any{}
		for i := range input.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0, 0}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "configured"})
	}))
	defer embeddings.Close()
	report := &review.Report{Module: "sample", Files: []review.File{{ID: "file", Path: "sample.go", Package: "sample", Source: "package sample\nfunc RejectRange(n int) bool { return n < 1 }\n"}},
		Symbols: []review.Symbol{{ID: "reject", Name: "RejectRange", Package: "sample", Kind: "function", Location: review.Location{FileID: "file", Line: 2, EndLine: 2}}}}
	work := New(report)
	if err := work.ConfigureEmbeddings(intelligence.EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: embeddings.URL, Model: "configured", Generation: "one"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := work.ShutdownRetrieval(context.Background()); err != nil {
			t.Error(err)
		}
	})
	snapshot, _, err := work.newChatService().snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, input any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(string(data)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		work.Handler().ServeHTTP(response, request)
		return response
	}
	query := map[string]any{"snapshotID": snapshot.info.ID, "query": "RejectRange", "limit": 10}
	if response := post("/api/retrieval/query", query); response.Code < 400 {
		t.Fatal("read-only query secretly built index")
	}
	if response := post("/api/retrieval/index", map[string]any{"snapshotID": snapshot.info.ID, "action": "start"}); response.Code != 202 {
		t.Fatalf("index start %d %s", response.Code, response.Body)
	}
	work.retrievalMu.Lock()
	state := work.retrieval
	work.retrievalMu.Unlock()
	state.mu.Lock()
	done := state.done
	state.mu.Unlock()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("index did not finish")
	}
	response := post("/api/retrieval/query", query)
	if response.Code != 200 {
		t.Fatalf("query %d %s", response.Code, response.Body)
	}
	var httpResult map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &httpResult); err != nil {
		t.Fatal(err)
	}
	if response := post("/api/retrieval/query", map[string]any{"snapshotID": "stale", "query": "RejectRange"}); response.Code < 400 {
		t.Fatal("stale identity accepted")
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := work.newChatService().server().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "retrieval-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "review_retrieve", Arguments: query})
	if err != nil || result.IsError {
		t.Fatalf("MCP query %+v %v", result, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var mcpResult map[string]any
	if err := json.Unmarshal(data, &mcpResult); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(httpResult["hits"]) != fmt.Sprint(mcpResult["hits"]) || httpResult["snapshotID"] != mcpResult["snapshotID"] {
		t.Fatalf("transport divergence HTTP=%v MCP=%v", httpResult, mcpResult)
	}
}
