package mcprouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/ai/mcpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestLocalBackendListAndCall verifies the in-process backend returns
// tools and dispatches calls through the provided CallFunc.
func TestLocalBackendListAndCall(t *testing.T) {
	t.Parallel()
	tools := []*mcp.Tool{
		{Name: "echo", Description: "echo tool"},
		{Name: "ping", Description: "ping tool"},
	}
	called := ""
	b, err := NewLocalBackend("local", tools, func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		called = name
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "called " + name}},
		}, nil
	})
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	if b.Name() != "local" {
		t.Fatalf("name = %q", b.Name())
	}
	list, err := b.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Name != "echo" {
		t.Fatalf("list = %+v", list)
	}
	res, err := b.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if called != "echo" {
		t.Fatalf("called = %q", called)
	}
	if extractText(res) != "called echo" {
		t.Fatalf("text = %q", extractText(res))
	}
}

// TestLocalBackendRejectsEmptyName verifies the input guard.
func TestLocalBackendRejectsEmptyName(t *testing.T) {
	t.Parallel()
	if _, err := NewLocalBackend("  ", nil, func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		return nil, nil
	}); err != ErrMissingBackendName {
		t.Fatalf("err = %v, want %v", err, ErrMissingBackendName)
	}
}

// TestLocalBackendRejectsNilCallFunc verifies the input guard.
func TestLocalBackendRejectsNilCallFunc(t *testing.T) {
	t.Parallel()
	if _, err := NewLocalBackend("x", nil, nil); err != ErrNilCallFunc {
		t.Fatalf("err = %v, want %v", err, ErrNilCallFunc)
	}
}

// TestRemoteBackendEndToEnd starts a mock MCP server, connects via
// mcpclient, wraps in RemoteBackend, and verifies ListTools/CallTool
// forward correctly.
func TestRemoteBackendEndToEnd(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "remote-mock", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "fetch"}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "fetched"}},
		}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, httpSrv.URL, mcpclient.DialOptions{DisableStandaloneSSE: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	b, err := NewRemoteBackend("remote", client)
	if err != nil {
		t.Fatalf("new remote: %v", err)
	}
	list, err := b.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "fetch" {
		t.Fatalf("list = %+v", list)
	}
	res, err := b.CallTool(ctx, "fetch", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if text := extractText(res); !strings.Contains(text, "fetched") {
		t.Fatalf("text = %q", text)
	}
}

// TestRouterWithLocalAndRemoteBackends verifies the router correctly
// aggregates a LocalBackend and a RemoteBackend simultaneously.
func TestRouterWithLocalAndRemoteBackends(t *testing.T) {
	t.Parallel()
	// Remote side.
	server := mcp.NewServer(&mcp.Implementation{Name: "r", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "remoteTool"}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "remote-ok"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, httpSrv.URL, mcpclient.DialOptions{DisableStandaloneSSE: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	r := New()
	local, err := NewLocalBackend("local", []*mcp.Tool{{Name: "localTool"}}, func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "local-ok"}}}, nil
	})
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	if err := r.AddBackend(local); err != nil {
		t.Fatalf("add local: %v", err)
	}
	remote, err := NewRemoteBackend("remote", client)
	if err != nil {
		t.Fatalf("new remote: %v", err)
	}
	if err := r.AddBackend(remote); err != nil {
		t.Fatalf("add remote: %v", err)
	}

	httpRouter := httptest.NewServer(r.Handler())
	defer httpRouter.Close()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             httpRouter.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect router: %v", err)
	}
	defer c.Close()
	list, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[string]bool{"local.localTool": false, "remote.remoteTool": false}
	for _, tool := range list.Tools {
		want[tool.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing tool %q in %+v", name, list.Tools)
		}
	}
}
