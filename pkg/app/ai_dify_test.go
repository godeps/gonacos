package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/dify"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
)

// difyTestHandler builds a handler with an AI service that has a Dify client
// and MCP router attached.
func difyTestHandler(t *testing.T, endpoint, apiKey string) (http.Handler, *ai.Service) {
	t.Helper()
	router := mcprouter.New()
	client := dify.NewClient(endpoint, apiKey)
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil, ai.WithMcpRouter(router), ai.WithDify(client))
	return NewHandlerWithServices("../..", bundle), bundle.AI
}

// difyTestHandlerNoClient builds a handler with no Dify client attached.
func difyTestHandlerNoClient(t *testing.T) (http.Handler, *ai.Service) {
	t.Helper()
	router := mcprouter.New()
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil, ai.WithMcpRouter(router))
	return NewHandlerWithServices("../..", bundle), bundle.AI
}

// TestDifyConfigGetNotConfigured verifies the config endpoint reports
// configured=false when no client is attached.
func TestDifyConfigGetNotConfigured(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandlerNoClient(t)
	rec := getFormVals(handler, "/v3/admin/ai/dify/config", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Configured bool   `json:"configured"`
		Endpoint   string `json:"endpoint"`
	}
	decodeResult(t, rec.Body.Bytes(), &resp)
	if resp.Configured {
		t.Fatalf("expected configured=false")
	}
	if resp.Endpoint != "" {
		t.Fatalf("endpoint = %q, want empty", resp.Endpoint)
	}
}

// TestDifyConfigGetConfigured verifies the config endpoint reports the
// endpoint when a client is attached.
func TestDifyConfigGetConfigured(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	rec := getFormVals(handler, "/v3/admin/ai/dify/config", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Configured bool   `json:"configured"`
		Endpoint   string `json:"endpoint"`
	}
	decodeResult(t, rec.Body.Bytes(), &resp)
	if !resp.Configured {
		t.Fatalf("expected configured=true")
	}
	if resp.Endpoint != "https://api.dify.ai" {
		t.Fatalf("endpoint = %q, want https://api.dify.ai", resp.Endpoint)
	}
}

