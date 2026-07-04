package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startMockMcpServer spins up a streamable HTTP MCP server with an "echo" tool.
func startMockMcpServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "mock-mcp", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "echo back the message",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Message string `json:"message" jsonschema:"the message to echo back"`
	}) (*mcp.CallToolResult, any, error) {
		msg := args.Message
		if msg == "" {
			msg = "(empty)"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "echo: " + msg},
			},
		}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return httptest.NewServer(handler)
}

// TestImportToolsFromMcpRemote verifies ImportToolsFromMcp dials a remote MCP
// server and returns its tools.
func TestImportToolsFromMcpRemote(t *testing.T) {
	t.Parallel()
	mock := startMockMcpServer(t)
	defer mock.Close()

	svc := NewService(nil)
	srv, err := svc.CreateMcpServer(McpServer{
		ID:       "remote-1",
		Name:     "Remote MCP",
		Protocol: "http",
		Endpoint: mock.URL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = srv

	tools, err := svc.ImportToolsFromMcp("remote-1")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len = %d, want 1", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Fatalf("name = %q, want echo", tools[0].Name)
	}
	if tools[0].Description != "echo back the message" {
		t.Fatalf("description = %q", tools[0].Description)
	}
}

// TestImportToolsFromMcpLocalFallback verifies that when no endpoint is set,
// ImportToolsFromMcp returns the locally-registered tools.
func TestImportToolsFromMcpLocalFallback(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	_, err := svc.CreateMcpServer(McpServer{
		ID:       "local-1",
		Name:     "Local MCP",
		Protocol: "http",
		Tools: []McpTool{
			{Name: "local-tool", Description: "a local tool"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tools, err := svc.ImportToolsFromMcp("local-1")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len = %d, want 1", len(tools))
	}
	if tools[0].Name != "local-tool" {
		t.Fatalf("name = %q", tools[0].Name)
	}
}

// TestImportToolsFromMcpDialFailure verifies that when the remote is
// unreachable, ImportToolsFromMcp falls back to local tools.
func TestImportToolsFromMcpDialFailure(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	_, err := svc.CreateMcpServer(McpServer{
		ID:       "broken-1",
		Name:     "Broken MCP",
		Protocol: "http",
		Endpoint: "http://127.0.0.1:1/no-server-here",
		Tools: []McpTool{
			{Name: "fallback-tool"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tools, err := svc.ImportToolsFromMcp("broken-1")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len = %d, want 1", len(tools))
	}
	if tools[0].Name != "fallback-tool" {
		t.Fatalf("name = %q", tools[0].Name)
	}
}

// TestValidateMcpImport verifies ValidateMcpImport succeeds for a reachable
// server and fails for an unreachable one.
func TestValidateMcpImport(t *testing.T) {
	t.Parallel()
	mock := startMockMcpServer(t)
	defer mock.Close()

	svc := NewService(nil)
	if err := svc.ValidateMcpImport(mock.URL); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := svc.ValidateMcpImport("http://127.0.0.1:1/no-server"); err == nil {
		t.Fatalf("expected error for unreachable URL")
	}
	if err := svc.ValidateMcpImport(""); err == nil {
		t.Fatalf("expected error for empty URL")
	}
}

// TestExecuteMcpImport verifies ExecuteMcpImport persists imported tools on
// the local McpServer entry.
func TestExecuteMcpImport(t *testing.T) {
	t.Parallel()
	mock := startMockMcpServer(t)
	defer mock.Close()

	svc := NewService(nil)
	_, err := svc.CreateMcpServer(McpServer{
		ID:       "exec-1",
		Name:     "Exec MCP",
		Protocol: "http",
		Endpoint: mock.URL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tools, err := svc.ExecuteMcpImport("exec-1")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len = %d, want 1", len(tools))
	}
	// Verify the tools are persisted on the server.
	srv, err := svc.GetMcpServer("exec-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(srv.Tools) != 1 {
		t.Fatalf("persisted len = %d, want 1", len(srv.Tools))
	}
	if srv.Tools[0].Name != "echo" {
		t.Fatalf("persisted name = %q", srv.Tools[0].Name)
	}
}

// TestImportToolsFromMcpNotFound verifies importing from an unknown server
// returns ErrResourceNotFound.
func TestImportToolsFromMcpNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	_, err := svc.ImportToolsFromMcp("ghost")
	if err == nil {
		t.Fatalf("expected error")
	}
}
