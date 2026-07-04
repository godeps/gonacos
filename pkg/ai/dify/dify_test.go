package dify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRunWorkflowBlocking verifies a blocking-mode workflow call parses the
// Dify response correctly.
func TestRunWorkflowBlocking(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflows/run" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth header = %q", r.Header.Get("Authorization"))
		}
		var req WorkflowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.WorkflowID != "wf-123" {
			t.Fatalf("workflow_id = %q", req.WorkflowID)
		}
		if req.ResponseMode != "blocking" {
			t.Fatalf("response_mode = %q", req.ResponseMode)
		}
		if req.Inputs["message"] != "hello" {
			t.Fatalf("inputs.message = %v", req.Inputs["message"])
		}
		_, _ = io.WriteString(w, `{
			"workflow_id": "wf-123",
			"task_id": "task-456",
			"data": {
				"status": "succeeded",
				"outputs": {"result": "world"}
			}
		}`)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "test-key")
	res, err := c.RunWorkflow(context.Background(), WorkflowRequest{
		WorkflowID: "wf-123",
		Inputs:     map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.Outputs["result"] != "world" {
		t.Fatalf("outputs.result = %v", res.Outputs["result"])
	}
}

// TestRunWorkflowError verifies non-2xx responses are surfaced as errors.
func TestRunWorkflowError(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error": "invalid api key"}`)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "bad-key")
	_, err := c.RunWorkflow(context.Background(), WorkflowRequest{WorkflowID: "wf"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrDifyAPIError) {
		t.Fatalf("err = %v, want ErrDifyAPIError", err)
	}
}

// TestRunWorkflowFailedStatus verifies a "failed" workflow status is surfaced
// as an error.
func TestRunWorkflowFailedStatus(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"data": {
				"status": "failed",
				"error": "input validation failed"
			}
		}`)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "key")
	_, err := c.RunWorkflow(context.Background(), WorkflowRequest{WorkflowID: "wf"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrWorkflowFailed) {
		t.Fatalf("err = %v, want ErrWorkflowFailed", err)
	}
}

// TestRunWorkflowNotConfigured verifies an empty endpoint returns
// ErrNotConfigured.
func TestRunWorkflowNotConfigured(t *testing.T) {
	t.Parallel()
	c := NewClient("", "key")
	_, err := c.RunWorkflow(context.Background(), WorkflowRequest{})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// TestSetAndListWorkflows verifies the local workflow registry.
func TestSetAndListWorkflows(t *testing.T) {
	t.Parallel()
	c := NewClient("https://example.com", "key")
	c.SetWorkflows([]WorkflowSummary{
		{ID: "wf-1", Name: "Workflow 1"},
		{ID: "wf-2", Name: "Workflow 2"},
	})
	list := c.ListWorkflows()
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].ID != "wf-1" {
		t.Fatalf("first = %q", list[0].ID)
	}
}

// TestExportMCPManifest verifies the manifest includes the router URL and
// tool name.
func TestExportMCPManifest(t *testing.T) {
	t.Parallel()
	m := ExportMCPManifest("https://gonacos.example.com/v3/ai/mcp/router", "dify.run")
	if m.RouterURL != "https://gonacos.example.com/v3/ai/mcp/router" {
		t.Fatalf("routerURL = %q", m.RouterURL)
	}
	if m.ToolName != "dify.run" {
		t.Fatalf("toolName = %q", m.ToolName)
	}
	if m.Method != "POST" {
		t.Fatalf("method = %q", m.Method)
	}
	if m.Headers["Content-Type"] != "application/json" {
		t.Fatalf("content-type = %q", m.Headers["Content-Type"])
	}
	if !strings.Contains(m.BodyTemplate, "dify.run") {
		t.Fatalf("bodyTemplate missing tool name: %q", m.BodyTemplate)
	}
}

// TestWorkflowBackendCallTool verifies the backend routes a tool call to the
// Dify client and extracts the result.
func TestWorkflowBackendCallTool(t *testing.T) {
	t.Parallel()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"data": {
				"status": "succeeded",
				"outputs": {"result": "the answer is 42"}
			}
		}`)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "key")
	backend, err := NewWorkflowBackend("dify", []*WorkflowTool{
		{ToolName: "ask", WorkflowID: "wf-1", Description: "Ask Dify"},
	}, c)
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if backend.Name() != "dify" {
		t.Fatalf("name = %q", backend.Name())
	}

	tools, err := backend.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ask" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := backend.CallTool(context.Background(), "ask", map[string]any{"q": "what is the answer"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	if len(res.Content) == 0 {
		t.Fatalf("no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "the answer is 42") {
		t.Fatalf("text = %q", tc.Text)
	}
}

// TestWorkflowBackendArgMap verifies the ArgMap renames MCP args to Dify
// inputs.
func TestWorkflowBackendArgMap(t *testing.T) {
	t.Parallel()
	var capturedInputs map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req WorkflowRequest
		json.NewDecoder(r.Body).Decode(&req)
		capturedInputs = req.Inputs
		_, _ = io.WriteString(w, `{"data":{"status":"succeeded","outputs":{"result":"ok"}}}`)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "key")
	backend, _ := NewWorkflowBackend("dify", []*WorkflowTool{
		{
			ToolName:   "ask",
			WorkflowID: "wf-1",
			ArgMap:     map[string]string{"question": "user_query"},
		},
	}, c)
	_, _ = backend.CallTool(context.Background(), "ask", map[string]any{"question": "hello"})
	if capturedInputs["user_query"] != "hello" {
		t.Fatalf("inputs = %+v", capturedInputs)
	}
	if _, ok := capturedInputs["question"]; ok {
		t.Fatalf("question should be renamed")
	}
}

// TestWorkflowBackendMissingTool verifies calling an unknown tool returns
// ErrMissingToolName.
func TestWorkflowBackendMissingTool(t *testing.T) {
	t.Parallel()
	c := NewClient("https://example.com", "key")
	backend, _ := NewWorkflowBackend("dify", []*WorkflowTool{
		{ToolName: "ask", WorkflowID: "wf-1"},
	}, c)
	_, err := backend.CallTool(context.Background(), "ghost", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, mcprouter.ErrMissingToolName) {
		t.Fatalf("err = %v, want ErrMissingToolName", err)
	}
}

// TestNewWorkflowBackendValidation verifies constructor guards.
func TestNewWorkflowBackendValidation(t *testing.T) {
	t.Parallel()
	c := NewClient("https://example.com", "key")
	if _, err := NewWorkflowBackend("", nil, c); !errors.Is(err, ErrBackendNameRequired) {
		t.Fatalf("empty name: err = %v", err)
	}
	if _, err := NewWorkflowBackend("x", nil, nil); !errors.Is(err, ErrClientRequired) {
		t.Fatalf("nil client: err = %v", err)
	}
	if _, err := NewWorkflowBackend("x", nil, c); !errors.Is(err, ErrNoTools) {
		t.Fatalf("no tools: err = %v", err)
	}
}
