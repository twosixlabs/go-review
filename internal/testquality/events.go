package testquality

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// AgentEvent retains one chronological action, not repeated provider request
// histories. The assessment owner serializes appends with live history reads.
type AgentEvent struct {
	Time     time.Time       `json:"time"`
	AgentID  string          `json:"agentID"`
	ParentID string          `json:"parentID,omitempty"`
	Kind     string          `json:"kind"`
	Step     int             `json:"step,omitempty"`
	Tool     string          `json:"tool,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Message  string          `json:"message,omitempty"`
	Error    string          `json:"error,omitempty"`
	Status   string          `json:"status,omitempty"`
}

const maximumAgentEvent = 8 << 20

func (s Store) agentEventPath(id string) (string, error) {
	if !assessmentID.MatchString(id) {
		return "", fmt.Errorf("invalid assessment ID")
	}
	dir := filepath.Join(s.Directory, id)
	if _, err := os.Stat(filepath.Join(dir, "assessment.json")); err != nil {
		return "", fmt.Errorf("agent event assessment unavailable: %w", err)
	}
	return filepath.Join(dir, "agent-events.jsonl"), nil
}

func (s Store) AppendAgentEvent(id string, event AgentEvent) error {
	path, err := s.agentEventPath(id)
	if err != nil {
		return err
	}
	event.Time = time.Now().UTC()
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(raw) > maximumAgentEvent {
		return fmt.Errorf("agent event exceeds 8 MiB")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(raw, '\n'))
	return errors.Join(writeErr, file.Close())
}

// AgentEvents uses byte offsets at JSONL record boundaries. Full event bodies
// are separately text-paged so large tool results never inflate the index.
func (s Store) AgentEvents(input QueryInput) (json.RawMessage, error) {
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
		return nil, fmt.Errorf("invalid agent event page")
	}
	path, err := s.agentEventPath(input.AssessmentID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return json.Marshal(map[string]any{"available": false, "reason": "No agent history was recorded for this assessment.", "items": []any{}, "nextOffset": nil})
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if int64(input.Offset) > info.Size() {
		return nil, fmt.Errorf("agent event offset exceeds history length")
	}
	if input.Offset > 0 {
		if _, err := file.Seek(int64(input.Offset)-1, io.SeekStart); err != nil {
			return nil, err
		}
		var before [1]byte
		if _, err := io.ReadFull(file, before[:]); err != nil {
			return nil, err
		}
		if before[0] != '\n' {
			return nil, fmt.Errorf("agent event offset must be a returned record boundary")
		}
	}
	reader := bufio.NewReader(file)
	read := func() ([]byte, error) {
		var raw []byte
		for {
			fragment, err := reader.ReadSlice('\n')
			if len(raw)+len(fragment) > maximumAgentEvent+1 {
				return nil, fmt.Errorf("agent event exceeds 8 MiB")
			}
			raw = append(raw, fragment...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read complete agent event: %w", err)
			}
			return raw, nil
		}
	}
	if input.Operation == "event" {
		raw, err := read()
		if err != nil {
			return nil, err
		}
		chars := []rune(string(raw[:len(raw)-1]))
		if input.TextOffset < 0 || input.TextOffset > len(chars) || input.TextLimit < 0 || input.TextLimit > 24000 {
			return nil, fmt.Errorf("invalid event text page; textLimit must be 1-24000")
		}
		limit := input.TextLimit
		if limit == 0 {
			limit = 6000
		}
		end := min(len(chars), input.TextOffset+limit)
		for {
			var next *int
			if end < len(chars) {
				next = &end
			}
			result := map[string]any{"eventOffset": input.Offset, "text": string(chars[input.TextOffset:end]), "textPage": map[string]any{"offset": input.TextOffset, "nextOffset": next, "total": len(chars)}}
			out, err := json.Marshal(result)
			if err != nil || len(out) <= 24000 {
				return out, err
			}
			end = input.TextOffset + (end-input.TextOffset)/2
		}
	}
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	items := []map[string]any{}
	cursor := input.Offset
	preview := func(text string) string {
		chars := []rune(text)
		return string(chars[:min(250, len(chars))])
	}
	for len(items) < limit && int64(cursor) < info.Size() {
		raw, err := read()
		if err != nil {
			return nil, err
		}
		var event AgentEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("decode agent event at byte %d: %w", cursor, err)
		}
		row := map[string]any{"offset": cursor, "time": event.Time, "agentID": event.AgentID, "parentID": event.ParentID, "kind": event.Kind, "tool": preview(event.Tool), "step": event.Step, "status": event.Status, "messagePreview": preview(event.Message), "errorPreview": preview(event.Error), "argsBytes": len(event.Args), "resultBytes": len(event.Result)}
		candidate, err := json.Marshal(append(items, row))
		if err != nil {
			return nil, err
		}
		if len(candidate) > 23000 && len(items) > 0 {
			break
		}
		items = append(items, row)
		cursor += len(raw)
	}
	var next *int
	if int64(cursor) < info.Size() {
		next = &cursor
	}
	return json.Marshal(map[string]any{"available": true, "items": items, "nextOffset": next, "offsetUnit": "bytes", "totalBytes": info.Size()})
}
