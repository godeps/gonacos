package mcprouter

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubBackend is a minimal Backend for testing. It returns a fixed tool
// list and echoes the called tool name back as text.
type stubBackend struct {
	name  string
	tools []*mcp.Tool
}

func (s stubBackend) Name() string { return s.name }
func (s stubBackend) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	out := make([]*mcp.Tool, len(s.tools))
	copy(out, s.tools)
	return out, nil
}
func (s stubBackend) CallTool(_ context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if name == "fail" {
		return nil, errors.New("stub failure")
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: name + " called on " + s.name},
		},
	}, nil
}

// TestRouterAddAndListBackends verifies AddBackend and ListBackends.
func TestRouterAddAndListBackends(t *testing.T) {
	t.Parallel()
	r := New()
	if err := r.AddBackend(stubBackend{name: "a", tools: []*mcp.Tool{{Name: "echo"}}}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := r.AddBackend(stubBackend{name: "b", tools: []*mcp.Tool{{Name: "ping"}}}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	names := r.ListBackends()
	if len(names) != 2 {
		t.Fatalf("backends = %v, want 2", names)
	}
}

// TestRouterRejectsEmptyBackendName verifies the input guard.
func TestRouterRejectsEmptyBackendName(t *testing.T) {
	t.Parallel()
	r := New()
	if err := r.AddBackend(stubBackend{name: "  "}); err != ErrMissingBackendName {
		t.Fatalf("err = %v, want %v", err, ErrMissingBackendName)
	}
}

// TestRouterRemoveBackend verifies removal.
func TestRouterRemoveBackend(t *testing.T) {
	t.Parallel()
	r := New()
	_ = r.AddBackend(stubBackend{name: "a", tools: []*mcp.Tool{{Name: "echo"}}})
	if err := r.RemoveBackend("a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := r.RemoveBackend("ghost"); err != ErrBackendNotFound {
		t.Fatalf("err = %v, want %v", err, ErrBackendNotFound)
	}
}

// TestRouterEndToEnd starts a real streamable HTTP server backed by the
// router, connects with mcpclient, and verifies that tools from multiple
// backends are aggregated under their prefixed names and callable.
func TestRouterEndToEnd(t *testing.T) {
	t.Parallel()
	r := New()
	if err := r.AddBackend(stubBackend{
		name:  "alpha",
		tools: []*mcp.Tool{{Name: "echo", Description: "echo on alpha"}},
	}); err != nil {
		t.Fatalf("add alpha: %v", err)
	}
	if err := r.AddBackend(stubBackend{
		name:  "beta",
		tools: []*mcp.Tool{{Name: "ping", Description: "ping on beta"}},
	}); err != nil {
		t.Fatalf("add beta: %v", err)
	}
	server := httptest.NewServer(r.Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	list, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{"alpha.echo": false, "beta.ping": false}
	for _, tool := range list.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("tool %q not in list: %+v", name, list.Tools)
		}
	}

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "alpha.echo",
		Arguments: map[string]any{"msg": "hi"},
	})
	if err != nil {
		t.Fatalf("call alpha.echo: %v", err)
	}
	text := extractText(res)
	if !strings.Contains(text, "echo called on alpha") {
		t.Fatalf("text = %q", text)
	}
}

// extractText returns the concatenated text content of a CallToolResult.
func extractText(result *mcp.CallToolResult) string {
	var b strings.Builder
	if result == nil {
		return ""
	}
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
