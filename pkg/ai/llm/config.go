// Package llm adapts the bifrost multi-provider gateway to the ai.LLMClient
// interface used by gonacos copilot endpoints.
package llm

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the YAML-loaded LLM configuration. It is intentionally simple:
// a flat list of providers plus the name of the default one.
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
	Default   string           `yaml:"default"`
}

// ProviderConfig describes a single LLM provider. Type maps to a bifrost
// ModelProvider (e.g. "openai", "anthropic", "ollama"). Endpoint is the
// provider base URL; for OpenAI-compatible providers this is the chat
// completions base URL.
type ProviderConfig struct {
	Name        string  `yaml:"name"`
	Type        string  `yaml:"type"`
	APIKey      string  `yaml:"apiKey"`
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"maxTokens"`
}

// LoadConfig reads a YAML config file from path. Environment variable
// references in the form ${VAR} in APIKey and Endpoint are expanded.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read llm config: %w", err)
	}
	return ParseConfig(data)
}

// ParseConfig unmarshals YAML bytes into a Config, applies defaults, and
// expands environment variables.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse llm config: %w", err)
	}
	if len(cfg.Providers) == 0 {
		return nil, ErrNoProviders
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Type = strings.TrimSpace(p.Type)
		p.Endpoint = strings.TrimSpace(p.Endpoint)
		p.Model = strings.TrimSpace(p.Model)
		if p.Name == "" {
			return nil, fmt.Errorf("%w: provider at index %d missing name", ErrInvalidConfig, i)
		}
		if p.Type == "" {
			p.Type = "openai"
		}
		if p.Model == "" {
			return nil, fmt.Errorf("%w: provider %q missing model", ErrInvalidConfig, p.Name)
		}
		p.APIKey = expandEnv(p.APIKey)
		p.Endpoint = expandEnv(p.Endpoint)
	}
	if cfg.Default == "" {
		cfg.Default = cfg.Providers[0].Name
	}
	if _, idx := cfg.findProvider(cfg.Default); idx < 0 {
		return nil, fmt.Errorf("%w: default provider %q not found", ErrInvalidConfig, cfg.Default)
	}
	return &cfg, nil
}

func (c *Config) findProvider(name string) (*ProviderConfig, int) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], i
		}
	}
	return nil, -1
}

// expandEnv replaces ${VAR} patterns with the value of the matching
// environment variable. Variables that are unset are left as-is so that
// misconfigurations surface clearly at call time.
func expandEnv(s string) string {
	if s == "" || !strings.Contains(s, "${") {
		return s
	}
	return os.Expand(s, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return "${" + name + "}"
	})
}
