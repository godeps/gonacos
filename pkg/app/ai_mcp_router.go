package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/ai/mcpclient"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/godeps/gonacos/pkg/protocol"
)

// registerMcpRouterRoutes mounts the MCP streamable HTTP router and its
// admin backends API. The streamable HTTP endpoint accepts POST (JSON-RPC
// request), GET (SSE stream), and DELETE (terminate session) verbs.
func registerMcpRouterRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	router := h.service.McpRouter()
	if router == nil {
		return
	}
	// Streamable HTTP transport: POST/GET/DELETE on a single endpoint.
	for _, m := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		register(m, "/v3/ai/mcp/router", h.mcpRouterProxy)
	}
	// Backend management API.
	register(http.MethodGet, "/v3/admin/ai/mcp/router/backends", h.mcpRouterListBackends)
	register(http.MethodPost, "/v3/admin/ai/mcp/router/backends", h.mcpRouterAddBackend)
	register(http.MethodDelete, "/v3/admin/ai/mcp/router/backends", h.mcpRouterRemoveBackend)
}

// mcpRouterProxy forwards the request to the mcprouter handler. The router
// is the authority on session management and JSON-RPC dispatch.
func (h aiHandler) mcpRouterProxy(w http.ResponseWriter, r *http.Request) {
	router := h.service.McpRouter()
	if router == nil {
		writeMcpRouterError(w, http.StatusServiceUnavailable, "mcp router not configured")
		return
	}
	router.Handler().ServeHTTP(w, r)
}

// mcpRouterListBackends returns the names of all mounted backends.
func (h aiHandler) mcpRouterListBackends(w http.ResponseWriter, r *http.Request) {
	router := h.service.McpRouter()
	if router == nil {
		writeMcpRouterError(w, http.StatusServiceUnavailable, "mcp router not configured")
		return
	}
	names := router.ListBackends()
	protocol.WriteResult(w, http.StatusOK, names)
}

// mcpRouterAddBackend mounts a remote MCP server as a backend. The body
// is a JSON object with name, url, bearerToken, and optional headers.
func (h aiHandler) mcpRouterAddBackend(w http.ResponseWriter, r *http.Request) {
	router := h.service.McpRouter()
	if router == nil {
		writeMcpRouterError(w, http.StatusServiceUnavailable, "mcp router not configured")
		return
	}
	var req struct {
		Name        string            `json:"name"`
		URL         string            `json:"url"`
		BearerToken string            `json:"bearerToken"`
		Headers     map[string]string `json:"headers"`
	}
	if err := decodeJSONRequest(r, &req); err != nil {
		writeMcpRouterError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" || req.URL == "" {
		writeMcpRouterError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, req.URL, mcpclient.DialOptions{
		BearerToken:          req.BearerToken,
		Headers:              req.Headers,
		DisableStandaloneSSE: true,
	})
	if err != nil {
		writeMcpRouterError(w, http.StatusBadGateway, "dial remote mcp: "+err.Error())
		return
	}
	backend, err := mcprouter.NewRemoteBackend(req.Name, client)
	if err != nil {
		_ = client.Close()
		writeMcpRouterError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := router.AddBackend(backend); err != nil {
		_ = client.Close()
		writeMcpRouterError(w, http.StatusBadRequest, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]string{"name": req.Name, "url": req.URL})
}

// mcpRouterRemoveBackend unmounts a backend by name. Backends mounted
// from the admin API are closed; backends mounted programmatically by
// the server are not (their lifecycle is owned by the caller).
func (h aiHandler) mcpRouterRemoveBackend(w http.ResponseWriter, r *http.Request) {
	router := h.service.McpRouter()
	if router == nil {
		writeMcpRouterError(w, http.StatusServiceUnavailable, "mcp router not configured")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeMcpRouterError(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	if err := router.RemoveBackend(name); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, mcprouter.ErrBackendNotFound) {
			status = http.StatusNotFound
		}
		writeMcpRouterError(w, status, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, "removed backend "+name)
}

func writeMcpRouterError(w http.ResponseWriter, status int, msg string) {
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

// decodeJSONRequest reads a JSON body into dst. Empty bodies are rejected.
func decodeJSONRequest(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid json: " + err.Error())
	}
	return nil
}
