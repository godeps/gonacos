package mcptemplate

import (
	"errors"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/ai/apitomcp"
)

// TestRenderRestApiTemplate renders the builtin rest-api-to-mcp template
// with the three required variables and verifies the result is a valid
// MCP config that picks up the defaults for the optional variables.
func TestRenderRestApiTemplate(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("rest-api-to-mcp")
	if tmpl == nil {
		t.Fatalf("rest-api-to-mcp builtin not found")
	}
	out, err := Render(*tmpl, map[string]string{
		"serverName": "testapi",
		"toolName":   "greet",
		"url":        "https://example.com/greet",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: testapi") {
		t.Fatalf("missing server name:\n%s", s)
	}
	if !strings.Contains(s, "- name: greet") {
		t.Fatalf("missing tool name:\n%s", s)
	}
	if !strings.Contains(s, "description: REST API tool") {
		t.Fatalf("missing default description:\n%s", s)
	}
	if !strings.Contains(s, "- name: query") {
		t.Fatalf("missing default argName:\n%s", s)
	}
	if !strings.Contains(s, "{{.response.body}}") {
		t.Fatalf("missing response body template:\n%s", s)
	}
	conv := apitomcp.NewConverter()
	if _, err := conv.LoadYAML(out); err != nil {
		t.Fatalf("rendered output is not a valid MCP config: %v", err)
	}
}

// TestRenderRestApiTemplateWithCustomValues verifies custom values override
// the builtin defaults.
func TestRenderRestApiTemplateWithCustomValues(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("rest-api-to-mcp")
	out, err := Render(*tmpl, map[string]string{
		"serverName":      "myapi",
		"toolName":        "fetch",
		"toolDescription": "fetch a user",
		"url":             "https://api.example.com/users",
		"argName":         "userId",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: myapi") {
		t.Fatalf("missing server name:\n%s", s)
	}
	if !strings.Contains(s, "- name: fetch") {
		t.Fatalf("missing tool name:\n%s", s)
	}
	if !strings.Contains(s, "description: fetch a user") {
		t.Fatalf("missing custom description:\n%s", s)
	}
	if !strings.Contains(s, "- name: userId") {
		t.Fatalf("missing custom argName:\n%s", s)
	}
}

// TestRenderRejectsMissingRequired verifies that a missing required variable
// surfaces as ErrMissingVariable and names the offending variable.
func TestRenderRejectsMissingRequired(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("rest-api-to-mcp")
	_, err := Render(*tmpl, map[string]string{
		"toolName": "greet",
		// serverName and url missing
	})
	if !errors.Is(err, ErrMissingVariable) {
		t.Fatalf("err = %v, want ErrMissingVariable", err)
	}
	if !strings.Contains(err.Error(), "serverName") {
		t.Fatalf("error should mention serverName: %v", err)
	}
}

// TestRenderRejectsEmptyBody verifies the empty-body guard.
func TestRenderRejectsEmptyBody(t *testing.T) {
	t.Parallel()
	_, err := Render(Template{ID: "x", Body: ""}, nil)
	if !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("err = %v, want ErrEmptyBody", err)
	}
}

// TestRenderRejectsWhitespaceBody verifies whitespace-only bodies are
// treated as empty.
func TestRenderRejectsWhitespaceBody(t *testing.T) {
	t.Parallel()
	_, err := Render(Template{ID: "x", Body: "   \n\t  "}, nil)
	if !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("err = %v, want ErrEmptyBody", err)
	}
}

// TestFindBuiltin verifies all builtin templates are discoverable and
// that unknown IDs return nil.
func TestFindBuiltin(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"rest-api-to-mcp", "openapi-to-mcp", "database-to-mcp"} {
		tmpl := FindBuiltin(id)
		if tmpl == nil {
			t.Fatalf("FindBuiltin(%q) = nil", id)
		}
		if tmpl.ID != id {
			t.Fatalf("FindBuiltin(%q).ID = %q", id, tmpl.ID)
		}
	}
	if FindBuiltin("nonexistent") != nil {
		t.Fatalf("FindBuiltin(nonexistent) should return nil")
	}
}

