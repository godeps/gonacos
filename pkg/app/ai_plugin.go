package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/plugin"
	"github.com/godeps/gonacos/pkg/protocol"
)

// registerPluginRoutes mounts the plugin admin API. All endpoints are admin-only.
func registerPluginRoutes(register func(string, string, http.HandlerFunc), admin aiHandler) {
	base := "/v3/admin/ai/plugins"
	register(http.MethodGet, base+"/list", admin.pluginList)
	register(http.MethodGet, base+"/detail", admin.pluginDetail)
	register(http.MethodPost, base+"/enable", admin.pluginEnable)
	register(http.MethodPost, base+"/disable", admin.pluginDisable)
	register(http.MethodPost, base+"/config", admin.pluginSetConfig)
	register(http.MethodPost, base+"/tools/list", admin.pluginToolsList)
	register(http.MethodPost, base+"/tools/call", admin.pluginToolsCall)
}

// pluginList returns all registered plugins with their enabled state.
func (h aiHandler) pluginList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	protocol.WriteResult(w, http.StatusOK, mgr.List())
}

// pluginDetail returns a single plugin's metadata and enabled state.
func (h aiHandler) pluginDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	info, err := mgr.Get(id)
	if err != nil {
		writePluginError(w, http.StatusNotFound, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, info)
}

// pluginEnable enables a plugin.
func (h aiHandler) pluginEnable(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*1e9)
	defer cancel()
	if err := mgr.Enable(ctx, id); err != nil {
		writePluginError(w, http.StatusBadRequest, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"pluginId": id,
		"status":   "enabled",
	})
}

// pluginDisable disables a plugin.
func (h aiHandler) pluginDisable(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	mgr.Disable(id)
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"pluginId": id,
		"status":   "disabled",
	})
}

// pluginSetConfig updates a plugin's config (which disables+re-inits it).
func (h aiHandler) pluginSetConfig(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	var cfg plugin.Config
	if raw := formValue(r, "config"); strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			writePluginError(w, http.StatusBadRequest, "invalid config json: "+err.Error())
			return
		}
	}
	if err := mgr.SetConfig(id, cfg); err != nil {
		writePluginError(w, http.StatusBadRequest, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"pluginId": id,
		"status":   "reconfigured",
	})
}

// pluginToolsList lists the tools exposed by an enabled plugin.
func (h aiHandler) pluginToolsList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	backend, err := plugin.NewPluginBackend(id, mgr)
	if err != nil {
		writePluginError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*1e9)
	defer cancel()
	tools, err := backend.ListTools(ctx)
	if err != nil {
		writePluginError(w, http.StatusBadGateway, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, tools)
}

// pluginToolsCall invokes a tool exposed by a plugin.
func (h aiHandler) pluginToolsCall(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	mgr := h.service.Plugins()
	if mgr == nil {
		writePluginError(w, http.StatusServiceUnavailable, "plugin manager not configured")
		return
	}
	id := formValue(r, "pluginId")
	if strings.TrimSpace(id) == "" {
		writePluginError(w, http.StatusBadRequest, "pluginId is required")
		return
	}
	toolName := formValue(r, "toolName")
	if strings.TrimSpace(toolName) == "" {
		writePluginError(w, http.StatusBadRequest, "toolName is required")
		return
	}
	var args map[string]any
	if raw := formValue(r, "args"); strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			writePluginError(w, http.StatusBadRequest, "invalid args json: "+err.Error())
			return
		}
	}
	backend, err := plugin.NewPluginBackend(id, mgr)
	if err != nil {
		writePluginError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*1e9)
	defer cancel()
	res, err := backend.CallTool(ctx, toolName, args)
	if err != nil {
		writePluginError(w, http.StatusBadGateway, err.Error())
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func writePluginError(w http.ResponseWriter, status int, msg string) {
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
	if errors.Is(nil, plugin.ErrPluginNotFound) {
		// no-op; just here so `errors` import is used even if future code
		// switches on typed errors below.
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: msg,
	})
}
