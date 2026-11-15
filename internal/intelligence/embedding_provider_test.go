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

func TestEmbeddingResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"reordered", `{"data":[{"index":1,"embedding":[0,4]},{"index":0,"embedding":[3,0]}]}`, false},
		{"missing", `{"data":[{"index":0,"embedding":[1,0]}]}`, true},
		{"duplicate", `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, true},
		{"dimension", `{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[0,1,0]}]}`, true},
		{"zero", `{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[0,1]}]}`, true},
		{"missing-index", `{"data":[{"embedding":[1,0]},{"index":1,"embedding":[0,1]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				var req struct {
					Model string
					Input []string
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Model != "configured" || strings.Join(req.Input, ",") != "a,b" {
					t.Errorf("wrong request %+v", req)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			p := EmbeddingProvider{Config: EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: server.URL + "/v1", Model: "configured"}}
			got, err := p.Embed(context.Background(), []string{"a", "b"})
			if (err != nil) != tc.bad {
				t.Fatalf("vectors=%v error=%v", got, err)
			}
			if !tc.bad && (got[0][0] != 1 || got[0][1] != 0 || got[1][0] != 0 || got[1][1] != 1) {
				t.Fatalf("incorrect normalized ordering %v", got)
			}
		})
	}
}

func TestEmbeddingConfiguredOnlyDiscovery(t *testing.T) {
	for _, protocol := range []string{"openai", "ollama"} {
		for _, dimensions := range []int{3, 7} {
			t.Run(fmt.Sprintf("%s-%d", protocol, dimensions), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/models/configured":
						if protocol != "openai" {
							t.Error("wrong protocol")
						}
						w.WriteHeader(404)
					case "/api/show":
						var req struct{ Model string }
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.Model != "configured" {
							t.Errorf("probed other model %+v", req)
						}
						fmt.Fprint(w, `{"model_info":{}}`)
					case "/embeddings", "/api/embed":
						var req struct {
							Model    string
							Input    []string
							Truncate *bool
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.Model != "configured" {
							t.Errorf("probed other model %+v", req)
						}
						if protocol == "ollama" && (req.Truncate == nil || *req.Truncate) {
							t.Error("truncation not disabled")
						}
						vector := make([]float32, dimensions)
						vector[0] = 1
						if protocol == "openai" {
							_ = json.NewEncoder(w).Encode(map[string]any{"model": "configured", "data": []any{map[string]any{"index": 0, "embedding": vector}}})
						} else {
							_ = json.NewEncoder(w).Encode(map[string]any{"model": "configured", "embeddings": [][]float32{vector}})
						}
					default:
						t.Errorf("unconfigured discovery request %s", r.URL)
						w.WriteHeader(500)
					}
				}))
				defer server.Close()
				profile, err := (EmbeddingProvider{Config: EmbeddingConfig{Enabled: true, Protocol: protocol, BaseURL: server.URL, Model: "configured"}}).Discover(context.Background())
				if err != nil || profile.Dimensions != dimensions || profile.CodeTextState != "unknown" || profile.CodeCodeState != "unknown" {
					t.Fatalf("profile=%+v error=%v", profile, err)
				}
			})
		}
	}
}

func TestEmbeddingErrorsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "rejected fixture-secret")
	}))
	defer server.Close()
	p := EmbeddingProvider{Config: EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: server.URL, Model: "configured", APIKey: "fixture-secret"}}
	if _, err := p.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("lost/leaked diagnostic %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestEmbeddingConfigIsolation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "embedding.json")
	if cfg, err := LoadEmbeddingConfig(file); err != nil || cfg.Enabled {
		t.Fatalf("missing config %+v %v", cfg, err)
	}
	t.Setenv("EMBED_TEST_KEY", "fixture")
	if err := os.WriteFile(file, []byte(`{"enabled":true,"protocol":"openai","baseURL":"http://127.0.0.1:1235/v1","model":"configured","apiKeyEnv":"EMBED_TEST_KEY","generation":"one"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEmbeddingConfig(file)
	if err != nil || cfg.APIKey != "fixture" || cfg.Generation != "one" {
		t.Fatalf("config %+v %v", cfg, err)
	}
	t.Setenv("EMBED_TEST_KEY", "")
	if _, err = LoadEmbeddingConfig(file); err == nil {
		t.Fatal("ignored missing credential")
	}
}
