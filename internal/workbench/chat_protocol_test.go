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

func TestAgentMCPRejectsTextualToolRequests(t *testing.T) {
	var calls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := "Verified answer."
		if calls == 1 {
			content = `[TOOL_REQUEST><invoke name="review_document">doc</invoke>[END_TOOL_REQUEST]`
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		if calls == 2 {
			data = []byte(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"stay","function":{"name":"navigate_ui","arguments":"{\"view\":\"unchanged\"}"}}]},"finish_reason":"tool_calls"}]}`)
		}
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer model.Close()
	s := New(&review.Report{Module: "sample"})
	service := s.newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []chatEvent
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "ornith"}, chatRequest{Message: "Explain"}, snapshot, service, func(e chatEvent) error { events = append(events, e); return nil })
	if err != nil || calls != 3 {
		t.Fatalf("invalid syntax was accepted as an answer: calls=%d err=%v events=%+v", calls, err, events)
	}
	discard := false
	for _, event := range events {
		discard = discard || event.Type == "discard_turn"
	}
	if !discard {
		t.Fatal("invalid textual tool request was left as normal response text")
	}
}
