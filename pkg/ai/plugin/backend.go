package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PluginBackend bridges a Plugin into the mcprouter.Backend interface. Tool
// calls are routed to Plugin.HandleMCPTool; tool listings come from
// Plugin.ListTools.
type PluginBackend struct {
	id  string
	mgr *Manager
}

// NewPluginBackend creates a backend for the named plugin. The plugin must
// be registered and enabled before CallTool is called.
func NewPluginBackend(id string, mgr *Manager) (*PluginBackend, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrPluginIDRequired
	}
	if mgr == nil {
		return nil, errors.New("plugin: manager is required")
	}
	return &PluginBackend{id: id, mgr: mgr}, nil
}

// Name returns the plugin ID as the backend name.
func (b *PluginBackend) Name() string { return b.id }

// ListTools returns the plugin's advertised tools. If the plugin is not
// enabled, returns an empty list.
func (b *PluginBackend) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	p := b.mgr.PluginFor(b.id)
	if p == nil {
		return nil, nil
	}
	return p.ListTools(), nil
}

// CallTool routes the call to the plugin's HandleMCPTool.
func (b *PluginBackend) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	p := b.mgr.PluginFor(b.id)
	if p == nil {
		return nil, fmt.Errorf("%w: %s", mcprouter.ErrMissingToolName, b.id)
	}
	resp, err := p.HandleMCPTool(ctx, ToolRequest{Tool: name, Args: args})
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: resp.Content}},
		IsError: resp.IsError,
	}, nil
}
