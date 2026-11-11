package workbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/intelligence"
)

func changeAcceptanceChat(t *testing.T, s *Server, c *changes.Comparison) {
	t.Helper()
	var helper changes.Record
	for _, record := range c.Records {
		if strings.Contains(record.Title, "helper") && len(record.FlowIDs) > 1 {
			helper = record
			break
		}
	}
	if helper.ID == "" {
		t.Fatal("fixture lost its shared implementation")
	}
	before, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	limit := 100
	calls := []struct {
		name string
		args any
	}{
		{"review_changes", changeQueryInput{Operation: "overview"}},
		{"review_changes", changeQueryInput{Operation: "files", pageInput: pageInput{Limit: &limit}}},
		{"review_changes", changeQueryInput{Operation: "records", pageInput: pageInput{Limit: &limit}}},
		{"review_changes", changeQueryInput{Operation: "records", Perspective: "shared"}},
		{"review_changes", changeQueryInput{Operation: "source", FileID: helper.FileIDs[0], Side: "before"}},
		{"review_changes", changeQueryInput{Operation: "source", FileID: helper.FileIDs[0], Side: "after"}},
		{"review_changes", changeQueryInput{Operation: "witnesses", RecordID: helper.ID, FlowID: helper.FlowIDs[0]}},
		{"review_changes", changeQueryInput{Operation: "correspondences", pageInput: pageInput{Limit: &limit}}},
		{"navigate_changes", changeQueryInput{ComparisonID: c.ID, Operation: "files", Query: "notes"}},
		{"propose_change_action", changeProposal{Action: "state", StateAction: changes.StateAction{
			ComparisonID: c.ID, Version: before.Version, ItemIDs: helper.ItemIDs, Status: "reviewed"}}},
	}
	turns := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turns++
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if turns == 1 {
			tools := []any{}
			for i, call := range calls {
				args, err := json.Marshal(call.args)
				if err != nil {
					t.Error(err)
					return
				}
				tools = append(tools, map[string]any{"index": i, "id": fmt.Sprintf("acceptance-%d", i), "type": "function",
					"function": map[string]any{"name": call.name, "arguments": string(args)}})
			}
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": tools}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
			return
		}
		toolCount := 0
		var evidence strings.Builder
		for _, message := range request.Messages {
			if message.Role != "tool" || !strings.HasPrefix(message.ToolCallID, "acceptance-") {
				continue
			}
			toolCount++
			evidence.WriteString(message.Content)
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(message.Content), &envelope); err != nil {
				t.Error(err)
			}
			if _, failed := envelope["error"]; failed {
				t.Errorf("controlled chat could not explore retained evidence: %s", message.Content)
			}
		}
		if toolCount != len(calls) || !strings.Contains(evidence.String(), "return 1") || !strings.Contains(evidence.String(), "return 2") ||
			!strings.Contains(evidence.String(), "source-call") || !strings.Contains(evidence.String(), "not executed") {
			t.Errorf("chat lost source, witness, or confirmation boundaries: %s", evidence.String())
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"content": "The shared helper changes its result from 1 to 2. Source paths support its callers; runtime effects remain unobserved. [change:" + c.ID + "|" + helper.ID + "]"}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer model.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "controlled-acceptance"})
	data, err := json.Marshal(chatRequest{Message: "Explain the shared helper using both revisions and inclusion witnesses, show supporting notes, and propose reviewing its implementation.",
		ComparisonID: c.ID, AllowNavigation: true, Context: json.RawMessage(`{"kind":"changes","comparisonID":"` + c.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/chat", strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || turns != 2 || !strings.Contains(response.Body.String(), "change_navigation") ||
		!strings.Contains(response.Body.String(), "change_proposal") || !strings.Contains(response.Body.String(), "[change:") {
		t.Fatalf("controlled chat acceptance: status %d, turns %d: %s", response.Code, turns, response.Body.String())
	}
	after, err := c.State()
	a, marshalErr := json.Marshal(before)
	b, afterMarshalErr := json.Marshal(after)
	if err != nil || marshalErr != nil || afterMarshalErr != nil || string(a) != string(b) {
		t.Fatalf("chat navigation/proposal mutated local progress: %s -> %s (%v)", a, b, err)
	}
}
