package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityEndToEndProviderSavedHTTPMCPAndChat(t *testing.T) {
	s := qualityFixture(t)
	text := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	s.review.Modules[0].Report.TestSources = []review.TestSource{{File: review.File{ID: "test", Path: "f_test.go", Source: text, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}}}
	var calls atomic.Int32
	var snapshot string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var initial struct {
			Responsibility    testquality.Responsibility `json:"responsibility"`
			ProductionSource  json.RawMessage            `json:"productionSource"`
			StatementCoverage json.RawMessage            `json:"statementCoverage"`
		}
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &initial); err != nil {
			t.Error(err)
		}
		snapshot = initial.Responsibility.Identity.SnapshotID
		if !strings.Contains(string(initial.ProductionSource), "return 1") || !strings.Contains(string(initial.StatementCoverage), "not-collected") {
			t.Error("missing production/coverage evidence")
		}
		name := "read_evidence"
		args, _ := json.Marshal(testquality.EvidenceInput{SnapshotID: snapshot, Operation: "source", Args: json.RawMessage(`{"fileID":"test","startLine":1,"endLine":3}`)})
		if body.Messages[len(body.Messages)-1].Role == "tool" {
			if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "t.Fatal") {
				t.Error("assertions unavailable")
			}
			value := 2
			citations := []testquality.Citation{{SnapshotID: snapshot, FileID: "f", StartLine: 2, EndLine: 2}, {SnapshotID: snapshot, FileID: "test", StartLine: 3, EndLine: 3}}
			ratings := []testquality.Rating{}
			for _, d := range []testquality.Dimension{testquality.AssertionStrength, testquality.RepresentativeCases, testquality.BehaviorPaths} {
				ratings = append(ratings, testquality.Rating{Dimension: d, State: testquality.RatingAssessed, Value: &value, Rationale: "One meaningful return-value assertion; limited case diversity.", Evidence: citations})
			}
			name = "submit_assessment"
			args, _ = json.Marshal(map[string]any{"status": "scored", "ratings": ratings, "findings": []testquality.Finding{{Category: "representative-cases", Kind: "inference", Title: "Single retained case", Rationale: "The test checks the return value.", Consequence: "Only this scenario is represented.", Suggestion: "Review whether additional contract variations are relevant.", Evidence: citations}}})
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint(calls.Load()), "function": map[string]string{"name": name, "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer provider.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "deterministic-provider", MaxSteps: 3})
	// Cancelling the initiating request AFTER acceptance must not cancel the job.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/test-quality/start", strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	cancel()
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var launch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[launch.ID]
	s.qualityMu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment did not finish")
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls %d", calls.Load())
	}
	a, err := s.qualityStore.Load(launch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "completed" || a.Items[0].Status != testquality.StatusScored {
		t.Fatalf("assessment: %+v", qualityCompact(a))
	}
	input := testquality.QueryInput{AssessmentID: launch.ID, Operation: "responsibility", SnapshotID: snapshot, SymbolID: "F"}
	body, _ := json.Marshal(input)
	httpResult := qualityRequest(t, s, "/api/test-quality/query", string(body))
	if httpResult.Code != 200 {
		t.Fatal(httpResult.Body.String())
	}
	st, ct := mcp.NewInMemoryTransports()
	server, err := s.newChatService().server().Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-quality-acceptance", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "review_test_quality", Arguments: input})
	if err != nil || result.IsError {
		t.Fatalf("MCP: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var mcpResult struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &mcpResult); err != nil {
		t.Fatal(err)
	}
	var left, right any
	json.Unmarshal(httpResult.Body.Bytes(), &left)
	json.Unmarshal(mcpResult.Data, &right)
	l, _ := json.Marshal(left)
	rr, _ := json.Marshal(right)
	if string(l) != string(rr) {
		t.Fatalf("HTTP/MCP evidence differs:\n%s\n%s", l, rr)
	}
	chatProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Messages []intelligence.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if !strings.Contains(b.Messages[0].Content, launch.ID) || !strings.Contains(b.Messages[len(b.Messages)-1].Content, "Single retained case") {
			t.Error("chat did not refresh pinned assessment detail")
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"The saved assessment identifies limited case diversity."},"finish_reason":"stop"}]}`+"\n\n")
	}))
	defer chatProvider.Close()
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: chatProvider.URL, Model: "chat"}, chatRequest{Message: "Explain this assessment", AssessmentID: launch.ID, Context: body}, nil, s.newChatService(), func(chatEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("browsing or chat reran assessment")
	}
}