// TestDifyConfigSet verifies the config endpoint updates the client at runtime.
func TestDifyConfigSet(t *testing.T) {
	t.Parallel()
	handler, svc := difyTestHandlerNoClient(t)
	form := url.Values{"endpoint": {"https://api.dify.ai"}, "apiKey": {"my-key"}}
	rec := postFormVals(handler, "/v3/admin/ai/dify/config", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	c := svc.DifyClient()
	if c == nil {
		t.Fatalf("client should be set")
	}
	if c.Endpoint() != "https://api.dify.ai" {
		t.Fatalf("endpoint = %q", c.Endpoint())
	}
	if c.APIKey() != "my-key" {
		t.Fatalf("apiKey = %q", c.APIKey())
	}
}

// TestDifyConfigSetMissingEndpoint verifies missing endpoint returns 400.
func TestDifyConfigSetMissingEndpoint(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandlerNoClient(t)
	form := url.Values{"apiKey": {"my-key"}}
	rec := postFormVals(handler, "/v3/admin/ai/dify/config", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDifyWorkflowsList verifies listing locally-configured workflows.
func TestDifyWorkflowsList(t *testing.T) {
	t.Parallel()
	handler, svc := difyTestHandler(t, "https://api.dify.ai", "key")
	svc.DifyClient().SetWorkflows([]dify.WorkflowSummary{
		{ID: "wf-1", Name: "Workflow 1"},
		{ID: "wf-2", Name: "Workflow 2"},
	})
	rec := getFormVals(handler, "/v3/admin/ai/dify/workflows/list", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []dify.WorkflowSummary
	decodeResult(t, rec.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].ID != "wf-1" {
		t.Fatalf("first = %q", list[0].ID)
	}
}

// TestDifyWorkflowsListNotConfigured verifies listing without a client returns 503.
func TestDifyWorkflowsListNotConfigured(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandlerNoClient(t)
	rec := getFormVals(handler, "/v3/admin/ai/dify/workflows/list", url.Values{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestDifyWorkflowsRun verifies a blocking-mode workflow call is forwarded
// to the Dify API.
func TestDifyWorkflowsRun(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflows/run" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"workflow_id":"wf-1","task_id":"t-1","data":{"status":"succeeded","outputs":{"result":"hello world"}}}`)
	}))
	defer mock.Close()

	handler, _ := difyTestHandler(t, mock.URL, "key")
	form := url.Values{
		"workflowId": {"wf-1"},
		"inputs":     {`{"message":"hi"}`},
		"user":       {"alice"},
	}
	rec := postFormVals(handler, "/v3/admin/ai/dify/workflows/run", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var res dify.WorkflowResult
	decodeResult(t, rec.Body.Bytes(), &res)
	if res.Status != "succeeded" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.Outputs["result"] != "hello world" {
		t.Fatalf("outputs.result = %v", res.Outputs["result"])
	}
}

// TestDifyWorkflowsRunMissingWorkflowID verifies missing workflowId returns 400.
func TestDifyWorkflowsRunMissingWorkflowID(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	form := url.Values{"inputs": {`{"message":"hi"}`}}
	rec := postFormVals(handler, "/v3/admin/ai/dify/workflows/run", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDifyWorkflowsRunInvalidInputs verifies invalid inputs JSON returns 400.
func TestDifyWorkflowsRunInvalidInputs(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	form := url.Values{"workflowId": {"wf-1"}, "inputs": {"not-json"}}
	rec := postFormVals(handler, "/v3/admin/ai/dify/workflows/run", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDifyWorkflowsRunNotConfigured verifies running without a client returns 503.
func TestDifyWorkflowsRunNotConfigured(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandlerNoClient(t)
	form := url.Values{"workflowId": {"wf-1"}}
	rec := postFormVals(handler, "/v3/admin/ai/dify/workflows/run", form)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestDifyWorkflowsImport verifies a workflow can be imported as a backend.
func TestDifyWorkflowsImport(t *testing.T) {
	t.Parallel()
	handler, svc := difyTestHandler(t, "https://api.dify.ai", "key")
	// Verify router has 0 backends initially.
	if len(svc.McpRouter().ListBackends()) != 0 {
		t.Fatalf("expected 0 backends initially")
	}
	body := map[string]any{
		"serverName": "dify",
		"tools": []map[string]any{
			{"toolName": "ask", "workflowId": "wf-1", "description": "Ask Dify"},
		},
	}
	rec := postJSONBody(handler, "/v3/admin/ai/dify/workflows/import", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	decodeResult(t, rec.Body.Bytes(), &resp)
	if resp["name"] != "dify" {
		t.Fatalf("name = %q", resp["name"])
	}
	if resp["status"] != "mounted" {
		t.Fatalf("status = %q", resp["status"])
	}
	// Verify router now has 1 backend.
	if len(svc.McpRouter().ListBackends()) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(svc.McpRouter().ListBackends()))
	}
}

// TestDifyWorkflowsImportMissingFields verifies missing serverName/tools returns 400.
func TestDifyWorkflowsImportMissingFields(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	body := map[string]any{"serverName": "", "tools": []map[string]any{}}
	rec := postJSONBody(handler, "/v3/admin/ai/dify/workflows/import", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDifyWorkflowsImportInvalidJSON verifies invalid JSON body returns 400.
func TestDifyWorkflowsImportInvalidJSON(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	// Send raw bytes that aren't valid JSON. postJSONBody marshals, so we
	// craft a body that marshals to invalid JSON: a bare string with quotes
	// is still valid JSON. Instead, use a custom request.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ai/dify/workflows/import", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDifyWorkflowsImportNoRouter verifies importing without a router returns 503.
func TestDifyWorkflowsImportNoRouter(t *testing.T) {
	t.Parallel()
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil) // no router
	handler := NewHandlerWithServices("../..", bundle)
	body := map[string]any{
		"serverName": "dify",
		"tools": []map[string]any{
			{"toolName": "ask", "workflowId": "wf-1"},
		},
	}
	rec := postJSONBody(handler, "/v3/admin/ai/dify/workflows/import", body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestDifyManifest verifies the manifest endpoint returns the expected fields.
func TestDifyManifest(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	form := url.Values{
		"routerUrl": {"https://gonacos.example.com/v3/ai/mcp/router"},
		"toolName":  {"dify.run"},
	}
	rec := getFormVals(handler, "/v3/admin/ai/dify/manifest", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var m dify.DifyToolManifest
	decodeResult(t, rec.Body.Bytes(), &m)
	if m.RouterURL != "https://gonacos.example.com/v3/ai/mcp/router" {
		t.Fatalf("routerURL = %q", m.RouterURL)
	}
	if m.ToolName != "dify.run" {
		t.Fatalf("toolName = %q", m.ToolName)
	}
	if m.Method != "POST" {
		t.Fatalf("method = %q", m.Method)
	}
}

// TestDifyManifestDefaults verifies the manifest endpoint uses defaults
// when params are empty.
func TestDifyManifestDefaults(t *testing.T) {
	t.Parallel()
	handler, _ := difyTestHandler(t, "https://api.dify.ai", "key")
	rec := getFormVals(handler, "/v3/admin/ai/dify/manifest", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var m dify.DifyToolManifest
	decodeResult(t, rec.Body.Bytes(), &m)
	if m.RouterURL != "/v3/ai/mcp/router" {
		t.Fatalf("routerURL = %q, want /v3/ai/mcp/router", m.RouterURL)
	}
	if m.ToolName != "dify.run" {
		t.Fatalf("toolName = %q, want dify.run", m.ToolName)
	}
}

// Suppress unused import warnings (json is used by decodeResult; httptest
// is used by difyTestHandler).
var _ = json.Unmarshal
