package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Profile describes API observations, not certification of semantic capability
// or the publisher/backbone provenance of an opaque configured endpoint.
type EmbeddingProfile struct {
	Model         string           `json:"model"`
	ReturnedModel string           `json:"returnedModel,omitempty"`
	Revision      string           `json:"revision,omitempty"`
	Generation    string           `json:"generation,omitempty"`
	Dimensions    int              `json:"dimensions"`
	MetadataState string           `json:"metadataState"`
	IdentityState string           `json:"identityState"`
	CodeTextState string           `json:"codeTextState"`
	CodeCodeState string           `json:"codeCodeState"`
	Limitations   []string         `json:"limitations"`
	Trials        []EmbeddingTrial `json:"trials,omitempty"`
}

// EmbeddingTrial records a small synthetic retrieval observation, not a model
// capability certification. Ties include the target itself.
type EmbeddingTrial struct {
	Direction      string `json:"direction"`
	Representation string `json:"representation"`
	Case           string `json:"case"`
	Rank           int    `json:"rank"`
	Ties           int    `json:"ties"`
	Candidates     int    `json:"candidates"`
}

type EmbeddingProvider struct {
	Config EmbeddingConfig
}

func (p EmbeddingProvider) request(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	if err := p.Config.Validate(); err != nil {
		return nil, 0, err
	}
	if !p.Config.Enabled {
		return nil, 0, fmt.Errorf("embeddings are disabled")
	}
	var input []byte
	var err error
	if body != nil {
		input, err = json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encode embedding request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.Config.BaseURL, "/")+path, bytes.NewReader(input))
	if err != nil {
		return nil, 0, fmt.Errorf("embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.Config.APIKey)
	}
	client := http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("embedding redirects are not allowed")
	}}
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("embedding endpoint: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > 32<<20 {
		return nil, response.StatusCode, fmt.Errorf("embedding response exceeds 32 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := strings.TrimSpace(string(data[:min(len(data), 8192)]))
		detail = (Provider{Config: Config{APIKey: p.Config.APIKey}}).redact(detail)
		return nil, response.StatusCode, fmt.Errorf("embedding HTTP %d: %s", response.StatusCode, detail)
	}
	return data, response.StatusCode, nil
}

func (p EmbeddingProvider) Discover(ctx context.Context) (EmbeddingProfile, error) {
	profile := EmbeddingProfile{Model: p.Config.Model, Revision: p.Config.Revision, Generation: p.Config.Generation,
		MetadataState: "available", IdentityState: "unverified", CodeTextState: "unknown", CodeCodeState: "unknown",
		Limitations: []string{"Retrieval quality and model provenance are unverified; vectors alone do not establish code understanding.", "Application byte budgets are not model token limits."}}
	method, path := http.MethodGet, "/models/"+url.PathEscape(p.Config.Model)
	var body any
	if p.Config.Protocol == "ollama" {
		method, path, body = http.MethodPost, "/api/show", map[string]string{"model": p.Config.Model}
	}
	data, status, err := p.request(ctx, method, path, body)
	if status == 404 || status == 405 || status == 501 {
		profile.MetadataState = "unavailable"
		profile.Limitations = append(profile.Limitations, err.Error())
	} else if err != nil {
		return profile, err
	} else {
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(data, &metadata); err != nil {
			return profile, fmt.Errorf("decode configured model metadata: %w", err)
		}
		// Only explicit fields from this configured resource are accepted. There
		// is no model-name catalog or inferred architecture/capability mapping.
		if profile.Revision == "" {
			for _, field := range []string{"revision", "digest"} {
				if value, ok := metadata[field]; ok {
					if err := json.Unmarshal(value, &profile.Revision); err != nil {
						return profile, fmt.Errorf("configured model %s metadata: %w", field, err)
					}
					if profile.Revision != "" {
						break
					}
				}
			}
		}
	}
	vectors, model, err := p.vectors(ctx, []string{"go-review embedding capability probe"})
	if err != nil {
		return profile, err
	}
	profile.Dimensions, profile.ReturnedModel = len(vectors[0]), model
	if profile.Revision != "" || profile.Generation != "" {
		profile.IdentityState = "revision-or-generation"
	} else {
		profile.Limitations = append(profile.Limitations, "No stable revision/generation; vector reuse is session-local.")
	}
	return profile, nil
}

