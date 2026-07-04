package ai

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/ai/apitomcp"
	"github.com/higress-group/openapi-to-mcpserver/pkg/models"
)

// ApitomcpConfig is a named, stored REST-API-to-MCP YAML configuration.
// The Name is derived from the YAML's server.name field at creation time.
type ApitomcpConfig struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	YAML        string    `json:"yaml"`
	ServerName  string    `json:"serverName,omitempty"`
	ToolCount   int       `json:"toolCount,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

// apitomcpStore owns the in-memory apitomcp config registry.
type apitomcpStore struct {
	mu      sync.RWMutex
	configs map[string]*ApitomcpConfig
}

func newApitomcpStore() *apitomcpStore {
	return &apitomcpStore{configs: map[string]*ApitomcpConfig{}}
}

func (s *apitomcpStore) get(name string) (*ApitomcpConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.configs[name]
	return cfg, ok
}

func (s *apitomcpStore) list() []*ApitomcpConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ApitomcpConfig, 0, len(s.configs))
	for _, cfg := range s.configs {
		out = append(out, cfg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *apitomcpStore) put(cfg *ApitomcpConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cfg.Name] = cfg
}

func (s *apitomcpStore) delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.configs[name]; !ok {
		return false
	}
	delete(s.configs, name)
	return true
}

func (s *apitomcpStore) replace(list []ApitomcpConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs = map[string]*ApitomcpConfig{}
	for i := range list {
		c := list[i]
		s.configs[c.Name] = &c
	}
}

// ValidateApitomcpYAML parses YAML and returns the server name + tool count
// without persisting anything. Useful for pre-flight validation.
func (s *Service) ValidateApitomcpYAML(yaml string) (serverName string, toolCount int, err error) {
	conv := apitomcp.NewConverter()
	cfg, err := conv.LoadYAML([]byte(yaml))
	if err != nil {
		return "", 0, err
	}
	return cfg.Server.Name, len(cfg.Tools), nil
}

// ListApitomcpConfigs returns all stored apitomcp configs, sorted by name.
func (s *Service) ListApitomcpConfigs() []*ApitomcpConfig {
	if s == nil || s.apitomcp == nil {
		return nil
	}
	return s.apitomcp.list()
}

// GetApitomcpConfig returns a single config by name.
func (s *Service) GetApitomcpConfig(name string) (*ApitomcpConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrApitomcpConfigNameRequired
	}
	cfg, ok := s.apitomcp.get(name)
	if !ok {
		return nil, ErrApitomcpConfigNotFound
	}
	return cfg, nil
}

// CreateApitomcpConfig stores a new config and mounts it on the MCP router
// (if one is attached). The name is derived from the YAML's server.name.
func (s *Service) CreateApitomcpConfig(yaml, description string) (*ApitomcpConfig, error) {
	if strings.TrimSpace(yaml) == "" {
		return nil, ErrApitomcpYAMLRequired
	}
	conv := apitomcp.NewConverter()
	parsed, err := conv.LoadYAML([]byte(yaml))
	if err != nil {
		return nil, fmt.Errorf("invalid apitomcp yaml: %w", err)
	}
	name := parsed.Server.Name
	if _, ok := s.apitomcp.get(name); ok {
		return nil, fmt.Errorf("%w: %s", ErrApitomcpConfigExists, name)
	}
	now := time.Now()
	cfg := &ApitomcpConfig{
		Name:        name,
		Description: strings.TrimSpace(description),
		YAML:        yaml,
		ServerName:  name,
		ToolCount:   len(parsed.Tools),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.apitomcp.put(cfg)
	s.mountApitomcpBackend(parsed)
	return cfg, nil
}

// UpdateApitomcpConfig replaces an existing config's YAML and description,
// then remounts the backend.
func (s *Service) UpdateApitomcpConfig(name, yaml, description string) (*ApitomcpConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrApitomcpConfigNameRequired
	}
	if strings.TrimSpace(yaml) == "" {
		return nil, ErrApitomcpYAMLRequired
	}
	existing, ok := s.apitomcp.get(name)
	if !ok {
		return nil, ErrApitomcpConfigNotFound
	}
	conv := apitomcp.NewConverter()
	parsed, err := conv.LoadYAML([]byte(yaml))
	if err != nil {
		return nil, fmt.Errorf("invalid apitomcp yaml: %w", err)
	}
	if parsed.Server.Name != name {
		return nil, fmt.Errorf("%w: yaml server.name (%q) must match config name (%q)",
			ErrApitomcpNameMismatch, parsed.Server.Name, name)
	}
	updated := &ApitomcpConfig{
		Name:        name,
		Description: strings.TrimSpace(description),
		YAML:        yaml,
		ServerName:  name,
		ToolCount:   len(parsed.Tools),
		CreatedAt:   existing.CreatedAt,
		UpdatedAt:   time.Now(),
	}
	s.apitomcp.put(updated)
	if s.router != nil {
		_ = s.router.RemoveBackend(name)
	}
	s.mountApitomcpBackend(parsed)
	return updated, nil
}

// DeleteApitomcpConfig removes a config and unmounts its backend.
func (s *Service) DeleteApitomcpConfig(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrApitomcpConfigNameRequired
	}
	if !s.apitomcp.delete(name) {
		return ErrApitomcpConfigNotFound
	}
	if s.router != nil {
		_ = s.router.RemoveBackend(name)
	}
	return nil
}

// ApitomcpBackendFor returns a temporary Backend for the named config,
// useful for testing tools/list and tools/call without mounting on the
// router. Returns ErrApitomcpConfigNotFound if the config doesn't exist.
func (s *Service) ApitomcpBackendFor(name string) (*apitomcp.ApiToMcpBackend, error) {
	cfg, err := s.GetApitomcpConfig(name)
	if err != nil {
		return nil, err
	}
	conv := apitomcp.NewConverter()
	parsed, err := conv.LoadYAML([]byte(cfg.YAML))
	if err != nil {
		return nil, fmt.Errorf("stored yaml is invalid: %w", err)
	}
	backend, err := conv.ToBackend(parsed, nil)
	if err != nil {
		return nil, err
	}
	if b, ok := backend.(*apitomcp.ApiToMcpBackend); ok {
		return b, nil
	}
	return nil, fmt.Errorf("unexpected backend type %T", backend)
}

// mountApitomcpBackend converts the parsed config to a Backend and mounts it
// on the router (if one is attached).
func (s *Service) mountApitomcpBackend(cfg *models.MCPConfig) {
	if s.router == nil {
		return
	}
	conv := apitomcp.NewConverter()
	backend, err := conv.ToBackend(cfg, nil)
	if err != nil {
		return
	}
	_ = s.router.AddBackend(backend)
}

var (
	// ErrApitomcpConfigNotFound is returned when a config is missing.
	ErrApitomcpConfigNotFound = errors.New("apitomcp: config not found")
	// ErrApitomcpConfigExists is returned when a config with the same name exists.
	ErrApitomcpConfigExists = errors.New("apitomcp: config already exists")
	// ErrApitomcpConfigNameRequired is returned when the config name is empty.
	ErrApitomcpConfigNameRequired = errors.New("apitomcp: config name is required")
	// ErrApitomcpYAMLRequired is returned when the YAML content is empty.
	ErrApitomcpYAMLRequired = errors.New("apitomcp: yaml content is required")
	// ErrApitomcpNameMismatch is returned when the YAML server.name doesn't match the stored name.
	ErrApitomcpNameMismatch = errors.New("apitomcp: yaml server.name must match config name")
)
