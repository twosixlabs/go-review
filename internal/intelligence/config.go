// Package intelligence provides optional local-model assistance over retained evidence.
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
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
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
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid agent URL: %w", err)
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("agent baseURL requires an http literal loopback IP URL without credentials, query or fragment")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("agent model is required")
	}
	return nil
}
