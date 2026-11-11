package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Enabled {
		t.Fatalf("absent config: %+v %v", cfg, err)
	}
	for _, base := range []string{"http://example.com/v1", "file:///tmp", "https://user:pass@example.com/v1", "http://127.0.0.1:1235/v1?x=y"} {
		if err := (Config{Enabled: true, BaseURL: base, Model: "ornith"}).Validate(); err == nil {
			t.Errorf("accepted nonliteral loopback URL %s", base)
		}
	}
	if err := os.WriteFile(path, []byte(`{"enabled":true,"baseURL":"http://127.0.0.1:1235/v1","model":"ornith-1.5-35b-a3b-mlx"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil || cfg.Model != "ornith-1.5-35b-a3b-mlx" {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestAgentConfigRemoteAndCredentials(t *testing.T) {
	for _, base := range []string{"https://example.com/v1", "http://localhost:1235/v1", "http://[::1]:1235/v1"} {
		if err := (Config{Enabled: true, BaseURL: base, Model: "reviewer"}).Validate(); err != nil {
			t.Errorf("valid endpoint %s: %v", base, err)
		}
	}
	t.Setenv("REVIEW_TEST_KEY", "fixture-key")
	file := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(file, []byte(`{"enabled":true,"baseURL":"https://example.com/v1","model":"reviewer","apiKeyEnv":"REVIEW_TEST_KEY"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	// Verify authentication on a controlled loopback endpoint, never send source externally.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("configured key was not attached to provider request")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()
	cfg.BaseURL = server.URL
	if _, err := (Provider{Config: cfg}).Complete(context.Background(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_TEST_KEY", "")
	if _, err := LoadConfig(file); err == nil {
		t.Fatal("missing configured key environment variable was silently ignored")
	}
}

func TestAgentProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "ornith" || !request.Stream || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request %s %+v", r.URL, request)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"review_flow\",\"arguments\":\"{\\\"entry\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"ID\\\":\\\"e\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	p := Provider{Config: Config{Enabled: true, BaseURL: server.URL + "/v1", Model: "ornith"}}
	var text strings.Builder
	msg, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, func(delta string) error {
		text.WriteString(delta)
		return nil
	})
	if err != nil || text.String() != "Hello world" || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"entryID":"e"}` {
		t.Fatalf("msg=%+v text=%s err=%v", msg, text.String(), err)
	}
}

func TestAgentProviderFailures(t *testing.T) {
	for _, body := range []string{
		`data: not-json` + "\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
	} {
		t.Run(body[:12], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			p := Provider{Config: Config{Enabled: true, BaseURL: s.URL, Model: "ornith"}}
			if _, err := p.Complete(context.Background(), nil, nil, nil); err == nil {
				t.Fatal("accepted malformed/truncated response")
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "model unavailable", 503) }))
	defer s.Close()
	p := Provider{Config: Config{Enabled: true, BaseURL: s.URL, Model: "ornith"}}
	if _, err := p.Complete(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("lost provider error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, nil, nil, nil); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestAgentProviderLive(t *testing.T) {
	base := os.Getenv("GO_REVIEW_AGENT_URL")
	if base == "" {
		t.Skip("opt-in local provider")
	}
	p := Provider{Config: Config{Enabled: true, BaseURL: base, Model: os.Getenv("GO_REVIEW_AGENT_MODEL")}}
	ctx, cancel := context.WithTimeout(context.Background(), 180000000000)
	defer cancel()
	messages := []Message{{Role: "user", Content: `Call the echo_probe tool with {"value":"local-probe-42"}, then report its returned value. Do not guess its result.`}}
	tool := Tool{Type: "function", Function: Function{Name: "echo_probe", Description: "Returns a synthetic probe result", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)}}
	first, err := p.Complete(ctx, messages, []Tool{tool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Function.Name != "echo_probe" || !json.Valid([]byte(first.ToolCalls[0].Function.Arguments)) {
		t.Fatalf("no valid tool call: %+v", first)
	}
	messages = append(messages, first, Message{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: `{"value":"verified-local-tool-result-731"}`})
	final, err := p.Complete(ctx, messages, []Tool{tool}, nil)
	if err != nil || !strings.Contains(final.Content, "verified-local-tool-result-731") {
		t.Fatalf("tool continuation %+v %v", final, err)
	}
	t.Logf("verified %s tool call and result continuation", p.Config.Model)
}

func TestAgentProviderRedactsCredentialErrorsAndRejectsRedirects(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		targetCalls := 0
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				return
			}
			http.Error(w, "Rejected fixture-secret", http.StatusUnauthorized)
		}))
		_, err := (Provider{Config: Config{Enabled: true, BaseURL: source.URL, Model: "fixture", APIKey: "fixture-secret"}}).Complete(context.Background(), nil, nil, nil)
		source.Close()
		target.Close()
		if err == nil || strings.Contains(err.Error(), "fixture-secret") || targetCalls != 0 {
			t.Fatalf("redirect=%v targetCalls=%d err=%v", redirect, targetCalls, err)
		}
	}
}