func (p EmbeddingProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	vectors, _, err := p.vectors(ctx, inputs)
	return vectors, err
}

// EmbedForProfile prevents response-level identity or dimension changes from
// contaminating an established index. Hidden alias changes remain unverifiable.
func (p EmbeddingProvider) EmbedForProfile(ctx context.Context, inputs []string, profile EmbeddingProfile) ([][]float32, error) {
	vectors, model, err := p.vectors(ctx, inputs)
	if err != nil {
		return nil, err
	}
	if len(vectors[0]) != profile.Dimensions || model != profile.ReturnedModel {
		return nil, fmt.Errorf("embedding profile changed: dimensions %d -> %d, returned model %q -> %q; rebuild the index",
			profile.Dimensions, len(vectors[0]), profile.ReturnedModel, model)
	}
	return vectors, nil
}

func (p EmbeddingProvider) vectors(ctx context.Context, inputs []string) ([][]float32, string, error) {
	if len(inputs) == 0 {
		return nil, "", fmt.Errorf("embedding inputs are required")
	}
	total := 0
	for _, input := range inputs {
		if strings.TrimSpace(input) == "" || len(input) > p.Config.InputLimit() {
			return nil, "", fmt.Errorf("embedding input is empty or exceeds %d bytes", p.Config.InputLimit())
		}
		total += len(input)
	}
	if total > p.Config.BatchLimit() {
		return nil, "", fmt.Errorf("embedding batch exceeds %d input bytes", p.Config.BatchLimit())
	}
	path := "/embeddings"
	body := map[string]any{"model": p.Config.Model, "input": inputs}
	if p.Config.Protocol == "ollama" {
		path = "/api/embed"
		body["truncate"] = false
	}
	data, _, err := p.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, "", err
	}
	var response struct {
		Model      string      `json:"model"`
		Embeddings [][]float64 `json:"embeddings"`
		Data       []struct {
			Index     *int      `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, "", fmt.Errorf("decode embedding response: %w", err)
	}
	raw := response.Embeddings
	if p.Config.Protocol == "openai" {
		raw = make([][]float64, len(inputs))
		if len(response.Data) != len(inputs) {
			return nil, "", fmt.Errorf("embedding response cardinality %d != %d", len(response.Data), len(inputs))
		}
		for _, item := range response.Data {
			if item.Index == nil || *item.Index < 0 || *item.Index >= len(inputs) || raw[*item.Index] != nil {
				return nil, "", fmt.Errorf("embedding response has a missing, duplicate or invalid index")
			}
			raw[*item.Index] = item.Embedding
		}
	}
	if len(raw) != len(inputs) {
		return nil, "", fmt.Errorf("embedding response cardinality %d != %d", len(raw), len(inputs))
	}
	out := make([][]float32, len(raw))
	dimension := len(raw[0])
	for i, vector := range raw {
		if dimension == 0 || len(vector) != dimension {
			return nil, "", fmt.Errorf("embedding response contains empty or inconsistent dimensions")
		}
		var norm float64
		for _, value := range vector {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
				return nil, "", fmt.Errorf("embedding response contains an invalid float32 value")
			}
			norm = math.Hypot(norm, value)
		}
		if norm == 0 {
			return nil, "", fmt.Errorf("embedding response contains a zero vector")
		}
		out[i] = make([]float32, dimension)
		for j, value := range vector {
			out[i][j] = float32(value / norm)
		}
	}
	return out, response.Model, nil
}
