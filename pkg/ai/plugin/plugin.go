// Package plugin defines a minimal plugin interface and a Manager that
// bridges plugins into the mcprouter as Backends.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Meta describes a plugin's identity.
type Meta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// Config is the per-plugin configuration map.
type Config map[string]string

// ToolRequest is what the router hands to a plugin when a tool is called.
type ToolRequest struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// ToolResponse is what the plugin hands back. Content is rendered as
// TextContent; IsError marks the result as an error.
type ToolResponse struct {
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// Plugin is the interface plugins implement. The Manager owns lifecycle
// (Init/Start/Stop) and routes tool calls via HandleMCPTool.
type Plugin interface {
	Meta() Meta
	Init(cfg Config) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	HandleMCPTool(ctx context.Context, req ToolRequest) (ToolResponse, error)
	ListTools() []*mcp.Tool
}

// Manager owns the plugin registry and runtime state.
type Manager struct {
	mu      sync.RWMutex
	plugins map[string]*entry
}

type entry struct {
	plugin  Plugin
	config  Config
	enabled bool
	started bool
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{plugins: map[string]*entry{}}
}

// Register adds a plugin to the registry. The plugin is not started until
// Enable is called.
func (m *Manager) Register(p Plugin, cfg Config) error {
	if p == nil {
		return ErrPluginRequired
	}
	meta := p.Meta()
	if meta.ID == "" {
		return ErrPluginIDRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.plugins[meta.ID]; ok {
		return fmt.Errorf("%w: %s", ErrPluginExists, meta.ID)
	}
	if err := p.Init(cfg); err != nil {
		return fmt.Errorf("init plugin %s: %w", meta.ID, err)
	}
	m.plugins[meta.ID] = &entry{plugin: p, config: cfg, enabled: false}
	return nil
}

// Enable starts a plugin and marks it as enabled.
func (m *Manager) Enable(ctx context.Context, id string) error {
	e, err := m.entry(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.enabled {
		return nil
	}
	if !e.started {
		if err := e.plugin.Start(ctx); err != nil {
			return fmt.Errorf("start plugin %s: %w", id, err)
		}
		e.started = true
	}
	e.enabled = true
	return nil
}

// Disable marks a plugin as disabled but does not stop it. The plugin's
// tools will no longer be advertised.
func (m *Manager) Disable(id string) error {
	e, err := m.entry(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.enabled = false
	return nil
}

// Unregister removes a plugin. The plugin is stopped first.
func (m *Manager) Unregister(ctx context.Context, id string) error {
	m.mu.Lock()
	e, ok := m.plugins[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	if e.started {
		_ = e.plugin.Stop(ctx)
	}
	m.mu.Lock()
	delete(m.plugins, id)
	m.mu.Unlock()
	return nil
}

// List returns metadata for all registered plugins, including enabled state.
func (m *Manager) List() []PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PluginInfo, 0, len(m.plugins))
	for _, e := range m.plugins {
		meta := e.plugin.Meta()
		out = append(out, PluginInfo{
			Meta:    meta,
			Enabled: e.enabled,
		})
	}
	return out
}

// Get returns the plugin metadata + enabled state by ID.
func (m *Manager) Get(id string) (PluginInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.plugins[id]
	if !ok {
		return PluginInfo{}, fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	return PluginInfo{Meta: e.plugin.Meta(), Enabled: e.enabled}, nil
}

// IsEnabled returns true if the plugin exists and is enabled.
func (m *Manager) IsEnabled(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.plugins[id]
	return ok && e.enabled
}

// PluginFor returns the underlying Plugin if enabled, or nil.
func (m *Manager) PluginFor(id string) Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.plugins[id]
	if !ok || !e.enabled {
		return nil
	}
	return e.plugin
}

// StopAll stops every running plugin. Used during graceful shutdown.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.RLock()
	entries := make([]*entry, 0, len(m.plugins))
	for _, e := range m.plugins {
		entries = append(entries, e)
	}
	m.mu.RUnlock()
	for _, e := range entries {
		if e.started {
			_ = e.plugin.Stop(ctx)
			e.started = false
			e.enabled = false
		}
	}
}

func (m *Manager) entry(id string) (*entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.plugins[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	return e, nil
}

// PluginInfo pairs a plugin's metadata with its enabled state.
type PluginInfo struct {
	Meta    Meta `json:"meta"`
	Enabled bool `json:"enabled"`
}

// SetConfig updates a plugin's config and re-inits it. The plugin is stopped
// before re-init and must be re-enabled to restart.
func (m *Manager) SetConfig(id string, cfg Config) error {
	e, err := m.entry(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.started {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.plugin.Stop(ctx)
		e.started = false
		e.enabled = false
	}
	if err := e.plugin.Init(cfg); err != nil {
		return fmt.Errorf("re-init plugin %s: %w", id, err)
	}
	e.config = cfg
	return nil
}

var (
	// ErrPluginRequired is returned when a nil plugin is registered.
	ErrPluginRequired = errors.New("plugin: plugin is required")
	// ErrPluginIDRequired is returned when the plugin ID is empty.
	ErrPluginIDRequired = errors.New("plugin: id is required")
	// ErrPluginExists is returned when a plugin with the same ID is registered.
	ErrPluginExists = errors.New("plugin: already registered")
	// ErrPluginNotFound is returned when a plugin is missing.
	ErrPluginNotFound = errors.New("plugin: not found")
)
