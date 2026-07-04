package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/protocol"
)

// registerApitomcpRoutes mounts the API→MCP admin API. Console gets
// read-only list/detail; admin gets full CRUD plus validate/tools endpoints.
func registerApitomcpRoutes(register func(string, string, http.HandlerFunc), admin, console aiHandler) {
	for _, base := range []string{"/v3/admin/ai/apitomcp", "/v3/console/ai/apitomcp"} {
		register(http.MethodGet, base+"/list", admin.apitomcpList)
		register(http.MethodGet, base+"/detail", admin.apitomcpDetail)
	}
	register(http.MethodPost, "/v3/admin/ai/apitomcp/create", admin.apitomcpCreate)
	register(http.MethodPut, "/v3/admin/ai/apitomcp/update", admin.apitomcpUpdate)
	register(http.MethodDelete, "/v3/admin/ai/apitomcp/delete", admin.apitomcpDelete)
	register(http.MethodPost, "/v3/admin/ai/apitomcp/validate", admin.apitomcpValidate)
	register(http.MethodGet, "/v3/admin/ai/apitomcp/tools/list", admin.apitomcpToolsList)
	register(http.MethodPost, "/v3/admin/ai/apitomcp/tools/call", admin.apitomcpToolsCall)
	// Console alias for read-only access.
	register(http.MethodGet, "/v3/console/ai/apitomcp/list", console.apitomcpList)
	register(http.MethodGet, "/v3/console/ai/apitomcp/detail", console.apitomcpDetail)
}

// apitomcpList returns all stored apitomcp configs.
func (h aiHandler) apitomcpList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListApitomcpConfigs())
}

// apitomcpDetail returns a single config by name.
func (h aiHandler) apitomcpDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	cfg, err := h.service.GetApitomcpConfig(formValue(r, "name"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, cfg)
}

// apitomcpCreate stores a new config from YAML and mounts it on the router.
func (h aiHandler) apitomcpCreate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	cfg, err := h.service.CreateApitomcpConfig(formValue(r, "yaml"), formValue(r, "description"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, cfg)
}

// apitomcpUpdate replaces an existing config's YAML and remounts the backend.
func (h aiHandler) apitomcpUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	cfg, err := h.service.UpdateApitomcpConfig(formValue(r, "name"), formValue(r, "yaml"), formValue(r, "description"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, cfg)
}

// apitomcpDelete removes a config and unmounts its backend.
func (h aiHandler) apitomcpDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteApitomcpConfig(formValue(r, "name")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

// apitomcpValidate parses YAML without persisting and returns the server
// name and tool count.
func (h aiHandler) apitomcpValidate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	serverName, toolCount, err := h.service.ValidateApitomcpYAML(formValue(r, "yaml"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"serverName": serverName,
		"toolCount":  toolCount,
		"valid":      true,
	})
}

// apitomcpToolsList lists the tools defined in the config without going
// through the router.
func (h aiHandler) apitomcpToolsList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	backend, err := h.service.ApitomcpBackendFor(formValue(r, "name"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tools, err := backend.ListTools(ctx)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, tools)
}

// apitomcpToolsCall invokes a tool defined in the config. The body is JSON:
// {"tool": "<toolName>", "args": {...}}.
func (h aiHandler) apitomcpToolsCall(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	name := formValue(r, "name")
	if strings.TrimSpace(name) == "" {
		writeAIError(w, ai.ErrApitomcpConfigNameRequired)
		return
	}
	var req struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	if err := decodeJSONRequest(r, &req); err != nil {
		writeApitomcpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Tool) == "" {
		writeApitomcpError(w, http.StatusBadRequest, "tool is required")
		return
	}
	backend, err := h.service.ApitomcpBackendFor(name)
	if err != nil {
		writeAIError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := backend.CallTool(ctx, req.Tool, req.Args)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// writeApitomcpError maps an HTTP status to a protocol error code and writes
// the response. Used for JSON-body validation errors that bypass writeAIError
// (which expects ai-package errors).
func writeApitomcpError(w http.ResponseWriter, status int, msg string) {
	code := protocol.CodeParameterValidateError
	switch status {
	case http.StatusNotFound:
		code = protocol.CodeNotFound
	case http.StatusConflict:
		code = protocol.CodeConflict
	case http.StatusServiceUnavailable, http.StatusNotImplemented:
		code = protocol.CodeNotImplemented
	case http.StatusUnauthorized:
		code = protocol.CodeAccessDenied
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: msg,
	})
}
