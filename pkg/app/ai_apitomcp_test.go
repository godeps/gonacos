package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	authsvc "github.com/godeps/gonacos/pkg/auth"
)

// apitomcpTestHandler builds a handler with an AI service that has an MCP
// router attached, so apitomcp configs can be mounted on it. Wrapped with
// admin token injection so /v3/admin/ai/ routes work without explicit token.
func apitomcpTestHandler(t *testing.T) (http.Handler, *ai.Service) {
	t.Helper()
	router := mcprouter.New()
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
	return wrapped, bundle.AI
}

// sampleApitomcpYAML returns a YAML config pointing the "ping" tool at the
// given URL.
func sampleApitomcpYAML(serverURL string) string {
	return `
server:
  name: pingapi
tools:
  - name: ping
    description: ping the server
    requestTemplate:
      method: GET
      url: ` + serverURL + `
    responseTemplate:
      body: "PONG: {{.response.body}}"
`
}

// postFormVals sends a POST request with URL-encoded form data.
func postFormVals(handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)
	return rec
}

// putFormVals sends a PUT request with URL-encoded form data.
func putFormVals(handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)
	return rec
}

// deleteFormVals sends a DELETE request with URL-encoded form data.
func deleteFormVals(handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)
	return rec
}

// getFormVals sends a GET request with URL-encoded query parameters.
func getFormVals(handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path+"?"+form.Encode(), nil)
	handler.ServeHTTP(rec, req)
	return rec
}

// decodeResult unmarshals the result envelope and re-decodes Data into dst.
func decodeResult(t *testing.T, body []byte, dst any) {
	t.Helper()
	var result resultBody
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, string(body))
	}
	if result.Code != 0 {
		t.Fatalf("result code = %d, message = %s", result.Code, result.Message)
	}
	data, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("decode data: %v (data: %s)", err, string(data))
	}
}

// TestApitomcpCreateAndList verifies that a config can be created and listed.
func TestApitomcpCreateAndList(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)

	rec := postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml":        {sampleApitomcpYAML("https://example.com")},
		"description": {"test config"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg ai.ApitomcpConfig
	decodeResult(t, rec.Body.Bytes(), &cfg)
	if cfg.Name != "pingapi" {
		t.Fatalf("name = %q, want pingapi", cfg.Name)
	}
	if cfg.ToolCount != 1 {
		t.Fatalf("toolCount = %d, want 1", cfg.ToolCount)
	}

	// List should include the new config.
	rec = getFormVals(handler, "/v3/admin/ai/apitomcp/list", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	var list []ai.ApitomcpConfig
	decodeResult(t, rec.Body.Bytes(), &list)
	found := false
	for _, c := range list {
		if c.Name == "pingapi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pingapi not in list: %+v", list)
	}
}

