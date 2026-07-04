// Package mcptemplate provides a small template system for generating
// apitomcp YAML configs from parameterized skeletons. A Template is a
// YAML body with Go text/template placeholders; Render substitutes the
// caller's values and validates the result still parses as a valid
// MCP config.
package mcptemplate

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/godeps/gonacos/pkg/ai/apitomcp"
	"github.com/higress-group/openapi-to-mcpserver/pkg/models"
	"gopkg.in/yaml.v3"
)

// Template is a parameterized MCP config skeleton.
type Template struct {
	ID          string     `json:"id" yaml:"id"`
	Name        string     `json:"name" yaml:"name"`
	Description string     `json:"description" yaml:"description"`
	Category    string     `json:"category" yaml:"category"`
	Variables   []Variable `json:"variables" yaml:"variables"`
	Body        string     `json:"body" yaml:"body"`
}

// Variable describes a single substitution slot in the template body.
type Variable struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
	Required    bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

// Render substitutes the caller's values into the template body and
// validates that the result is a parseable apitomcp YAML config. Missing
// required variables cause an error; missing optional variables fall
// back to their default.
func Render(tmpl Template, values map[string]string) ([]byte, error) {
	if strings.TrimSpace(tmpl.Body) == "" {
		return nil, ErrEmptyBody
	}
	resolved, err := resolveValues(tmpl.Variables, values)
	if err != nil {
		return nil, err
	}
	rendered, err := executeTemplate(tmpl.ID, tmpl.Body, resolved)
	if err != nil {
		return nil, err
	}
	if err := validateYAML(rendered); err != nil {
		return nil, fmt.Errorf("rendered template is not a valid MCP config: %w", err)
	}
	return rendered, nil
}

// resolveValues merges defaults with caller-supplied values and enforces
// required variables.
func resolveValues(vars []Variable, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		val, ok := in[v.Name]
		if !ok || strings.TrimSpace(val) == "" {
			if v.Required && v.Default == "" {
				return nil, fmt.Errorf("%w: %s", ErrMissingVariable, v.Name)
			}
			val = v.Default
		}
		out[v.Name] = val
	}
	for k, v := range in {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out, nil
}

// executeTemplate parses and executes a Go text/template in one shot.
// The values map is exposed as `.Vars` to avoid clashing with template
// builtins like `.Arg`.
func executeTemplate(name, body string, values map[string]string) ([]byte, error) {
	tmpl, err := template.New(name).Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Vars": values}); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// validateYAML parses the bytes as a models.MCPConfig to ensure the
// rendered template is structurally valid. This catches typos in
// variable substitution that would otherwise surface at backend
// construction time.
func validateYAML(b []byte) error {
	var cfg models.MCPConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return err
	}
	conv := apitomcp.NewConverter()
	if _, err := conv.LoadYAML(b); err != nil {
		return err
	}
	return nil
}

// BuiltinTemplates is the set of templates shipped with gonacos.
var BuiltinTemplates = []Template{
	{
		ID:          "rest-api-to-mcp",
		Name:        "REST API to MCP",
		Description: "Wrap a single GET endpoint as an MCP tool.",
		Category:    "rest",
		Variables: []Variable{
			{Name: "serverName", Description: "MCP server name", Required: true},
			{Name: "toolName", Description: "MCP tool name", Required: true},
			{Name: "toolDescription", Description: "Human-readable tool description", Default: "REST API tool"},
			{Name: "url", Description: "Upstream REST URL (may include {{.Vars.query}})", Required: true},
			{Name: "argName", Description: "Name of the query argument", Default: "query"},
		},
		Body: `server:
  name: {{.Vars.serverName}}
tools:
  - name: {{.Vars.toolName}}
    description: {{.Vars.toolDescription}}
    args:
      - name: {{.Vars.argName}}
        type: string
        required: true
    requestTemplate:
      method: GET
      url: {{.Vars.url}}
    responseTemplate:
      body: "{{"{{.response.body}}"}}"
`,
	},
	{
		ID:          "openapi-to-mcp",
		Name:        "OpenAPI to MCP (scaffold)",
		Description: "Generate a skeleton MCP config from a single OpenAPI path. For full multi-path conversion, use the higress openapi-to-mcpserver CLI.",
		Category:    "openapi",
		Variables: []Variable{
			{Name: "serverName", Description: "MCP server name", Required: true},
			{Name: "baseUrl", Description: "Upstream base URL", Required: true},
			{Name: "path", Description: "OpenAPI path (e.g. /users/{id})", Required: true},
			{Name: "method", Description: "HTTP method", Default: "GET"},
		},
		Body: `server:
  name: {{.Vars.serverName}}
tools:
  - name: callApi
    description: "Call {{.Vars.method}} {{.Vars.path}}"
    requestTemplate:
      method: {{.Vars.method}}
      url: {{.Vars.baseUrl}}{{.Vars.path}}
    responseTemplate:
      body: "{{"{{.response.body}}"}}"
`,
	},
	{
		ID:          "database-to-mcp",
		Name:        "Database to MCP (placeholder)",
		Description: "Placeholder for a database-backed MCP tool. Requires a custom HTTP gateway in front of the database.",
		Category:    "database",
		Variables: []Variable{
			{Name: "serverName", Description: "MCP server name", Required: true},
			{Name: "queryEndpoint", Description: "HTTP endpoint that accepts a SQL query and returns rows as JSON", Required: true},
		},
		Body: `server:
  name: {{.Vars.serverName}}
tools:
  - name: runQuery
    description: "Run a read-only SQL query"
    args:
      - name: sql
        type: string
        required: true
    requestTemplate:
      method: POST
      url: {{.Vars.queryEndpoint}}
      argsToJsonBody: true
    responseTemplate:
      body: "{{"{{.response.body}}"}}"
`,
	},
}

// FindBuiltin returns the builtin template with the given ID, or nil.
func FindBuiltin(id string) *Template {
	for i := range BuiltinTemplates {
		if BuiltinTemplates[i].ID == id {
			return &BuiltinTemplates[i]
		}
	}
	return nil
}

var (
	// ErrEmptyBody is returned when a template has no body.
	ErrEmptyBody = errors.New("mcptemplate: template body is empty")
	// ErrMissingVariable is returned when a required variable is not supplied.
	ErrMissingVariable = errors.New("mcptemplate: missing required variable")
)
