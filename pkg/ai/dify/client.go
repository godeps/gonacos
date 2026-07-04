// Package dify provides a minimal HTTP client for the Dify workflow API and
// a Backend that exposes Dify workflows as MCP tools.
package dify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client calls the Dify HTTP API.
type Client struct {
	endpoint  string
	apiKey    string
	http      *http.Client
	mu        sync.RWMutex
	workflows []WorkflowSummary
}

// NewClient creates a Dify client. The endpoint should be the base URL
// (e.g. "https://api.dify.ai"); the apiKey is the app-scoped key.
func NewClient(endpoint, apiKey string) *Client {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	return &Client{
		endpoint: endpoint,
		apiKey:   strings.TrimSpace(apiKey),
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

// Endpoint returns the configured base URL (no trailing slash).
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// APIKey returns the configured API key (masked usage left to callers).
func (c *Client) APIKey() string {
	if c == nil {
		return ""
	}
	return c.apiKey
}

// WorkflowRequest is the body sent to POST /v1/workflows/run.
type WorkflowRequest struct {
	WorkflowID   string         `json:"workflow_id"`
	Inputs       map[string]any `json:"inputs"`
	ResponseMode string         `json:"response_mode,omitempty"`
	User         string         `json:"user,omitempty"`
}

// WorkflowResult is the parsed response from a blocking-mode run.
type WorkflowResult struct {
	WorkflowID string         `json:"workflow_id"`
	TaskID     string         `json:"task_id"`
	Status     string         `json:"status"`
	Outputs    map[string]any `json:"outputs"`
	Error      string         `json:"error,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

// RunWorkflow invokes a Dify workflow in blocking mode and returns the
// result.
func (c *Client) RunWorkflow(ctx context.Context, req WorkflowRequest) (*WorkflowResult, error) {
	if c.endpoint == "" {
		return nil, ErrNotConfigured
	}
	if req.ResponseMode == "" {
		req.ResponseMode = "blocking"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := c.endpoint + "/v1/workflows/run"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrDifyAPIError, resp.StatusCode, string(raw))
	}
	var result WorkflowResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(raw))
	}
	// Dify nests the actual workflow data under "data".
	if result.Data != nil {
		if status, ok := result.Data["status"].(string); ok {
			result.Status = status
		}
		if outputs, ok := result.Data["outputs"].(map[string]any); ok {
			result.Outputs = outputs
		}
		if errMsg, ok := result.Data["error"].(string); ok && errMsg != "" {
			result.Error = errMsg
		}
	}
	if result.Status == "failed" || result.Error != "" {
		return &result, fmt.Errorf("%w: %s", ErrWorkflowFailed, result.Error)
	}
	return &result, nil
}

// WorkflowSummary is a minimal description of a Dify workflow. Since Dify
// doesn't expose a list endpoint, this is populated from local config.
type WorkflowSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ListWorkflows returns locally-configured workflows. Dify's HTTP API does
// not expose a list endpoint; this returns the entries the caller registered
// via SetWorkflows.
func (c *Client) ListWorkflows() []WorkflowSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]WorkflowSummary, len(c.workflows))
	copy(out, c.workflows)
	return out
}

// SetWorkflows replaces the locally-known workflow summaries.
func (c *Client) SetWorkflows(list []WorkflowSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workflows = append([]WorkflowSummary(nil), list...)
}

// DifyToolManifest describes how to wire gonacos's MCP router into a Dify
// HTTP node. This is a convenience struct for the Dify admin UI.
type DifyToolManifest struct {
	RouterURL    string            `json:"routerUrl"`
	ToolName     string            `json:"toolName"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"bodyTemplate,omitempty"`
}

// ExportMCPManifest builds a manifest the admin can display when wiring
// gonacos's MCP router into a Dify workflow's HTTP node.
func ExportMCPManifest(routerURL, toolName string) DifyToolManifest {
	return DifyToolManifest{
		RouterURL: routerURL,
		ToolName:  toolName,
		Method:    "POST",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		BodyTemplate: `{"method":"tools/call","params":{"name":"` + toolName + `","args":{{#args#}}}}`,
	}
}

var (
	// ErrNotConfigured is returned when the endpoint is empty.
	ErrNotConfigured = errors.New("dify: not configured")
	// ErrDifyAPIError is returned when Dify returns a non-2xx status.
	ErrDifyAPIError = errors.New("dify: API error")
	// ErrWorkflowFailed is returned when the workflow status is "failed".
	ErrWorkflowFailed = errors.New("dify: workflow failed")
)
