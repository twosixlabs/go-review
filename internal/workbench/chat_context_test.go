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

func TestAgentContextArchivesEvidenceWithoutBreakingToolPairs(t *testing.T) {
	memory := newChatMemory(0)
	source := strings.Repeat("retained source ", 4000)
	messages := []intelligence.Message{
		{Role: "system", Content: "Instructions"},
		{Role: "user", Content: "Trace this behavior"},
		{Role: "assistant", ToolCalls: []intelligence.ToolCall{{ID: "read", Function: intelligence.Function{Name: "review_source", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "read", Content: source},
		{Role: "assistant", Content: "Continue"},
		{Role: "tool", ToolCallID: "recent", Content: "Recent result"},
		{Role: "assistant", Content: "Continue"},
		{Role: "tool", ToolCallID: "newest", Content: "Newest result"},
	}
	compacted, changed, err := memory.compact(messages, 32768)
	if err != nil || !changed {
		t.Fatalf("compaction: changed=%v err=%v", changed, err)
	}
	if len(compacted) != len(messages) || compacted[3].ToolCallID != "read" {
		t.Fatal("compaction broke tool-call/result protocol")
	}
	if strings.Contains(compacted[3].Content, "retained source") {
		t.Fatal("raw payload not archived")
	}
	page, err := memory.read("evidence-1", 0)
	if err != nil || !strings.Contains(page["text"].(string), "retained source") || page["nextOffset"] == nil {
		t.Fatalf("archived source not recoverable: %+v %v", page, err)
	}
	if _, err := memory.read("missing", 0); err == nil {
		t.Fatal("unknown archive accepted")
	}
	if _, err := memory.read("evidence-1", -1); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestAgentLongInvestigationManagesEvidenceBetweenRounds(t *testing.T) {
	server := New(&review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Text: strings.Repeat("Source evidence. ", 450)}}})
	service := server.newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	rounds, compactions := 0, 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		delta := map[string]any{"content": "Investigation complete"}
		finish := "stop"
		if rounds <= 20 {
			delta = map[string]any{"content": "Checking relevant evidence.", "tool_calls": []any{map[string]any{
				"index": 0, "id": fmt.Sprintf("read-%d", rounds), "function": map[string]string{"name": "review_document", "arguments": `{"id":"doc"}`}}}}
			finish = "tool_calls"
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer model.Close()
	err = server.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "fixture", ContextBytes: 64 << 10},
		chatRequest{Message: "Investigate these application responsibilities"}, snapshot, service, func(event chatEvent) error {
			if event.Type == "activity" {
				compactions++
			}
			return nil
		})
	if err != nil || rounds != 21 || compactions == 0 {
		t.Fatalf("rounds=%d compactions=%d err=%v", rounds, compactions, err)
	}
}

func TestAgentArchivedConversationRemainsExactlyRecoverable(t *testing.T) {
	old := []intelligence.Message{{Role: "user", Content: strings.Repeat("Earlier context with Unicode: 日本語.\n", 2000)},
		{Role: "assistant", Content: "Earlier explanation"}}
	messages := append([]intelligence.Message{{Role: "system", Content: "Instructions"}}, old...)
	messages = append(messages, intelligence.Message{Role: "user", Content: "Current question"})
	memory := newChatMemory(len(old))
	compacted, changed, err := memory.compact(messages, 32768)
	if err != nil || !changed {
		t.Fatalf("compaction changed=%v err=%v", changed, err)
	}
	if compacted[len(compacted)-1].Content != "Current question" || !strings.Contains(compacted[0].Content, "evidence-1") {
		t.Fatal("current question or earlier-memory handle lost")
	}
	var restored strings.Builder
	for offset := 0; ; {
		page, err := memory.read("evidence-1", offset)
		if err != nil {
			t.Fatal(err)
		}
		restored.WriteString(page["text"].(string))
		next := page["nextOffset"].(*int)
		if next == nil {
			break
		}
		offset = *next
	}
	var recovered []intelligence.Message
	if err := json.Unmarshal([]byte(restored.String()), &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 2 || recovered[0].Content != old[0].Content || recovered[1].Content != old[1].Content {
		t.Fatal("earlier conversation was shortened or changed")
	}
}

func TestAgentRecalledPagesCanLeaveActiveContext(t *testing.T) {
	page := `{"archiveID":"evidence-previous","text":"` + strings.Repeat("Recovered evidence. ", 3000) + `"}`
	messages := []intelligence.Message{{Role: "system", Content: "Instructions"},
		{Role: "tool", ToolCallID: "old-recall", Content: page},
		{Role: "user", Content: "Current question"},
		{Role: "assistant", Content: "Continue"},
		{Role: "tool", ToolCallID: "recent", Content: "Recent evidence"},
		{Role: "assistant", Content: "Continue"}}
	memory := newChatMemory(0)
	compacted, changed, err := memory.compact(messages, 32768)
	if err != nil || !changed {
		t.Fatalf("recalled page could not be archived: changed=%v err=%v", changed, err)
	}
	if compacted[1].ToolCallID != "old-recall" {
		t.Fatal("recall tool result lost its call ID")
	}
	if memory.archive["evidence-1"] != page {
		t.Fatal("recalled evidence no longer exactly recoverable")
	}
}