// TestRenderOpenApiTemplate renders the openapi-to-mcp builtin with all
// variables supplied and verifies the URL is concatenated correctly.
func TestRenderOpenApiTemplate(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("openapi-to-mcp")
	out, err := Render(*tmpl, map[string]string{
		"serverName": "usersapi",
		"baseUrl":    "https://api.example.com",
		"path":       "/users/{id}",
		"method":     "GET",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: usersapi") {
		t.Fatalf("missing server name:\n%s", s)
	}
	if !strings.Contains(s, "url: https://api.example.com/users/{id}") {
		t.Fatalf("missing url:\n%s", s)
	}
	if !strings.Contains(s, "method: GET") {
		t.Fatalf("missing method:\n%s", s)
	}
	conv := apitomcp.NewConverter()
	if _, err := conv.LoadYAML(out); err != nil {
		t.Fatalf("rendered output is not a valid MCP config: %v", err)
	}
}

// TestRenderOpenApiUsesDefaultMethod verifies the openapi template falls
// back to GET when method is not provided.
func TestRenderOpenApiUsesDefaultMethod(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("openapi-to-mcp")
	out, err := Render(*tmpl, map[string]string{
		"serverName": "usersapi",
		"baseUrl":    "https://api.example.com",
		"path":       "/users",
		// method missing — should default to GET
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "method: GET") {
		t.Fatalf("missing default method:\n%s", s)
	}
}

// TestRenderDatabaseTemplate renders the database-to-mcp builtin.
func TestRenderDatabaseTemplate(t *testing.T) {
	t.Parallel()
	tmpl := FindBuiltin("database-to-mcp")
	out, err := Render(*tmpl, map[string]string{
		"serverName":    "dbapi",
		"queryEndpoint": "https://db.example.com/query",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: dbapi") {
		t.Fatalf("missing server name:\n%s", s)
	}
	if !strings.Contains(s, "url: https://db.example.com/query") {
		t.Fatalf("missing url:\n%s", s)
	}
	if !strings.Contains(s, "argsToJsonBody: true") {
		t.Fatalf("missing argsToJsonBody:\n%s", s)
	}
	conv := apitomcp.NewConverter()
	if _, err := conv.LoadYAML(out); err != nil {
		t.Fatalf("rendered output is not a valid MCP config: %v", err)
	}
}

// TestRenderRejectsInvalidYAML verifies that a template body which renders
// to unparseable YAML is rejected by validateYAML.
func TestRenderRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	_, err := Render(Template{
		ID:   "bad",
		Body: `server: [unclosed`,
	}, nil)
	if err == nil {
		t.Fatalf("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "valid MCP config") {
		t.Fatalf("error should mention invalid MCP config: %v", err)
	}
}

// TestRenderRejectsInvalidMCPConfig verifies that a template body which
// renders to valid YAML but is missing required MCP fields (e.g.
// server.name) is rejected.
func TestRenderRejectsInvalidMCPConfig(t *testing.T) {
	t.Parallel()
	_, err := Render(Template{
		ID: "no-server",
		Body: `tools:
  - name: x
    requestTemplate:
      url: http://example.com`,
	}, nil)
	if err == nil {
		t.Fatalf("expected error for missing server.name")
	}
	if !strings.Contains(err.Error(), "valid MCP config") {
		t.Fatalf("error should mention invalid MCP config: %v", err)
	}
}

// TestRenderPassesExtraVariables verifies that values not declared in
// Variables are still exposed to the template body via .Vars.
func TestRenderPassesExtraVariables(t *testing.T) {
	t.Parallel()
	out, err := Render(Template{
		ID: "extra",
		Variables: []Variable{
			{Name: "name", Required: true},
		},
		Body: `server:
  name: {{.Vars.name}}
  description: {{.Vars.extra}}`,
	}, map[string]string{
		"name":  "testapi",
		"extra": "ad-hoc value",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "description: ad-hoc value") {
		t.Fatalf("extra variable not rendered:\n%s", s)
	}
}
