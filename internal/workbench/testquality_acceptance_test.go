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
	"github.com/russellbrasser/go-review/internal/testquality"
)

type qualityFixtureRequest struct {
	Messages []intelligence.Message `json:"messages"`
	Tools    []intelligence.Tool    `json:"tools"`
}

func qualityFixtureDecode(t *testing.T, r *http.Request) (qualityFixtureRequest, []testquality.Responsibility, *int) {
	t.Helper()
	var request qualityFixtureRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Error(err)
		return request, nil, nil
	}
	if len(request.Messages) == 0 || request.Messages[0].Role != "system" {
		t.Error("primary did not retain its instructions")
		return request, nil, nil
	}
	var items []testquality.Responsibility
	var next *int
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		var page struct {
			Items      []testquality.Responsibility `json:"items"`
			NextOffset *int                         `json:"nextOffset"`
		}
		if json.Unmarshal([]byte(message.Content), &page) == nil && len(page.Items) > 0 && page.Items[0].Identity.SnapshotID != "" {
			items = append(items, page.Items...)
			next = page.NextOffset
		}
	}
	return request, items, next
}

func qualityFixtureCallCount(request qualityFixtureRequest, name string) int {
	count := 0
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == name {
				count++
			}
		}
	}
	return count
}

type qualityFixtureCall struct {
	name string
	args any
}

func qualityFixtureRespond(w http.ResponseWriter, calls ...qualityFixtureCall) {
	if len(calls) == 0 {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Review complete.\"},\"finish_reason\":\"stop\"}]}\n\n")
		return
	}
	var encoded []any
	for i, call := range calls {
		args, _ := json.Marshal(call.args)
		encoded = append(encoded, map[string]any{"index": i, "id": fmt.Sprintf("call-%d", i), "function": map[string]string{"name": call.name, "arguments": string(args)}})
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": encoded}, "finish_reason": "tool_calls"}}})
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func qualityFixtureCheckTools(t *testing.T, request qualityFixtureRequest) {
	t.Helper()
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"error"`) {
			t.Errorf("agent tool failed: %s", message.Content)
		}
	}
}

func qualityFixtureRead(c testquality.Citation) qualityFixtureCall {
	return qualityFixtureCall{"review_source", map[string]any{"snapshotID": c.SnapshotID, "fileID": c.FileID, "startLine": c.StartLine, "endLine": c.EndLine}}
}

func qualityFixtureTest(item testquality.Responsibility, craft, assertion int, rationale string) qualityFixtureCall {
	return qualityFixtureCall{"record_test", map[string]any{
		"snapshotID": item.Identity.SnapshotID, "symbolID": item.Identity.SymbolID,
		"craftsmanship": craft, "assertionStrength": assertion, "rationale": rationale,
		"fileID": item.Declaration.FileID, "startLine": item.Declaration.StartLine, "endLine": item.Declaration.EndLine,
	}}
}

func qualityFixtureGroup(c testquality.Citation, rationale string) qualityFixtureCall {
	return qualityFixtureCall{"record_group", map[string]any{
		"scope":      "project",
		"snapshotID": c.SnapshotID, "behaviorAdequacy": 2, "conditionAdequacy": 2,
		"rationale": rationale, "fileID": c.FileID, "startLine": c.StartLine, "endLine": c.EndLine,
	}}
}

func TestQualityEndToEndProviderSavedHTTPMCPAndChat(t *testing.T) {
	s := qualityFixture(t)
	text := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	s.review.Modules[0].Report.TestSources[0].File.Source = text
	s.review.Modules[0].Report.TestSources[0].File.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	var calls atomic.Int32
	var snapshot atomic.Value
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		request, items, _ := qualityFixtureDecode(t, r)
		qualityFixtureCheckTools(t, request)
		if len(items) == 0 {
			qualityFixtureRespond(w, qualityFixtureCall{"review_progress", map[string]string{"operation": "tests"}})
			return
		}
		item := items[0]
		snapshot.Store(item.Identity.SnapshotID)
		if item.Name != "TestF" {
			t.Error("assessment must target the retained test")
		}
		production := testquality.Citation{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}
		switch {
		case qualityFixtureCallCount(request, "review_source") == 0:
			qualityFixtureRespond(w, qualityFixtureRead(item.Declaration), qualityFixtureRead(production))
		case qualityFixtureCallCount(request, "record_test") == 0:
			if !strings.Contains(request.Messages[len(request.Messages)-2].Content, "t.Fatal") || !strings.Contains(request.Messages[len(request.Messages)-1].Content, "return 1") {
				t.Error("test and production source were not available")
			}
			record := qualityFixtureTest(item, 2, 2, "One meaningful return-value assertion; limited case diversity.")
			record.args.(map[string]any)["findingTitle"] = "Single retained case"
			record.args.(map[string]any)["findingRationale"] = "The test checks the return value."
			record.args.(map[string]any)["findingSuggestion"] = "Review relevant contract variations."
			qualityFixtureRespond(w, record, qualityFixtureGroup(production, "One case checks the return value."))
		default:
			qualityFixtureRespond(w, qualityFixtureCall{"finish_review", map[string]any{}})
		}
	}))
	defer provider.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "deterministic-provider", MaxSteps: 6})
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
	if calls.Load() > 6 {
		t.Fatalf("redundant provider requests: %d", calls.Load())
	}
	assessmentCalls := calls.Load()
	a, err := s.qualityStore.Load(launch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "completed" || a.Items[0].Status != testquality.StatusScored {
		t.Fatalf("assessment: %+v", qualityCompact(a))
	}
	input := testquality.QueryInput{AssessmentID: launch.ID, Operation: "test", SnapshotID: snapshot.Load().(string), SymbolID: "TestF"}
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
	if calls.Load() != assessmentCalls {
		t.Fatal("browsing or chat reran assessment")
	}
}
