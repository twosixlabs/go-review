package workbench

import (
	"encoding/json"
	"fmt"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

// Turn-owned archives retain exact evidence displaced from the model context.
// Handles cannot access another turn, snapshot, or the filesystem.
type chatMemory struct {
	history int
	bytes   int
	archive map[string]string
}

func newChatMemory(history int) *chatMemory {
	return &chatMemory{history: history, archive: make(map[string]string)}
}

func (m *chatMemory) compact(messages []intelligence.Message, budget int) ([]intelligence.Message, bool, error) {
	size := func() int { data, _ := json.Marshal(messages); return len(data) }
	changed := false
	for i := 1; i < len(messages)-4 && size() > budget; i++ {
		message := &messages[i]
		if message.Role != "tool" || len(message.Content) < 1024 {
			continue
		}
		if m.bytes+len(message.Content) > 8<<20 {
			return nil, changed, fmt.Errorf("turn evidence archive reached 8 MiB; continue with a focused follow-up")
		}
		id := fmt.Sprintf("evidence-%d", len(m.archive)+1)
		m.archive[id] = message.Content
		m.bytes += len(message.Content)
		data, err := json.Marshal(map[string]string{"archiveID": id, "status": "archived",
			"hint": "Exact evidence retained. Use recall_evidence with archiveID and offset to retrieve it."})
		if err != nil {
			return nil, changed, err
		}
		message.Content = string(data)
		changed = true
	}
	if size() > budget && m.history > 0 {
		var displaced []intelligence.Message
		for size() > budget-1024 && m.history > 0 {
			count := 1
			if m.history > 1 && messages[1].Role == "user" && messages[2].Role == "assistant" {
				count = 2
			}
			displaced = append(displaced, messages[1:1+count]...)
			messages = append(messages[:1], messages[1+count:]...)
			m.history -= count
		}
		data, err := json.Marshal(displaced)
		if err != nil {
			return nil, changed, err
		}
		if m.bytes+len(data) > 8<<20 {
			return nil, changed, fmt.Errorf("turn context archive reached 8 MiB")
		}
		id := fmt.Sprintf("evidence-%d", len(m.archive)+1)
		m.archive[id] = string(data)
		m.bytes += len(data)
		messages[0].Content += "\nEarlier conversation memory (not verified source evidence) is archived as " + id +
			". Use recall_evidence to read the exact earlier messages when relevant."
		changed = true
	}
	if size() > budget {
		return nil, changed, fmt.Errorf("active investigation exceeds %d-byte model context budget; use smaller evidence pages or increase contextBytes", budget)
	}
	return messages, changed, nil
}

func (m *chatMemory) read(id string, offset int) (map[string]any, error) {
	text, ok := m.archive[id]
	if !ok {
		return nil, fmt.Errorf("unknown turn evidence archive %q", id)
	}
	runes := []rune(text)
	if offset < 0 || offset > len(runes) {
		return nil, fmt.Errorf("archive offset outside 0-%d", len(runes))
	}
	end := min(offset+4000, len(runes))
	var next *int
	if end < len(runes) {
		next = &end
	}
	return map[string]any{"archiveID": id, "text": string(runes[offset:end]), "offset": offset, "nextOffset": next, "total": len(runes)}, nil
}
