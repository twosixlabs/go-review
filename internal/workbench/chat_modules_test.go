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

func TestAgentExploresRetainedModulesWithoutChangingSelection(t *testing.T) {
	first := &review.Report{Module: "first", Root: "/repo/first"}
	second := &review.Report{Module: "second", Root: "/repo/second", Documents: []review.Document{{ID: "purpose", Text: "Second module evidence"}}}
	s := New(first)
	s.review = &review.RepositoryReview{Modules: []review.ReviewedModule{{Directory: first.Root, Report: first}, {Directory: second.Root, Report: second}}}
	service := s.newChatService()
	snapshot, _, err := service.snapshot("", "first")
	if err != nil {
		t.Fatal(err)
	}

	other, _, err := service.snapshot("", "second")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		delta := map[string]any{"content": "Explained"}
		finish := "stop"
		if calls == 1 {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "other", "function": map[string]string{
				"name": "review_document", "arguments": fmt.Sprintf(`{"id":"purpose","snapshotID":%q}`, other.info.ID)}}}}
			finish = "tool_calls"
		} else if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Second module evidence") {
			t.Error("did not retrieve real second-module evidence")
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer model.Close()
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "fixture"},
		chatRequest{Message: "Explain the second module"}, snapshot, service, func(chatEvent) error { return nil })
	if err != nil || calls != 2 || s.current() != first {
		t.Fatalf("calls=%d selection=%v err=%v", calls, s.current(), err)
	}
}

func TestAgentModuleSelectionRejectsStaleEvidence(t *testing.T) {
	first := &review.Report{Module: "first", Root: "/repo/first"}
	second := &review.Report{Module: "second", Root: "/repo/second"}
	s := New(first)
	s.review = &review.RepositoryReview{Modules: []review.ReviewedModule{{Directory: first.Root, Report: first}, {Directory: second.Root, Report: second}}}
	target, _, err := s.newChatService().snapshot("", "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stale", target.info.ID} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/review/select", strings.NewReader(fmt.Sprintf(`{"snapshotID":%q}`, id)))
		request.Header.Set("Content-Type", "application/json")
		s.selectReviewModule(response, request)
		if id == "stale" {
			if response.Code < 400 || s.current() != first {
				t.Fatal("stale module selection changed evidence")
			}
		} else if response.Code != 200 || s.current() != second {
			t.Fatalf("valid selection failed: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestAgentHTTPStatusNeverDisclosesCredentials(t *testing.T) {
	s := New(&review.Report{Module: "fixture"})
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: "https://example.com/v1", Model: "fixture", APIKey: "private-fixture-key"})
	response := httptest.NewRecorder()
	s.chatStatus(response, httptest.NewRequest("GET", "/api/chat/status", nil))
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-fixture-key") ||
		!strings.Contains(response.Body.String(), `"external":true`) {
		t.Fatalf("unsafe provider status: %s", response.Body.String())
	}
}

func TestAgentReadingRetainedSnapshotDoesNotSelectIt(t *testing.T) {
	first := &review.Report{Module: "first", Root: "/repo/first"}
	second := &review.Report{Module: "second", Root: "/repo/second"}
	s := New(first)
	s.review = &review.RepositoryReview{Modules: []review.ReviewedModule{{Directory: first.Root, Report: first}, {Directory: second.Root, Report: second}}}
	target, _, err := s.newChatService().snapshot("", "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stale", target.info.ID} {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:8090/api/snapshot?snapshotID="+id, nil))
		if id == "stale" {
			if response.Code != 409 {
				t.Fatalf("stale snapshot status=%d", response.Code)
			}
		} else {
			var received review.Report
			if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || received.Module != "second" {
				t.Fatalf("wrong retained snapshot: %d %q", response.Code, received.Module)
			}
		}
		if s.current() != first {
			t.Fatal("read-only snapshot request changed selection")
		}
	}
}
