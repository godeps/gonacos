package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/mcptemplate"
)

// templateTestHandler builds a handler with an AI service for template tests.
func templateTestHandler(t *testing.T) (http.Handler, *ai.Service) {
	t.Helper()
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil)
	return NewHandlerWithServices("../..", bundle), bundle.AI
}

// postJSON sends a POST request with a JSON body.
func postJSONBody(handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

// putJSON sends a PUT request with a JSON body.
func putJSONBody(handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

// TestTemplateListIncludesBuiltins verifies the list endpoint returns builtin
// templates.
func TestTemplateListIncludesBuiltins(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/mcp/templates/list", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []mcptemplate.Template
	decodeResult(t, rec.Body.Bytes(), &list)
	ids := map[string]bool{}
	for _, tmpl := range list {
		ids[tmpl.ID] = true
	}
	for _, id := range []string{"rest-api-to-mcp", "openapi-to-mcp", "database-to-mcp"} {
		if !ids[id] {
			t.Fatalf("builtin %q not in list: %v", id, ids)
		}
	}
}

// TestTemplateDetailBuiltin verifies the detail endpoint returns a builtin.
func TestTemplateDetailBuiltin(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/mcp/templates/detail", url.Values{"id": {"rest-api-to-mcp"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var tmpl mcptemplate.Template
	decodeResult(t, rec.Body.Bytes(), &tmpl)
	if tmpl.ID != "rest-api-to-mcp" {
		t.Fatalf("id = %q", tmpl.ID)
	}
	if len(tmpl.Variables) == 0 {
		t.Fatalf("no variables")
	}
}

// TestTemplateDetailNotFound verifies 404 for unknown template.
func TestTemplateDetailNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/mcp/templates/detail", url.Values{"id": {"ghost"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestTemplateCreateAndDetail verifies a user template can be created and
// retrieved.
func TestTemplateCreateAndDetail(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:          "my-tmpl",
		Name:        "My Template",
		Description: "custom template",
		Category:    "rest",
		Variables: []mcptemplate.Variable{
			{Name: "serverName", Description: "server name", Required: true},
		},
		Body: `server:
  name: {{.Vars.serverName}}
tools:
  - name: ping
    requestTemplate:
      url: http://example.com
    responseTemplate:
      body: "{{"{{.response.body}}"}}"
`,
	}
	rec := postJSONBody(handler, "/v3/admin/ai/mcp/templates/create", tmpl)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created mcptemplate.Template
	decodeResult(t, rec.Body.Bytes(), &created)
	if created.ID != "my-tmpl" {
		t.Fatalf("id = %q", created.ID)
	}

	// Detail should return the new template.
	rec = getFormVals(handler, "/v3/admin/ai/mcp/templates/detail", url.Values{"id": {"my-tmpl"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d", rec.Code)
	}
	var got mcptemplate.Template
	decodeResult(t, rec.Body.Bytes(), &got)
	if got.Name != "My Template" {
		t.Fatalf("name = %q", got.Name)
	}
}

// TestTemplateCreateRejectsBuiltinID verifies creating a template with a
// builtin ID is rejected.
func TestTemplateCreateRejectsBuiltinID(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:   "rest-api-to-mcp",
		Name: "Override",
		Body: "server:\n  name: x",
	}
	rec := postJSONBody(handler, "/v3/admin/ai/mcp/templates/create", tmpl)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var result resultBody
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Code != 409 {
		t.Fatalf("protocol code = %d, want 409", result.Code)
	}
}

// TestTemplateUpdate verifies the update endpoint replaces a user template.
func TestTemplateUpdate(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:   "upd-tmpl",
		Name: "Original",
		Body: "server:\n  name: x",
	}
	postJSONBody(handler, "/v3/admin/ai/mcp/templates/create", tmpl)

	updated := tmpl
	updated.Name = "Updated"
	rec := putJSONBody(handler, "/v3/admin/ai/mcp/templates/update", updated)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got mcptemplate.Template
	decodeResult(t, rec.Body.Bytes(), &got)
	if got.Name != "Updated" {
		t.Fatalf("name = %q, want Updated", got.Name)
	}
}

// TestTemplateUpdateRejectsBuiltin verifies builtins are immutable.
func TestTemplateUpdateRejectsBuiltin(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:   "rest-api-to-mcp",
		Name: "Override",
		Body: "server:\n  name: x",
	}
	rec := putJSONBody(handler, "/v3/admin/ai/mcp/templates/update", tmpl)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestTemplateDelete verifies the delete endpoint removes a user template.
func TestTemplateDelete(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:   "del-tmpl",
		Name: "Delete Me",
		Body: "server:\n  name: x",
	}
	postJSONBody(handler, "/v3/admin/ai/mcp/templates/create", tmpl)

	rec := deleteFormVals(handler, "/v3/admin/ai/mcp/templates/delete", url.Values{"id": {"del-tmpl"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Detail should now 404.
	rec = getFormVals(handler, "/v3/admin/ai/mcp/templates/detail", url.Values{"id": {"del-tmpl"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("detail after delete: status = %d, want 404", rec.Code)
	}
}

// TestTemplateDeleteRejectsBuiltin verifies builtins cannot be deleted.
func TestTemplateDeleteRejectsBuiltin(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	rec := deleteFormVals(handler, "/v3/admin/ai/mcp/templates/delete", url.Values{"id": {"rest-api-to-mcp"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestTemplateRender verifies the render endpoint produces YAML from a
// template + variable values.
func TestTemplateRender(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	form := url.Values{
		"id":         {"rest-api-to-mcp"},
		"serverName": {"testapi"},
		"toolName":   {"greet"},
		"url":        {"https://example.com"},
	}
	rec := postFormVals(handler, "/v3/admin/ai/mcp/templates/render", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("render: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		ID       string `json:"id"`
		YAML     string `json:"yaml"`
		Rendered bool   `json:"rendered"`
	}
	decodeResult(t, rec.Body.Bytes(), &result)
	if !result.Rendered {
		t.Fatalf("rendered = false")
	}
	if !strings.Contains(result.YAML, "name: testapi") {
		t.Fatalf("yaml missing server name:\n%s", result.YAML)
	}
	if !strings.Contains(result.YAML, "- name: greet") {
		t.Fatalf("yaml missing tool name:\n%s", result.YAML)
	}
}

// TestTemplateRenderRejectsMissingRequired verifies missing required variables
// return an error.
func TestTemplateRenderRejectsMissingRequired(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	form := url.Values{
		"id": {"rest-api-to-mcp"},
		// serverName and toolName missing
	}
	rec := postFormVals(handler, "/v3/admin/ai/mcp/templates/render", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// TestTemplateInstantiate verifies the instantiate endpoint renders a template
// and creates an apitomcp config.
func TestTemplateInstantiate(t *testing.T) {
	t.Parallel()
	handler, _ := templateTestHandler(t)
	form := url.Values{
		"id":         {"rest-api-to-mcp"},
		"serverName": {"instapi"},
		"toolName":   {"ping"},
		"url":        {"https://example.com"},
	}
	rec := postFormVals(handler, "/v3/admin/ai/mcp/templates/instantiate", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("instantiate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg ai.ApitomcpConfig
	decodeResult(t, rec.Body.Bytes(), &cfg)
	if cfg.Name != "instapi" {
		t.Fatalf("name = %q, want instapi", cfg.Name)
	}
	if cfg.ToolCount != 1 {
		t.Fatalf("toolCount = %d, want 1", cfg.ToolCount)
	}
	// Config should be persistent.
	rec = getFormVals(handler, "/v3/admin/ai/apitomcp/detail", url.Values{"name": {"instapi"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("detail after instantiate: status = %d", rec.Code)
	}
}

// TestTemplateSnapshotRestore verifies user templates survive snapshot/restore.
func TestTemplateSnapshotRestore(t *testing.T) {
	t.Parallel()
	_, svc := templateTestHandler(t)
	tmpl := mcptemplate.Template{
		ID:       "snap-tmpl",
		Name:     "Snap",
		Category: "rest",
		Body:     "server:\n  name: x",
	}
	if _, err := svc.CreateTemplate(tmpl); err != nil {
		t.Fatalf("create: %v", err)
	}

	snap, err := svc.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	data, _ := json.Marshal(snap)
	var asMap map[string]any
	json.Unmarshal(data, &asMap)

	newSvc := ai.NewService(nil)
	if err := newSvc.Restore(asMap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := newSvc.GetTemplate("snap-tmpl")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if got.Name != "Snap" {
		t.Fatalf("name = %q", got.Name)
	}
	// Builtins should still be present.
	list := newSvc.ListTemplates()
	found := false
	for _, t := range list {
		if t.ID == "rest-api-to-mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("builtins missing after restore")
	}
}
