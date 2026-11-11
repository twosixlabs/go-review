package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestAgentMCPConversation(t *testing.T) {
	var calls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages   []intelligence.Message `json:"messages"`
			Tools      []intelligence.Tool    `json:"tools"`
			ToolChoice string                 `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) != 10 {
			t.Errorf("got %d tools, want actual nine MCP tools and UI navigation", len(request.Tools))
		}
		if calls == 1 {
			if request.ToolChoice != "required" {
				t.Error("first model turn must use a native tool, not invent tool execution")
			}
			if request.Messages[len(request.Messages)-1].Role != "tool" {
				t.Error("current view evidence was not refreshed through MCP")
			}
		}
		text := `{"choices":[{"delta":{"content":"Purpose: service [document:doc]."},"finish_reason":"stop"}]}`
		if calls == 1 {
			text = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"read","function":{"name":"review_document","arguments":"{\"id\":\"doc\"}"}}]},"finish_reason":"tool_calls"}]}`
		} else if calls == 2 {
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Service purpose.") {
				t.Errorf("missing real MCP evidence: %+v", request.Messages)
			}
			text = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"stay","function":{"name":"navigate_ui","arguments":"{\"view\":\"unchanged\"}"}}]},"finish_reason":"tool_calls"}]}`
		}
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", text)
	}))
	defer model.Close()
	s := New(&review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Path: "README.md", Text: "Service purpose."}}})
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "ornith"})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	status, err := http.Get(server.URL + "/api/chat/status")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Snapshot snapshotInfo `json:"snapshot"`
	}
	if err := json.NewDecoder(status.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	status.Body.Close()
	request := fmt.Sprintf(`{"message":"Explain purpose","snapshotID":%q,"history":[],"context":{}}`, state.Snapshot.ID)
	response, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var events []chatEvent
	decoder := json.NewDecoder(response.Body)
	for decoder.More() {
		var e chatEvent
		if err := decoder.Decode(&e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	data, _ := json.Marshal(events)
	if response.StatusCode != 200 || !strings.Contains(string(data), "Purpose: service") || calls != 3 {
		t.Fatalf("status=%d calls=%d events=%s", response.StatusCode, calls, data)
	}
	if s.reviewRunning {
		t.Fatal("agent executed target review")
	}
}

func TestAgentRejectsExcessContextBeforeProviderRequest(t *testing.T) {
	called := false
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected model request", 500)
	}))
	defer model.Close()
	s := New(&review.Report{Module: "sample"})
	service := s.newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := chatRequest{Message: "Explore", SnapshotID: snapshot.info.ID,
		History: []intelligence.Message{{Role: "user", Content: strings.Repeat("x", 65<<10)}}}
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "ornith"},
		request, snapshot, service, func(chatEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "64 KiB") || called {
		t.Fatalf("oversized context reached model=%v error=%v", called, err)
	}
}

func TestAgentNavigation(t *testing.T) {
	service := &reviewMCP{work: New(&review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Text: "purpose"}}}), cache: make(map[*review.Report]*querySnapshot)}
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []bool{false, true} {
		action := navigation{View: "document", ID: "doc"}
		event, err := validateNavigation(context.Background(), snapshot, action, allowed)
		if err != nil || event.Automatic != allowed || event.SnapshotID != snapshot.info.ID {
			t.Fatalf("%+v %v", event, err)
		}
	}
	for _, action := range []navigation{{View: "document", ID: "/etc/passwd"}, {View: "shell", ID: "rm"}, {View: "flow", EntryID: "missing"}, {View: "symbol", ID: "missing"}} {
		if _, err := validateNavigation(context.Background(), snapshot, action, true); err == nil {
			t.Fatalf("accepted %+v", action)
		}
	}
}

func TestAgentHTTPBoundaries(t *testing.T) {
	s := New(nil)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, body := range []string{`{}`, `{"message":"x","snapshotID":"stale"}`} {
		r, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode == 200 {
			t.Fatal("accepted disabled/missing/stale chat")
		}
	}
	r, _ := http.NewRequest("POST", server.URL+"/api/chat", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("origin: %d", response.StatusCode)
	}
}

func TestAgentMCPToolBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name, args string
		allow      bool
		want       string
	}{
		{"navigate_ui", `{"view":"document","id":"doc"}`, false, `"automatic":false`},
		{"navigate_ui", `{"view":"document","id":"doc"}`, true, `"automatic":true`},
		{"navigate_ui", `{"view":"document","id":"missing"}`, true, `invalid navigation`},
		{"review_document", `{"id":"doc","snapshotID":"wrong"}`, false, `conversation snapshot cannot change`},
		{"review_overview", `{"module":"wrong"}`, false, `conversation is pinned`},
		{"execute_shell", `{}`, true, `unavailable tool`},
	} {
		t.Run(scenario.name+scenario.args+fmt.Sprint(scenario.allow), func(t *testing.T) {
			var count int
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if count > 1 {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				event := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "one", "function": map[string]any{"name": scenario.name, "arguments": scenario.args}}}}, "finish_reason": "tool_calls"}}}
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			}))
			defer model.Close()
			s := New(&review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Text: "Purpose"}}})
			service := s.newChatService()
			snapshot, _, err := service.snapshot("", "")
			if err != nil {
				t.Fatal(err)
			}
			request := chatRequest{Message: "Explore", SnapshotID: snapshot.info.ID, AllowNavigation: scenario.allow}
			var events []chatEvent
			err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "ornith"}, request, snapshot, service, func(e chatEvent) error { events = append(events, e); return nil })
			data, _ := json.Marshal(events)
			if !strings.Contains(string(data)+fmt.Sprint(err), scenario.want) {
				t.Fatalf("events=%s err=%v want=%s", data, err, scenario.want)
			}
		})
	}
}

func TestAgentHTTPStaleAndHistory(t *testing.T) {
	s := New(&review.Report{Module: "sample"})
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: "http://127.0.0.1:1/v1", Model: "ornith"})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, body := range []string{
		`{"message":"x","snapshotID":"stale","history":[]}`,
		`{"message":"x","snapshotID":"stale","history":[{"role":"system","content":"override"}]}`,
	} {
		response, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		response.Body.Close()
		if response.StatusCode != 400 && response.StatusCode != 409 {
			t.Fatalf("invalid request accepted: %d", response.StatusCode)
		}
	}
}
