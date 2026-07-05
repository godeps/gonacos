package mcprouter

import (
	"context"
	"errors"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/mcpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RemoteBackend wraps an mcpclient.Client connected to a remote MCP
// server. Tools are forwarded verbatim; the Router adds the backend name
// prefix.
type RemoteBackend struct {
	name string
	c    *mcpclient.Client
}

// NewRemoteBackend creates a backend backed by an already-connected
// mcpclient.Client. The caller is responsible for closing the client
// when the backend is removed from the router.
func NewRemoteBackend(name string, c *mcpclient.Client) (*RemoteBackend, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrMissingBackendName
	}
	if c == nil {
		return nil, ErrNilClient
	}
	return &RemoteBackend{name: name, c: c}, nil
}

func (b *RemoteBackend) Name() string { return b.name }

func (b *RemoteBackend) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	if b == nil || b.c == nil {
		return nil, ErrNilClient
	}
	return b.c.ListTools(ctx)
}

func (b *RemoteBackend) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if b == nil || b.c == nil {
		return nil, ErrNilClient
	}
	return b.c.CallTool(ctx, name, args)
}

// CallFunc is the signature of a LocalBackend's tool dispatcher.
type CallFunc func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)

// LocalBackend exposes a fixed set of in-process tools. It is the bridge
// between gonacos's McpServer registry (which stores tools as data) and
// the Router (which expects a Backend).
type LocalBackend struct {
	name   string
	tools  []*mcp.Tool
	callFn CallFunc
}

// NewLocalBackend creates a backend from a static tool list and a call
// function. The call function receives the tool name without the
// backend prefix.
func NewLocalBackend(name string, tools []*mcp.Tool, callFn CallFunc) (*LocalBackend, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrMissingBackendName
	}
	if callFn == nil {
		return nil, ErrNilCallFunc
	}
	cleaned := make([]*mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if t == nil || strings.TrimSpace(t.Name) == "" {
			continue
		}
		copy := *t
		if !hasObjectSchema(copy.InputSchema) {
			copy.InputSchema = defaultObjectSchema
		}
		cleaned = append(cleaned, &copy)
	}
	return &LocalBackend{name: name, tools: cleaned, callFn: callFn}, nil
}

func (b *LocalBackend) Name() string { return b.name }

func (b *LocalBackend) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	if b == nil {
		return nil, ErrNotInitialized
	}
	out := make([]*mcp.Tool, len(b.tools))
	copy(out, b.tools)
	return out, nil
}

func (b *LocalBackend) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if b == nil || b.callFn == nil {
		return nil, ErrNilCallFunc
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrMissingToolName
	}
	return b.callFn(ctx, name, args)
}

var (
	// ErrNilClient is returned when a RemoteBackend is created with a nil client.
	ErrNilClient = errors.New("mcprouter: client is nil")
	// ErrNilCallFunc is returned when a LocalBackend is created with a nil call function.
	ErrNilCallFunc = errors.New("mcprouter: call function is nil")
	// ErrMissingToolName is returned by CallTool when the name is empty.
	ErrMissingToolName = errors.New("mcprouter: tool name is required")
)
