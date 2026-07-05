// Package mcpclient wraps the official modelcontextprotocol/go-sdk to give
// gonacos a small, opinionated client for remote MCP servers speaking the
// streamable HTTP transport.
package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool and CallToolResult are re-exported so callers do not need to import
// the go-sdk directly. They are type aliases, not new types.
type (
	Tool           = mcp.Tool
	CallToolResult = mcp.CallToolResult
)

// DialOptions controls transport-level settings for a client connection.
type DialOptions struct {
	// Headers are added to every request to the remote MCP server.
	Headers map[string]string
	// BearerToken, if set, is sent as `Authorization: Bearer <token>`.
	BearerToken string
	// Timeout is the per-request timeout. Zero means 60s.
	Timeout time.Duration
	// MaxRetries is the number of reconnect attempts the transport makes
	// before giving up. Negative disables retries.
	MaxRetries int
	// DisableStandaloneSSE suppresses the persistent SSE GET after
	// initialization. Useful when the server does not support server-
	// initiated notifications.
	DisableStandaloneSSE bool
}

// Client is a thin wrapper around *mcp.ClientSession. It is safe for
// concurrent use after Dial returns.
type Client struct {
	session *mcp.ClientSession
	closer  func() error
}

// Dial connects to a streamable HTTP MCP server at url and performs the
// initialize handshake. The returned Client must be closed with Close.
func Dial(ctx context.Context, url string, opts DialOptions) (*Client, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, ErrMissingURL
	}
	httpClient := &http.Client{Timeout: withDefault(opts.Timeout, 60*time.Second)}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             url,
		HTTPClient:           httpClient,
		MaxRetries:           opts.MaxRetries,
		DisableStandaloneSSE: opts.DisableStandaloneSSE,
	}
	if len(opts.Headers) > 0 || opts.BearerToken != "" {
		transport.HTTPClient = httpClientWithAuth(httpClient, opts)
	}
	impl := &mcp.Implementation{Name: "gonacos-mcpclient", Version: "v1.0.0"}
	client := mcp.NewClient(impl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect: %w", err)
	}
	return &Client{
		session: session,
		closer:  session.Close,
	}, nil
}

func withDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// httpClientWithAuth returns an *http.Client whose Transport wraps every
// outbound request with the configured headers and Bearer token. The
// underlying client's own timeout is preserved.
func httpClientWithAuth(base *http.Client, opts DialOptions) *http.Client {
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &http.Client{
		Timeout: base.Timeout,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			for k, v := range opts.Headers {
				req.Header.Set(k, v)
			}
			if opts.BearerToken != "" && req.Header.Get("Authorization") == "" {
				req.Header.Set("Authorization", "Bearer "+opts.BearerToken)
			}
			return rt.RoundTrip(req)
		}),
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ListTools returns the tools advertised by the remote server. It
// paginates internally so callers receive the full list.
func (c *Client) ListTools(ctx context.Context) ([]*Tool, error) {
	if c == nil || c.session == nil {
		return nil, ErrNotConnected
	}
	var out []*Tool
	var cursor string
	for {
		params := &mcp.ListToolsParams{}
		if cursor != "" {
			params.Cursor = cursor
		}
		result, err := c.session.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("mcp list tools: %w", err)
		}
		if result == nil {
			break
		}
		out = append(out, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// CallTool invokes the named tool on the remote server with the given
// arguments. Arguments may be any JSON-marshalable value (typically a
// map[string]any).
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*CallToolResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrMissingToolName
	}
	if c == nil || c.session == nil {
		return nil, ErrNotConnected
	}
	params := &mcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	}
	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("mcp call %q: %w", name, err)
	}
	return result, nil
}

// Ping checks that the remote server is still responsive.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.session == nil {
		return ErrNotConnected
	}
	return c.session.Ping(ctx, nil)
}

// Close terminates the session. Subsequent calls return ErrNotConnected.
func (c *Client) Close() error {
	if c == nil || c.closer == nil {
		return nil
	}
	err := c.closer()
	c.closer = nil
	c.session = nil
	return err
}

// ExtractText returns the concatenated text content from a CallToolResult.
// Non-text content (images, audio, embedded resources) is skipped. Returns
// ErrEmptyResponse if the result has no text content.
func ExtractText(result *CallToolResult) (string, error) {
	if result == nil {
		return "", ErrEmptyResponse
	}
	var b strings.Builder
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok && tc != nil {
			b.WriteString(tc.Text)
		}
	}
	if b.Len() == 0 {
		return "", ErrEmptyResponse
	}
	return b.String(), nil
}

var (
	// ErrMissingURL is returned by Dial when the URL is empty.
	ErrMissingURL = errors.New("mcpclient: url is required")
	// ErrMissingToolName is returned by CallTool when the name is empty.
	ErrMissingToolName = errors.New("mcpclient: tool name is required")
	// ErrNotConnected is returned when a method is called before Dial or after Close.
	ErrNotConnected = errors.New("mcpclient: not connected")
	// ErrEmptyResponse is returned when the server returned no usable content.
	ErrEmptyResponse = errors.New("mcpclient: empty response from server")
)
