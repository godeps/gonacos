package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/dify"
	"github.com/godeps/gonacos/pkg/protocol"
)

// registerDifyRoutes mounts the Dify admin API. All endpoints are admin-only.
func registerDifyRoutes(register func(string, string, http.HandlerFunc), admin aiHandler) {
	register(http.MethodGet, "/v3/admin/ai/dify/config", admin.difyConfigGet)
	register(http.MethodPost, "/v3/admin/ai/dify/config", admin.difyConfigSet)
	register(http.MethodGet, "/v3/admin/ai/dify/workflows/list", admin.difyWorkflowsList)
	register(http.MethodPost, "/v3/admin/ai/dify/workflows/run", admin.difyWorkflowsRun)
	register(http.MethodPost, "/v3/admin/ai/dify/workflows/import", admin.difyWorkflowsImport)
	register(http.MethodGet, "/v3/admin/ai/dify/manifest", admin.difyManifest)
}

// difyConfigGet returns whether Dify is configured.
func (h aiHandler) difyConfigGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	c := h.service.DifyClient()
	configured := c != nil
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"configured": configured,
		"endpoint":   difyEndpoint(c),
	})
}

// difyConfigSet updates the Dify endpoint + API key at runtime.
func (h aiHandler) difyConfigSet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	endpoint := formValue(r, "endpoint")
	apiKey := formValue(r, "apiKey")
	if strings.TrimSpace(endpoint) == "" {
		writeDifyError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	c := dify.NewClient(endpoint, apiKey)
	// Replace the client on the service. This is safe because DifyClient()
	// just returns the pointer; callers should hold the pointer for the
	// duration of a request.
	h.service.SetDifyClient(c)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"configured": true,
		"endpoint":   endpoint,
	})
}

// difyWorkflowsList returns locally-configured Dify workflows.
func (h aiHandler) difyWorkflowsList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	c := h.service.DifyClient()
	if c == nil {
		writeDifyError(w, http.StatusServiceUnavailable, "dify not configured")
		return
	}
	protocol.WriteResult(w, http.StatusOK, c.ListWorkflows())
}

// difyWorkflowsRun invokes a Dify workflow by ID.
func (h aiHandler) difyWorkflowsRun(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	c := h.service.DifyClient()
	if c == nil {
		writeDifyError(w, http.StatusServiceUnavailable, "dify not configured")
		return
	}
	workflowID := formValue(r, "workflowId")
	if strings.TrimSpace(workflowID) == "" {
		writeDifyError(w, http.StatusBadRequest, "workflowId is required")
		return
	}
	user := formValue(r, "user")
	var inputs map[string]any
	if raw := formValue(r, "inputs"); strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
			writeDifyError(w, http.StatusBadRequest, "invalid inputs json: "+err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*1e9)
	defer cancel()
	res, err := c.RunWorkflow(ctx, dify.WorkflowRequest{
		WorkflowID: workflowID,
		Inputs:     inputs,
		User:       user,
	})
	if err != nil {
		writeDifyError(w, http.StatusBadGateway, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// difyWorkflowsImport registers a Dify workflow as a backend on the MCP
// router. The body is a JSON WorkflowTool + server name.
func (h aiHandler) difyWorkflowsImport(w http.ResponseWriter, r *http.Request) {
	router := h.service.McpRouter()
	if router == nil {
		writeDifyError(w, http.StatusServiceUnavailable, "mcp router not configured")
		return
	}
	c := h.service.DifyClient()
	if c == nil {
		writeDifyError(w, http.StatusServiceUnavailable, "dify not configured")
		return
	}
	var req struct {
		ServerName string               `json:"serverName"`
		Tools      []*dify.WorkflowTool `json:"tools"`
	}
	if err := decodeJSONRequest(r, &req); err != nil {
		writeDifyError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ServerName) == "" || len(req.Tools) == 0 {
		writeDifyError(w, http.StatusBadRequest, "serverName and tools are required")
		return
	}
	backend, err := dify.NewWorkflowBackend(req.ServerName, req.Tools, c)
	if err != nil {
		writeDifyError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := router.AddBackend(backend); err != nil {
		writeDifyError(w, http.StatusBadRequest, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"name":   req.ServerName,
		"status": "mounted",
	})
}

// difyManifest returns the Dify tool manifest for wiring gonacos's MCP
// router into a Dify HTTP node.
func (h aiHandler) difyManifest(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	routerURL := formValue(r, "routerUrl")
	toolName := formValue(r, "toolName")
	if strings.TrimSpace(routerURL) == "" {
		routerURL = "/v3/ai/mcp/router"
	}
	if strings.TrimSpace(toolName) == "" {
		toolName = "dify.run"
	}
	manifest := dify.ExportMCPManifest(routerURL, toolName)
	protocol.WriteResult(w, http.StatusOK, manifest)
}

func difyEndpoint(c *dify.Client) string {
	if c == nil {
		return ""
	}
	return c.Endpoint()
}

func writeDifyError(w http.ResponseWriter, status int, msg string) {
	code := protocol.CodeParameterValidateError
	switch status {
	case http.StatusNotFound:
		code = protocol.CodeNotFound
	case http.StatusConflict:
		code = protocol.CodeConflict
	case http.StatusServiceUnavailable, http.StatusNotImplemented:
		code = protocol.CodeNotImplemented
	case http.StatusBadGateway:
		code = protocol.CodeNotImplemented
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: msg,
	})
}
