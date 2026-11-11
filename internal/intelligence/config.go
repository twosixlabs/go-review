// Package intelligence provides optional model assistance over retained evidence.
package intelligence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Enabled      bool   `json:"enabled"`
	BaseURL      string `json:"baseURL"`
	Model        string `json:"model"`
	APIKey       string `json:"apiKey,omitempty"`
	APIKeyEnv    string `json:"apiKeyEnv,omitempty"`
	MaxSteps     int    `json:"maxSteps,omitempty"`
	ContextBytes int    `json:"contextBytes,omitempty"`
}

func (c Config) StepLimit() int {
	if c.MaxSteps > 0 {
		return c.MaxSteps
	}
	return 64
}

func (c Config) ContextLimit() int {
	if c.ContextBytes > 0 {
		return c.ContextBytes
	}
	return 256 << 10
}

func (c Config) External() bool {
	u, err := url.Parse(c.BaseURL)
	return err == nil && !loopbackHost(u.Hostname())
}

func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
}

func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "go-review", "agent.json"), nil
}

// Missing configuration deliberately disables AI without affecting source browsing.
func LoadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read agent config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode agent config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Enabled && cfg.APIKeyEnv != "" {
		cfg.APIKey = os.Getenv(cfg.APIKeyEnv)
		if strings.TrimSpace(cfg.APIKey) == "" {
			return Config{}, fmt.Errorf("agent API key environment variable %s is empty or unset", cfg.APIKeyEnv)
		}
		cfg.APIKeyEnv = ""
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid agent URL: %w", err)
	}
	if u.Hostname() == "" || u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())) ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("agent baseURL requires HTTPS, or HTTP loopback, without credentials, query or fragment")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("agent model is required")
	}
	if c.APIKey != "" && c.APIKeyEnv != "" {
		return fmt.Errorf("configure apiKey or apiKeyEnv, not both")
	}
	if strings.ContainsAny(c.APIKey, "\r\n") {
		return fmt.Errorf("agent API key contains a newline")
	}
	if c.MaxSteps < 0 || c.MaxSteps > 256 {
		return fmt.Errorf("maxSteps must be 1-256 or omitted")
	}
	if c.ContextBytes != 0 && (c.ContextBytes < 32768 || c.ContextBytes > 2<<20) {
		return fmt.Errorf("contextBytes must be 32768-2097152 or omitted")
	}
	return nil
}
