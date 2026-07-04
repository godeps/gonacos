package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoArgs is the typed argument struct for the echo tool. Using a typed
// struct lets mcp.AddTool auto-generate the input schema, which the
// non-generic Server.AddTool requires a manual schema for.
type echoArgs struct {
	Message string `json:"message" jsonschema:"the message to echo back"`
}

// startMockServer spins up a streamable HTTP MCP server that registers a
// single "echo" tool. The tool returns whatever text the caller passes in
// the "message" argument, prefixed with "echo: ".
func startMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "mock-mcp", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "echo back the message",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
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

// TestDialAndListTools verifies the full happy path: Dial, ListTools sees
// the echo tool, CallTool returns the echoed text.
func TestDialAndListTools(t *testing.T) {
	t.Parallel()
	server := startMockServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, server.URL, DialOptions{DisableStandaloneSSE: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		var names []string
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("tools = %v, want [echo]", names)
	}

	result, err := client.CallTool(ctx, "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	text, err := ExtractText(result)
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if text != "echo: hello" {
		t.Fatalf("text = %q, want %q", text, "echo: hello")
	}
}

// TestDialRejectsEmptyURL verifies the input guard.
func TestDialRejectsEmptyURL(t *testing.T) {
	t.Parallel()
	if _, err := Dial(context.Background(), "", DialOptions{}); err != ErrMissingURL {
		t.Fatalf("err = %v, want %v", err, ErrMissingURL)
	}
}

// TestCallToolRejectsEmptyName verifies the input guard.
func TestCallToolRejectsEmptyName(t *testing.T) {
	t.Parallel()
	client := &Client{}
	if _, err := client.CallTool(context.Background(), "  ", nil); err != ErrMissingToolName {
		t.Fatalf("err = %v, want %v", err, ErrMissingToolName)
	}
}

// TestMethodsRejectAfterClose ensures Close invalidates the session for
// subsequent calls.
func TestMethodsRejectAfterClose(t *testing.T) {
	t.Parallel()
	server := startMockServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, server.URL, DialOptions{DisableStandaloneSSE: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := client.ListTools(ctx); err != ErrNotConnected {
		t.Fatalf("list after close: err = %v, want %v", err, ErrNotConnected)
	}
}

// TestExtractTextRejectsNilResult verifies the helper's nil guard.
func TestExtractTextRejectsNilResult(t *testing.T) {
	t.Parallel()
	if _, err := ExtractText(nil); err != ErrEmptyResponse {
		t.Fatalf("err = %v, want %v", err, ErrEmptyResponse)
	}
	// Result with no content is also empty.
	if _, err := ExtractText(&CallToolResult{}); err != ErrEmptyResponse {
		t.Fatalf("err = %v, want %v", err, ErrEmptyResponse)
	}
}

// TestDialWithBearerToken verifies that auth headers are forwarded to the
// server. The mock server rejects requests without the expected token.
func TestDialWithBearerToken(t *testing.T) {
	t.Parallel()
	const token = "secret-token"
	// Create the MCP server once so its session state survives across
	// the initialize -> notifications/initialized -> tools/call sequence.
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "auth-mock", Version: "v1.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "ping"}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, server.URL, DialOptions{
		BearerToken:          token,
		DisableStandaloneSSE: true,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	result, err := client.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if text, _ := ExtractText(result); text != "pong" {
		t.Fatalf("text = %q, want pong", text)
	}
}

// TestDialFailureInvalidURL ensures Dial surfaces transport errors for
// unreachable servers.
func TestDialFailureInvalidURL(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// 127.0.0.1:1 is almost always closed.
	_, err := Dial(ctx, "http://127.0.0.1:1/mcp", DialOptions{
		DisableStandaloneSSE: true,
		MaxRetries:           0,
	})
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "mcp connect") {
		t.Fatalf("err = %v, want mcp connect error or deadline", err)
	}
}
