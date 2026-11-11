package intelligence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type Provider struct {
	Config      Config
	RequireTool bool
}

// Complete assembles indexed tool-call fragments while forwarding only user-facing
// text. The caller owns cancellation; redirects cannot escape the configured endpoint.
func (p Provider) Complete(ctx context.Context, messages []Message, tools []Tool, emit func(string) error) (Message, error) {
	result := Message{Role: "assistant"}
	if err := p.Config.Validate(); err != nil {
		return result, err
	}
	if !p.Config.Enabled {
		return result, fmt.Errorf("review agent is disabled")
	}
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
		if p.RequireTool {
			choice = "required"
		}
	}
	body, err := json.Marshal(struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Tools       []Tool    `json:"tools,omitempty"`
		Stream      bool      `json:"stream"`
		MaxTokens   int       `json:"max_tokens"`
		Temperature float64   `json:"temperature"`
		ToolChoice  string    `json:"tool_choice,omitempty"`
	}{p.Config.Model, messages, tools, true, 8192, .2, choice})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.Config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.Config.APIKey)
	}
	client := http.Client{Timeout: 180 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("provider redirects are not allowed") }}
	response, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("model request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, readErr := io.ReadAll(io.LimitReader(response.Body, 8192))
		if readErr != nil {
			return result, fmt.Errorf("model HTTP %d: read error: %w", response.StatusCode, readErr)
		}
		return result, fmt.Errorf("model HTTP %d: %s", response.StatusCode, p.redact(strings.TrimSpace(string(detail))))
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 2<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	calls := map[int]*ToolCall{}
	finished := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int      `json:"index"`
						ID       string   `json:"id"`
						Type     string   `json:"type"`
						Function Function `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return result, fmt.Errorf("decode model stream: %w", err)
		}
		if event.Error != nil {
			return result, fmt.Errorf("model stream: %s", p.redact(event.Error.Message))
		}

		if len(event.Choices) == 0 {
			continue
		}
		choice := event.Choices[0]
		if choice.Finish != "" {
			if choice.Finish != "stop" && choice.Finish != "tool_calls" {
				return result, fmt.Errorf("model response incomplete: %s", choice.Finish)
			}
			finished = true
		}
		if choice.Delta.Content != "" {
			result.Content += choice.Delta.Content
			if emit != nil {
				if err := emit(choice.Delta.Content); err != nil {
					return result, err
				}
			}
		}
		for _, part := range choice.Delta.ToolCalls {
			if part.Index < 0 || part.Index > 31 {
				return result, fmt.Errorf("invalid tool call index %d", part.Index)
			}
			call := calls[part.Index]
			if call == nil {
				call = &ToolCall{Type: "function"}
				calls[part.Index] = call
			}
			call.ID += part.ID
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read model stream: %w", err)
	}
	if !finished {
		return result, fmt.Errorf("model stream ended without a complete response")
	}
	indices := make([]int, 0, len(calls))
	for index := range calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := calls[index]
		if call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return result, fmt.Errorf("malformed tool call %d", index)
		}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return result, fmt.Errorf("model returned no text or tool calls")
	}
	return result, nil
}

func (p Provider) redact(text string) string {
	if p.Config.APIKey != "" {
		return strings.ReplaceAll(text, p.Config.APIKey, "[redacted]")
	}
	return text
}
