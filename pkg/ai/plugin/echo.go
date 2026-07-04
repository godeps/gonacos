package plugin

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EchoPlugin is a built-in test plugin that echoes its arguments. It
// advertises a single "echo" tool.
type EchoPlugin struct {
	prefix string
}

// NewEchoPlugin creates an EchoPlugin with the given prefix (defaults to
// "echo" if empty).
func NewEchoPlugin(prefix string) *EchoPlugin {
	if prefix == "" {
		prefix = "echo"
	}
	return &EchoPlugin{prefix: prefix}
}

// Meta returns the plugin metadata.
func (p *EchoPlugin) Meta() Meta {
	return Meta{
		ID:          "echo",
		Name:        "Echo Plugin",
		Description: "A test plugin that echoes arguments",
		Version:     "0.1.0",
	}
}

// Init accepts a "prefix" config key to override the echo prefix.
func (p *EchoPlugin) Init(cfg Config) error {
	if v, ok := cfg["prefix"]; ok && v != "" {
		p.prefix = v
	}
	return nil
}

// Start is a no-op.
func (p *EchoPlugin) Start(_ context.Context) error { return nil }

// Stop is a no-op.
func (p *EchoPlugin) Stop(_ context.Context) error { return nil }

// ListTools returns the "echo" tool.
func (p *EchoPlugin) ListTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "echo",
			Description: "Echo the provided message",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "The message to echo back",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

// HandleMCPTool echoes the "message" arg.
func (p *EchoPlugin) HandleMCPTool(_ context.Context, req ToolRequest) (ToolResponse, error) {
	msg, _ := req.Args["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("%v", req.Args)
	}
	return ToolResponse{Content: fmt.Sprintf("%s: %s", p.prefix, msg)}, nil
}
