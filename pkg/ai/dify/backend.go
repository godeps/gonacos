package dify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WorkflowTool binds a Dify workflow to a single MCP tool. The tool's args
// become the workflow's inputs; the workflow's outputs["result"] (or the
// raw JSON of outputs if no "result" key) becomes the tool's text content.
type WorkflowTool struct {
	ToolName    string            `json:"toolName"`
	Description string            `json:"description,omitempty"`
	WorkflowID  string            `json:"workflowId"`
	ArgMap      map[string]string `json:"argMap,omitempty"`
}

// WorkflowBackend exposes one or more Dify workflows as MCP tools behind
// the mcprouter.Backend interface.
type WorkflowBackend struct {
	name  string
	tools []*WorkflowTool
	c     *Client
}

// NewWorkflowBackend creates a backend with the given server name and tools.
// The Client must be non-nil and configured with the endpoint + apiKey.
func NewWorkflowBackend(name string, tools []*WorkflowTool, c *Client) (*WorkflowBackend, error) {
	if strings.TrimSpace(name) == "" {
		return nil, ErrBackendNameRequired
	}
	if c == nil {
		return nil, ErrClientRequired
	}
	if len(tools) == 0 {
		return nil, ErrNoTools
	}
	return &WorkflowBackend{name: name, tools: tools, c: c}, nil
}

// Name returns the backend's server name.
func (b *WorkflowBackend) Name() string { return b.name }

// ListTools returns the MCP tools advertised by this backend.
func (b *WorkflowBackend) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	out := make([]*mcp.Tool, 0, len(b.tools))
	for _, t := range b.tools {
		tool := &mcp.Tool{
			Name:        t.ToolName,
			Description: t.Description,
			InputSchema: buildWorkflowSchema(t),
		}
		out = append(out, tool)
	}
	return out, nil
}

// CallTool invokes the Dify workflow mapped to the named tool.
func (b *WorkflowBackend) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t := b.findTool(name)
	if t == nil {
		return nil, fmt.Errorf("%w: %s", mcprouter.ErrMissingToolName, name)
	}
	inputs := buildInputs(t, args)
	res, err := b.c.RunWorkflow(ctx, WorkflowRequest{
		WorkflowID: t.WorkflowID,
		Inputs:     inputs,
	})
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			IsError: true,
		}, nil
	}
	text := extractResult(res.Outputs)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

func (b *WorkflowBackend) findTool(name string) *WorkflowTool {
	for _, t := range b.tools {
		if t.ToolName == name {
			return t
		}
	}
	return nil
}

// buildInputs maps MCP args to Dify workflow inputs. If the tool declares
// an ArgMap, args are renamed accordingly; otherwise args pass through
// unchanged.
func buildInputs(t *WorkflowTool, args map[string]any) map[string]any {
	if len(t.ArgMap) == 0 {
		return args
	}
	out := map[string]any{}
	for mcpArg, difyKey := range t.ArgMap {
		if v, ok := args[mcpArg]; ok {
			out[difyKey] = v
		}
	}
	for k, v := range args {
		if _, mapped := t.ArgMap[k]; !mapped {
			out[k] = v
		}
	}
	return out
}

// extractResult returns the "result" key from outputs if present, otherwise
// a JSON representation of the whole outputs map.
func extractResult(outputs map[string]any) string {
	if outputs == nil {
		return ""
	}
	if r, ok := outputs["result"].(string); ok {
		return r
	}
	// Fall back to JSON for structured output.
	if len(outputs) > 0 {
		data, err := json.Marshal(outputs)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

// buildWorkflowSchema derives a permissive input schema: each declared
// ArgMap key becomes a string property. If no ArgMap, the schema is an
// empty object.
func buildWorkflowSchema(t *WorkflowTool) map[string]any {
	props := map[string]any{}
	required := []string{}
	for mcpArg := range t.ArgMap {
		props[mcpArg] = map[string]any{"type": "string"}
		required = append(required, mcpArg)
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

var (
	// ErrBackendNameRequired is returned when the backend name is empty.
	ErrBackendNameRequired = errors.New("dify: backend name is required")
	// ErrClientRequired is returned when no Dify client is supplied.
	ErrClientRequired = errors.New("dify: client is required")
	// ErrNoTools is returned when no tools are configured.
	ErrNoTools = errors.New("dify: no tools configured")
)
