package intelligence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EmbeddingConfig is independent of chat configuration; enabling chat never
// authorizes sending source to an embedding endpoint.
type EmbeddingConfig struct {
	Enabled        bool   `json:"enabled"`
	Protocol       string `json:"protocol"`
	BaseURL        string `json:"baseURL"`
	Model          string `json:"model"`
	APIKey         string `json:"apiKey,omitempty"`
	APIKeyEnv      string `json:"apiKeyEnv,omitempty"`
	Revision       string `json:"revision,omitempty"`
	Generation     string `json:"generation,omitempty"`
	QueryPrefix    string `json:"queryPrefix,omitempty"`
	DocumentPrefix string `json:"documentPrefix,omitempty"`
	InputBytes     int    `json:"inputBytes,omitempty"`
	BatchBytes     int    `json:"batchBytes,omitempty"`
}

func EmbeddingConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "go-review", "embeddings.json"), nil
}

func (c EmbeddingConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if err := (Config{Enabled: true, BaseURL: c.BaseURL, Model: c.Model, APIKey: c.APIKey, APIKeyEnv: c.APIKeyEnv}).Validate(); err != nil {
		return fmt.Errorf("embedding config: %w", err)
	}
	if c.Protocol != "openai" && c.Protocol != "ollama" {
		return fmt.Errorf("embedding protocol must be openai or ollama")
	}
	if c.InputBytes != 0 && (c.InputBytes < 256 || c.InputBytes > 1<<20) {
		return fmt.Errorf("embedding inputBytes must be 256-1048576 or omitted")
	}
	if c.BatchBytes != 0 && (c.BatchBytes < 256 || c.BatchBytes > 4<<20) {
		return fmt.Errorf("embedding batchBytes must be 256-4194304 or omitted")
	}
	if c.BatchLimit() < c.InputLimit() || len(c.QueryPrefix) >= c.InputLimit()-128 || len(c.DocumentPrefix) >= c.InputLimit()-128 {
		return fmt.Errorf("embedding batch budget must fit an input and prefixes must leave room for content")
	}
	return nil
}

// These are application byte budgets, not claims about the model's token limit.
func (c EmbeddingConfig) InputLimit() int {
	if c.InputBytes == 0 {
		return 4096
	}
	return c.InputBytes
}

func (c EmbeddingConfig) BatchLimit() int {
	if c.BatchBytes == 0 {
		return 32768
	}
	return c.BatchBytes
}

func LoadEmbeddingConfig(path string) (EmbeddingConfig, error) {
	var c EmbeddingConfig
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read embedding config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("decode embedding config: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("embedding config requires one JSON object")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if c.Enabled && c.APIKeyEnv != "" {
		c.APIKey = os.Getenv(c.APIKeyEnv)
		if c.APIKey == "" {
			return c, fmt.Errorf("embedding API key environment variable %s is empty or unset", c.APIKeyEnv)
		}
		c.APIKeyEnv = ""
		if err := c.Validate(); err != nil {
			return c, err
		}
	}
	return c, nil
}
