package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/mcpclient"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	authsvc "github.com/godeps/gonacos/pkg/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// routerTestHandler builds a NewHandlerWithServices-backed handler with a
// router that has one stub backend mounted. The stub exposes a single
// "echo" tool that returns the message it received. Wrapped with admin
// token injection so /v3/admin/ai/ routes work without explicit token.
func routerTestHandler(t *testing.T) (http.Handler, *mcprouter.Router) {
	t.Helper()
	router := mcprouter.New()
	local, err := mcprouter.NewLocalBackend("stub", []*mcp.Tool{
		{Name: "echo", Description: "echo the message"},
	}, func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		msg, _ := args["message"].(string)
		if msg == "" {
			msg = "(empty)"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + msg}},
		}, nil
	})
	if err != nil {
		t.Fatalf("new local backend: %v", err)
	}
	if err := router.AddBackend(local); err != nil {
		t.Fatalf("add backend: %v", err)
	}
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	result, err := bundle.Auth.Login("nacos", "nacos")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	token := result.AccessToken
	bundle.AI = ai.NewService(nil, ai.WithMcpRouter(router))
	h := NewHandlerWithServices("../..", bundle)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(authsvc.AuthorizationHeader) == "" {
			r.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+token)
		}
		h.ServeHTTP(w, r)
	})
	return wrapped, router
}

// TestMcpRouterProxyEndToEnd connects to /v3/ai/mcp/router with mcpclient
// and verifies the stub backend's echo tool is reachable through the
// HTTP layer.
func TestMcpRouterProxyEndToEnd(t *testing.T) {
	t.Parallel()
	handler, _ := routerTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, server.URL+"/v3/ai/mcp/router", mcpclient.DialOptions{
		DisableStandaloneSSE: true,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "stub.echo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stub.echo not in tools: %+v", tools)
	}

	res, err := client.CallTool(ctx, "stub.echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	text, err := mcpclient.ExtractText(res)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if text != "echo: hi" {
		t.Fatalf("text = %q, want %q", text, "echo: hi")
	}
}

// TestMcpRouterListBackends verifies the admin API returns mounted backend
// names.
func TestMcpRouterListBackends(t *testing.T) {
	t.Parallel()
	handler, _ := routerTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/ai/mcp/router/backends", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := json.Marshal(body.Data)
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("unmarshal names: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "stub" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stub not in backends: %v", names)
	}
}

// TestMcpRouterRemoveBackend verifies the admin DELETE endpoint unmounts
// a backend.
func TestMcpRouterRemoveBackend(t *testing.T) {
	t.Parallel()
	handler, router := routerTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v3/admin/ai/mcp/router/backends?name=stub", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(router.ListBackends()) != 0 {
		t.Fatalf("backends after remove = %v", router.ListBackends())
	}
}

// TestMcpRouterRemoveBackendNotFound verifies 404 for unknown backend.
func TestMcpRouterRemoveBackendNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := routerTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v3/admin/ai/mcp/router/backends?name=ghost", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestMcpRouterAddBackend verifies the admin POST endpoint mounts a remote
// backend. The remote is a mock MCP server started inline.
func TestMcpRouterAddBackend(t *testing.T) {
	t.Parallel()
	// Start a mock remote MCP server.
	remote := mcp.NewServer(&mcp.Implementation{Name: "remote", Version: "v1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "ping"}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	remoteHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil)
	remoteSrv := httptest.NewServer(remoteHandler)
	defer remoteSrv.Close()

	handler, _ := routerTestHandler(t)

	body, _ := json.Marshal(map[string]string{
		"name": "remote1",
		"url":  remoteSrv.URL + "/v3/ai/mcp/router",
	})
	// The remote server's MCP endpoint is the root, not the gonacos path.
	body, _ = json.Marshal(map[string]string{
		"name": "remote1",
		"url":  remoteSrv.URL,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ai/mcp/router/backends", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Verify the remote backend's tool is now reachable through the router.
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, server.URL+"/v3/ai/mcp/router", mcpclient.DialOptions{
		DisableStandaloneSSE: true,
	})
	if err != nil {
		t.Fatalf("dial router: %v", err)
	}
	defer client.Close()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "remote1.ping" {
			found = true
		}
	}
	if !found {
		var names []string
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("remote1.ping not in tools: %v", names)
	}
}

// TestMcpRouterAddBackendRejectsMissingFields verifies the input guard.
func TestMcpRouterAddBackendRejectsMissingFields(t *testing.T) {
	t.Parallel()
	handler, _ := routerTestHandler(t)
	body, _ := json.Marshal(map[string]string{"name": "x"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ai/mcp/router/backends", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "name and url are required") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