// TestApitomcpDetail verifies the detail endpoint.
func TestApitomcpDetail(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)

	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})

	rec := getFormVals(handler, "/v3/admin/ai/apitomcp/detail", url.Values{"name": {"pingapi"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg ai.ApitomcpConfig
	decodeResult(t, rec.Body.Bytes(), &cfg)
	if cfg.Name != "pingapi" {
		t.Fatalf("name = %q", cfg.Name)
	}
	if cfg.YAML == "" {
		t.Fatalf("yaml is empty")
	}
}

// TestApitomcpDetailNotFound verifies 404 for unknown config.
func TestApitomcpDetailNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/apitomcp/detail", url.Values{"name": {"ghost"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestApitomcpCreateRejectsInvalidYAML verifies bad YAML returns 400.
func TestApitomcpCreateRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	rec := postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {"server: [unclosed"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// TestApitomcpCreateRejectsDuplicate verifies creating the same config twice
// returns a conflict (HTTP 400 with protocol code 409).
func TestApitomcpCreateRejectsDuplicate(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	rec := postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var result resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Code != 409 {
		t.Fatalf("protocol code = %d, want 409", result.Code)
	}
}

// TestApitomcpUpdate verifies the update endpoint replaces YAML and remounts.
func TestApitomcpUpdate(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://old.example.com")},
	})

	rec := putFormVals(handler, "/v3/admin/ai/apitomcp/update", url.Values{
		"name": {"pingapi"},
		"yaml": {sampleApitomcpYAML("https://new.example.com")},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg ai.ApitomcpConfig
	decodeResult(t, rec.Body.Bytes(), &cfg)
	if !strings.Contains(cfg.YAML, "new.example.com") {
		t.Fatalf("yaml not updated")
	}
}

// TestApitomcpUpdateRejectsNameMismatch verifies changing server.name is
// rejected with a conflict (HTTP 400 with protocol code 409).
func TestApitomcpUpdateRejectsNameMismatch(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	rec := putFormVals(handler, "/v3/admin/ai/apitomcp/update", url.Values{
		"name": {"pingapi"},
		"yaml": {`
server:
  name: different
tools:
  - name: x
    requestTemplate:
      url: http://example.com
`},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var result resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Code != 409 {
		t.Fatalf("protocol code = %d, want 409", result.Code)
	}
}

// TestApitomcpDelete verifies the delete endpoint removes the config.
func TestApitomcpDelete(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	rec := deleteFormVals(handler, "/v3/admin/ai/apitomcp/delete", url.Values{"name": {"pingapi"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d", rec.Code)
	}
	// Detail should now 404.
	rec = getFormVals(handler, "/v3/admin/ai/apitomcp/detail", url.Values{"name": {"pingapi"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("detail after delete: status = %d, want 404", rec.Code)
	}
}

// TestApitomcpValidate verifies the validate endpoint returns server name and
// tool count without persisting.
func TestApitomcpValidate(t *testing.T) {
	t.Parallel()
	handler, svc := apitomcpTestHandler(t)
	rec := postFormVals(handler, "/v3/admin/ai/apitomcp/validate", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		ServerName string `json:"serverName"`
		ToolCount  int    `json:"toolCount"`
		Valid      bool   `json:"valid"`
	}
	decodeResult(t, rec.Body.Bytes(), &result)
	if result.ServerName != "pingapi" {
		t.Fatalf("serverName = %q", result.ServerName)
	}
	if result.ToolCount != 1 {
		t.Fatalf("toolCount = %d", result.ToolCount)
	}
	if !result.Valid {
		t.Fatalf("valid = false")
	}
	// Should not have been persisted.
	if cfg, _ := svc.GetApitomcpConfig("pingapi"); cfg != nil {
		t.Fatalf("config was persisted by validate")
	}
}

// TestApitomcpToolsList verifies the tools/list endpoint returns the tools
// defined in the config.
func TestApitomcpToolsList(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	rec := getFormVals(handler, "/v3/admin/ai/apitomcp/tools/list", url.Values{"name": {"pingapi"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var tools []map[string]any
	decodeResult(t, rec.Body.Bytes(), &tools)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0]["name"] != "ping" {
		t.Fatalf("tool name = %v", tools[0]["name"])
	}
}

// TestApitomcpToolsCall verifies the tools/call endpoint invokes the tool.
func TestApitomcpToolsCall(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "pong")
	}))
	defer mock.Close()

	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML(mock.URL)},
	})

	body, _ := json.Marshal(map[string]any{
		"tool": "ping",
		"args": map[string]any{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ai/apitomcp/tools/call?name=pingapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	decodeResult(t, rec.Body.Bytes(), &result)
	content, _ := json.Marshal(result["content"])
	var contents []map[string]any
	if err := json.Unmarshal(content, &contents); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if len(contents) == 0 {
		t.Fatalf("no content")
	}
	text, _ := contents[0]["text"].(string)
	if !strings.Contains(text, "PONG: pong") {
		t.Fatalf("text = %q", text)
	}
}

// TestApitomcpMountedOnRouter verifies that creating a config mounts the
// backend on the MCP router, making the tool reachable via the router
// endpoint.
func TestApitomcpMountedOnRouter(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "pong")
	}))
	defer mock.Close()

	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML(mock.URL)},
	})

	// Verify the backend is mounted on the router via the admin API.
	rec := getFormVals(handler, "/v3/admin/ai/mcp/router/backends", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("list backends: status = %d", rec.Code)
	}
	var backends []string
	decodeResult(t, rec.Body.Bytes(), &backends)
	found := false
	for _, b := range backends {
		if b == "pingapi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pingapi not mounted on router: %v", backends)
	}
}

// TestApitomcpDeleteUnmountsFromRouter verifies that deleting a config also
// unmounts the backend.
func TestApitomcpDeleteUnmountsFromRouter(t *testing.T) {
	t.Parallel()
	handler, _ := apitomcpTestHandler(t)
	postFormVals(handler, "/v3/admin/ai/apitomcp/create", url.Values{
		"yaml": {sampleApitomcpYAML("https://example.com")},
	})
	deleteFormVals(handler, "/v3/admin/ai/apitomcp/delete", url.Values{"name": {"pingapi"}})

	rec := getFormVals(handler, "/v3/admin/ai/mcp/router/backends", url.Values{})
	var backends []string
	decodeResult(t, rec.Body.Bytes(), &backends)
	for _, b := range backends {
		if b == "pingapi" {
			t.Fatalf("pingapi still mounted after delete")
		}
	}
}

// TestApitomcpSnapshotRestore verifies configs survive snapshot/restore.
func TestApitomcpSnapshotRestore(t *testing.T) {
	t.Parallel()
	_, svc := apitomcpTestHandler(t)
	if _, err := svc.CreateApitomcpConfig(sampleApitomcpYAML("https://example.com"), ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	snap, err := svc.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	data, _ := json.Marshal(snap)
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// New service, restore.
	router := mcprouter.New()
	newSvc := ai.NewService(nil, ai.WithMcpRouter(router))
	if err := newSvc.Restore(asMap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	cfg, err := newSvc.GetApitomcpConfig("pingapi")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if cfg.ToolCount != 1 {
		t.Fatalf("toolCount = %d", cfg.ToolCount)
	}
	// Backend should be remounted on the new router.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx
	backends := router.ListBackends()
	found := false
	for _, b := range backends {
		if b == "pingapi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pingapi not remounted after restore: %v", backends)
	}
}
