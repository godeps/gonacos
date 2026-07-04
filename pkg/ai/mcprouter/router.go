// Package mcprouter aggregates multiple MCP backends behind a single
// streamable HTTP endpoint. It is the gonacos equivalent of Nacos Java's
// McpServerRouter: tools from every registered backend are exposed under
// a `backendName.toolName` prefix, and tools/call is dispatched by
// parsing that prefix.
package mcprouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Backend is the contract a backend must implement to be mounted on a
// Router. Callers may implement this directly or use one of the provided
// concrete backends (LocalBackend, RemoteBackend, etc.).
type Backend interface {
	// Name returns the backend's unique identifier. It is used as the
	// tool name prefix and must be stable across calls.
	Name() string
	// ListTools returns the tools this backend exposes. The tools are
	// re-prefixed with Name() by the Router before being advertised.
	ListTools(ctx context.Context) ([]*mcp.Tool, error)
	// CallTool invokes the named tool (without the backend prefix) with
	// the given arguments.
	CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)
}

// Router is a multi-backend MCP server. It owns a single *mcp.Server and
// re-registers tools whenever backends are added or removed.
type Router struct {
	mu       sync.RWMutex
	backends map[string]Backend
	server   *mcp.Server
	handler  http.Handler
}

// New creates an empty Router. Call AddBackend to mount backends, then
// Handler to obtain an http.Handler for a streamable HTTP route.
func New() *Router {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "gonacos-mcp-router",
		Version: "v1.0.0",
	}, &mcp.ServerOptions{
		Instructions: "gonacos MCP router: aggregated tools from multiple backends",
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
	return &Router{
		backends: map[string]Backend{},
		server:   server,
		handler:  handler,
	}
}

// AddBackend mounts a backend. If a backend with the same name exists,
// the old one is replaced and its tools are removed first. Adding a
// backend triggers a tool refresh on the underlying server.
func (r *Router) AddBackend(b Backend) error {
	if r == nil {
		return ErrNotInitialized
	}
	name := strings.TrimSpace(b.Name())
	if name == "" {
		return ErrMissingBackendName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.backends[name]; ok {
		r.removeBackendTools(existing)
	}
	r.backends[name] = b
	return r.registerBackendTools(b)
}

// RemoveBackend unmounts a backend by name.
func (r *Router) RemoveBackend(name string) error {
	if r == nil {
		return ErrNotInitialized
	}
	name = strings.TrimSpace(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.backends[name]
	if !ok {
		return ErrBackendNotFound
	}
	r.removeBackendTools(b)
	delete(r.backends, name)
	return nil
}

// ListBackends returns the names of all mounted backends.
func (r *Router) ListBackends() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.backends))
	for name := range r.backends {
		out = append(out, name)
	}
	return out
}

// Handler returns the http.Handler that serves the streamable HTTP MCP
// protocol. Mount it on a route like /v3/ai/mcp/router.
func (r *Router) Handler() http.Handler {
	if r == nil {
		return nil
	}
	return r.handler
}

// registerBackendTools re-registers every tool from the backend with a
// `backendName.toolName` prefix. The caller must hold r.mu.
func (r *Router) registerBackendTools(b Backend) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := b.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("list tools from %q: %w", b.Name(), err)
	}
	for _, tool := range tools {
		t := *tool
		t.Name = prefixedName(b.Name(), tool.Name)
		if !hasObjectSchema(t.InputSchema) {
			t.InputSchema = defaultObjectSchema
		}
		toolName := tool.Name
		r.server.AddTool(&t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, fmt.Errorf("parse arguments: %w", err)
				}
			}
			return b.CallTool(ctx, toolName, args)
		})
	}
	return nil
}

// removeBackendTools removes all tools that were registered for the
// backend. The caller must hold r.mu.
func (r *Router) removeBackendTools(b Backend) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := b.ListTools(ctx)
	if err != nil {
		// Best-effort removal: log and continue.
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, prefixedName(b.Name(), tool.Name))
	}
	if len(names) > 0 {
		r.server.RemoveTools(names...)
	}
	return nil
}

// prefixedName joins backend name and tool name with a dot.
func prefixedName(backendName, toolName string) string {
	return backendName + "." + toolName
}

// hasObjectSchema returns true if v looks like a JSON-schema object with
// at least a "type" field. This is the minimum go-sdk requires to avoid
// panicking in AddTool.
func hasObjectSchema(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, hasType := m["type"]
	return hasType
}

var defaultObjectSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{},
}

var (
	// ErrNotInitialized is returned when a method is called on a nil Router.
	ErrNotInitialized = errors.New("mcprouter: router not initialized")
	// ErrMissingBackendName is returned when a backend has an empty Name().
	ErrMissingBackendName = errors.New("mcprouter: backend name is required")
	// ErrBackendNotFound is returned by RemoveBackend when the name is unknown.
	ErrBackendNotFound = errors.New("mcprouter: backend not found")
)
