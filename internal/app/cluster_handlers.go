package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/saker-ai/gonacos/internal/cluster"
	"github.com/saker-ai/gonacos/internal/protocol"
)

type clusterHandler struct {
	service *cluster.Service
}

func registerClusterRoutes(register func(string, string, http.HandlerFunc), service *cluster.Service) {
	h := clusterHandler{service: service}

	register(http.MethodGet, "/v3/admin/core/cluster/node/list", h.clusterNodeList)
	register(http.MethodPut, "/v3/admin/core/cluster/node/list", h.clusterNodeListUpdate)
	register(http.MethodGet, "/v3/admin/core/cluster/node/self", h.clusterNodeSelf)
	register(http.MethodGet, "/v3/admin/core/cluster/lookup", h.clusterLookup)
	register(http.MethodPut, "/v3/admin/core/cluster/lookup", h.clusterLookupUpdate)
	register(http.MethodGet, "/v3/console/core/cluster/nodes", h.clusterNodeList)
	register(http.MethodGet, "/v3/admin/core/cluster/snapshot", h.clusterSnapshot)
	register(http.MethodPost, "/v3/admin/core/cluster/restore", h.clusterRestore)
	register(http.MethodGet, "/v3/admin/core/cluster/status", h.clusterStatus)

	register(http.MethodGet, "/v3/admin/core/plugin/list", h.pluginList)
	register(http.MethodGet, "/v3/admin/core/plugin/detail", h.pluginDetail)
	register(http.MethodPut, "/v3/admin/core/plugin/config", h.pluginConfigUpdate)
	register(http.MethodPut, "/v3/admin/core/plugin/status", h.pluginStatusUpdate)
	register(http.MethodGet, "/v3/console/plugin", h.pluginDetail)
	register(http.MethodGet, "/v3/console/plugin/list", h.pluginList)
	register(http.MethodGet, "/v3/console/plugin/availability", h.pluginAvailability)
	register(http.MethodGet, "/v3/console/plugin/config", h.pluginConfigGet)
	register(http.MethodGet, "/v3/console/plugin/status", h.pluginStatusGet)

	register(http.MethodGet, "/v3/admin/core/ops/ids", h.opsIDs)
	register(http.MethodPut, "/v3/admin/core/ops/log", h.opsLogUpdate)
	register(http.MethodPut, "/v3/admin/cs/ops/log", h.opsLogUpdate)
	register(http.MethodPut, "/v3/admin/ns/ops/log", h.opsLogUpdate)
	register(http.MethodPost, "/v3/admin/core/ops/raft", h.opsRaft)

	register(http.MethodGet, "/v3/admin/core/loader/cluster", h.loaderCluster)
	register(http.MethodGet, "/v3/admin/core/loader/current", h.loaderCurrent)
	register(http.MethodPost, "/v3/admin/core/loader/reloadClient", h.loaderReloadClient)
	register(http.MethodPost, "/v3/admin/core/loader/reloadCurrent", h.loaderReloadCurrent)
	register(http.MethodPost, "/v3/admin/core/loader/smartReloadCluster", h.loaderSmartReload)
}

func (h clusterHandler) clusterNodeList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListMembers())
}

func (h clusterHandler) clusterNodeListUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	members, err := parseMembers(formValue(r, "members"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	result, err := h.service.UpdateNodes(members)
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h clusterHandler) clusterNodeSelf(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.Self())
}

func (h clusterHandler) clusterLookup(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.ListMembers())
}

func (h clusterHandler) clusterLookupUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	result, err := h.service.UpdateLookup(formValue(r, "type"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h clusterHandler) clusterSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := h.service.Snapshot()
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, snap)
}

func (h clusterHandler) clusterRestore(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterMissing,
			Message: "request body is required",
		})
		return
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: "invalid JSON body: " + err.Error(),
		})
		return
	}
	if payload.Data == nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterMissing,
			Message: "data field is required",
		})
		return
	}
	if err := h.service.Restore(payload.Data); err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"restored": h.service.SnapshotKey(),
	})
}

func (h clusterHandler) clusterStatus(w http.ResponseWriter, r *http.Request) {
	self := h.service.Self()
	status := map[string]any{
		"mode":              string(h.service.Mode()),
		"self":              self,
		"memberCount":       len(h.service.ListMembers()),
		"snapshotAvailable": true,
		"snapshotKey":       h.service.SnapshotKey(),
		"logLevel":          h.service.LogLevel(),
	}
	protocol.WriteResult(w, http.StatusOK, status)
}

func (h clusterHandler) pluginList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListPlugins())
}

func (h clusterHandler) pluginDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	plugin, err := h.service.GetPlugin(formValue(r, "pluginId"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin)
}

func (h clusterHandler) pluginConfigGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	plugin, err := h.service.GetPlugin(formValue(r, "pluginId"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin.Config)
}

func (h clusterHandler) pluginConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	config, err := parseMetadataMap(formValue(r, "config"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	plugin, err := h.service.UpdatePluginConfig(formValue(r, "pluginId"), config)
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin)
}

func (h clusterHandler) pluginStatusGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	plugin, err := h.service.GetPlugin(formValue(r, "pluginId"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin.Status)
}

func (h clusterHandler) pluginStatusUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	enabled := strings.EqualFold(strings.TrimSpace(formValue(r, "enabled")), "true")
	plugin, err := h.service.UpdatePluginStatus(formValue(r, "pluginId"), enabled)
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin)
}

func (h clusterHandler) pluginAvailability(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	plugins := h.service.ListPlugins()
	out := make([]map[string]any, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, map[string]any{
			"pluginId":  p.ID,
			"name":      p.Name,
			"available": p.Available,
		})
	}
	protocol.WriteResult(w, http.StatusOK, out)
}

func (h clusterHandler) opsIDs(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.IDs())
}

func (h clusterHandler) opsLogUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	h.service.SetLogLevel(formValue(r, "logLevel"))
	protocol.WriteResult(w, http.StatusOK, h.service.LogLevel())
}

func (h clusterHandler) opsRaft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	result, err := h.service.RaftOps(formValue(r, "command"), formValue(r, "groupId"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h clusterHandler) loaderCluster(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.LoaderMetrics())
}

func (h clusterHandler) loaderCurrent(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.CurrentClients())
}

func (h clusterHandler) loaderReloadClient(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ReloadSingle(formValue(r, "clientId")))
}

func (h clusterHandler) loaderReloadCurrent(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.ReloadCount())
}

func (h clusterHandler) loaderSmartReload(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.service.SmartReload())
}

func parseMembers(s string) ([]cluster.Member, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var members []cluster.Member
	if err := json.Unmarshal([]byte(s), &members); err != nil {
		return nil, err
	}
	return members, nil
}

func writeClusterError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, cluster.ErrMissingMemberID),
		errors.Is(err, cluster.ErrMissingPluginID):
		code = protocol.CodeParameterMissing
	case errors.Is(err, cluster.ErrMemberNotFound),
		errors.Is(err, cluster.ErrPluginNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, cluster.ErrNotClusterMode):
		status = http.StatusConflict
		code = protocol.CodeConflict
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
