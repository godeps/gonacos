package apitomcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/ai/mcpclient"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sampleYAML returns a config that points the "greet" tool at the given
// URL. The tool takes a `name` argument and returns the response body
// wrapped in a template.
func sampleYAML(serverURL string) []byte {
	return []byte(`
server:
  name: api
  transport: http
tools:
  - name: greet
    description: greet a person
    args:
      - name: name
        type: string
        description: the person to greet
        required: true
    requestTemplate:
      method: GET
      url: ` + serverURL + `/greet?name={{.args.name}}
      headers:
        - key: X-Source
          value: gonacos-test
    responseTemplate:
      body: "GREETING: {{.response.body}}"
`)
}

// TestLoadYAMLAndCallTool loads a config, wraps it as a Backend, and
// verifies the HTTP request is dispatched and the response template is
// rendered.
func TestLoadYAMLAndCallTool(t *testing.T) {
	t.Parallel()
	var capturedPath, capturedHeader string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedHeader = r.Header.Get("X-Source")
		name := r.URL.Query().Get("name")
		_, _ = io.WriteString(w, "hello "+name)
	}))
	defer mock.Close()

	conv := NewConverter().WithAllowPrivate()
	cfg, err := conv.LoadYAML(sampleYAML(mock.URL))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Name != "api" {
		t.Fatalf("server name = %q", cfg.Server.Name)
	}
	backend, err := conv.ToBackend(cfg, nil)
	if err != nil {
		t.Fatalf("to backend: %v", err)
	}
	if backend.Name() != "api" {
		t.Fatalf("backend name = %q", backend.Name())
	}

	tools, err := backend.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := backend.CallTool(context.Background(), "greet", map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if len(res.Content) == 0 {
		t.Fatalf("no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "GREETING: hello world") {
		t.Fatalf("text = %q", tc.Text)
	}
	if capturedPath != "/greet" {
		t.Fatalf("path = %q", capturedPath)
	}
	if capturedHeader != "gonacos-test" {
		t.Fatalf("header = %q", capturedHeader)
	}
}

// TestLoadYAMLRejectsMissingServerName verifies the validation guard.
func TestLoadYAMLRejectsMissingServerName(t *testing.T) {
	t.Parallel()
	conv := NewConverter().WithAllowPrivate()
	_, err := conv.LoadYAML([]byte(`
server: {}
tools:
  - name: x
    requestTemplate:
      url: http://example.com
`))
	if err != ErrMissingServerName {
		t.Fatalf("err = %v, want %v", err, ErrMissingServerName)
	}
}

// TestLoadYAMLRejectsMissingURL verifies the per-tool URL guard fires
// at ToBackend time.
func TestLoadYAMLRejectsMissingURL(t *testing.T) {
	t.Parallel()
	conv := NewConverter().WithAllowPrivate()
	cfg, err := conv.LoadYAML([]byte(`
server:
  name: api
tools:
  - name: x
    requestTemplate:
      method: GET
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = conv.ToBackend(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "missing requestTemplate.url") {
		t.Fatalf("err = %v", err)
	}
}

// TestToBackendRejectsNoTools verifies the empty-tools guard.
func TestToBackendRejectsNoTools(t *testing.T) {
	t.Parallel()
	conv := NewConverter().WithAllowPrivate()
	_, err := conv.ToBackend(nil, nil)
	if err != ErrNilConfig {
		t.Fatalf("err = %v, want %v", err, ErrNilConfig)
	}
}

// TestApiToMcpBackendThroughRouter mounts the backend on a router and
// verifies end-to-end dispatch via the streamable HTTP transport.
func TestApiToMcpBackendThroughRouter(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "pong")
	}))
	defer mock.Close()

	conv := NewConverter().WithAllowPrivate()
	cfg, err := conv.LoadYAML([]byte(`
server:
  name: pingapi
tools:
  - name: ping
    description: ping the server
    requestTemplate:
      method: GET
      url: ` + mock.URL + `
    responseTemplate:
      body: "PONG: {{.response.body}}"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	backend, err := conv.ToBackend(cfg, nil)
	if err != nil {
		t.Fatalf("to backend: %v", err)
	}
	router := mcprouter.New()
	if err := router.AddBackend(backend); err != nil {
		t.Fatalf("add backend: %v", err)
	}
	httpSrv := httptest.NewServer(router.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, httpSrv.URL, mcpclient.DialOptions{DisableStandaloneSSE: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "pingapi.ping" {
		var names []string
		for _, t := range tools {
			names = append(names, t.Name)
		}
		t.Fatalf("tools = %v", names)
	}

	res, err := client.CallTool(ctx, "pingapi.ping", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	text, _ := mcpclient.ExtractText(res)
	if !strings.Contains(text, "PONG: pong") {
		t.Fatalf("text = %q", text)
	}
}

// TestApiToMcpBackendPostJSON verifies ArgsToJsonBody templating.
func TestApiToMcpBackendPostJSON(t *testing.T) {
	t.Parallel()
	var capturedBody string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		capturedBody = string(buf)
		_, _ = io.WriteString(w, "ok")
	}))
	defer mock.Close()

	conv := NewConverter().WithAllowPrivate()
	cfg, err := conv.LoadYAML([]byte(`
server:
  name: postapi
tools:
  - name: submit
    description: submit data
    args:
      - name: payload
        type: string
        required: true
    requestTemplate:
      method: POST
      url: ` + mock.URL + `
      argsToJsonBody: true
    responseTemplate:
      body: "{{.response.body}}"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	backend, err := conv.ToBackend(cfg, nil)
	if err != nil {
		t.Fatalf("to backend: %v", err)
	}
	res, err := backend.CallTool(context.Background(), "submit", map[string]any{"payload": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	if !strings.Contains(capturedBody, "payload") || !strings.Contains(capturedBody, "hello") {
		t.Fatalf("captured body = %q", capturedBody)
	}
}

// TestApiToMcpBackendErrorResponse verifies the error template is used
// when the upstream returns 4xx/5xx.
func TestApiToMcpBackendErrorResponse(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad input")
	}))
	defer mock.Close()

	conv := NewConverter().WithAllowPrivate()
	cfg, err := conv.LoadYAML([]byte(`
server:
  name: errapi
tools:
  - name: fail
    requestTemplate:
      method: GET
      url: ` + mock.URL + `
    responseTemplate:
      body: "OK"
    errorResponseTemplate: "ERROR: {{.response.body}}"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	backend, err := conv.ToBackend(cfg, nil)
	if err != nil {
		t.Fatalf("to backend: %v", err)
	}
	res, err := backend.CallTool(context.Background(), "fail", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "ERROR: bad input") {
		t.Fatalf("text = %q", tc.Text)
	}
}
