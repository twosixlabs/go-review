package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestAgentMCPRequiresActualNavigationDecision(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("authorized-%t", allowed), func(t *testing.T) {
			testAgentNavigationDecision(t, allowed)
		})
	}
}

func testAgentNavigationDecision(t *testing.T, allowed bool) {
	t.Helper()
	var calls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Tools      []intelligence.Tool `json:"tools"`
			ToolChoice string              `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var delta any = map[string]string{"content": "I expanded the document."}
		finish := "stop"
		if len(request.Tools) == 1 && request.Tools[0].Function.Name == "navigate_ui" {
			if request.ToolChoice != "required" {
				t.Error("navigation decision is not required")
			}
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "navigate", "function": map[string]string{"name": "navigate_ui", "arguments": `{"view":"document","id":"doc"}`}}}}
			finish = "tool_calls"
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer model.Close()
	s := New(&review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Text: "Purpose"}}})
	service := s.newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	var navigated bool
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "model"},
		chatRequest{Message: "Show purpose document", AllowNavigation: allowed}, snapshot, service,
		func(event chatEvent) error {
			if event.Navigation != nil {
				navigated = true
				if event.Navigation.Automatic != allowed {
					t.Error("navigation permission changed")
				}
			}
			return nil
		})
	if err != nil || !navigated || calls != 3 {
		t.Fatalf("prose stood in for a navigation action: calls=%d navigated=%v err=%v", calls, navigated, err)
	}
}
