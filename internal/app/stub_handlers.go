package app

import (
	"errors"
	"net/http"
	"strconv"

	aivsvc "github.com/godeps/gonacos/internal/ai"
	clustersvc "github.com/godeps/gonacos/internal/cluster"
	configsvc "github.com/godeps/gonacos/internal/config"
	namingsvc "github.com/godeps/gonacos/internal/naming"
	"github.com/godeps/gonacos/internal/protocol"
)

// stubHandler bundles the service references needed by the remaining
// OpenAPI operations that were previously 501 stubs. Each handler here
// either implements the operation against live service state or returns
// a pragmatic response (e.g. derby ops are not applicable to a Go server).
type stubHandler struct {
	config  *configsvc.Service
	naming  *namingsvc.Service
	ai      *aivsvc.Service
	cluster *clustersvc.Service
}

func registerStubRoutes(
	register func(string, string, http.HandlerFunc),
	config *configsvc.Service,
	naming *namingsvc.Service,
	ai *aivsvc.Service,
	cluster *clustersvc.Service,
) {
	h := stubHandler{config: config, naming: naming, ai: ai, cluster: cluster}

	// Config metadata + content search.
	register(http.MethodPut, "/v3/admin/cs/config/metadata", h.publishConfigMetadata)
	register(http.MethodGet, "/v3/console/cs/config/searchDetail", h.searchConfigByContent)

	// Derby ops — not applicable (Go server has no embedded Derby DB).
	register(http.MethodGet, "/v3/admin/cs/ops/derby", h.derbyOps)
	register(http.MethodPost, "/v3/admin/cs/ops/derby/import", h.importDerby)

	// Naming client tracking.
	register(http.MethodGet, "/v3/admin/ns/client", h.clientDetail)
	register(http.MethodGet, "/v3/admin/ns/client/distro", h.clientDistro)
	register(http.MethodGet, "/v3/admin/ns/client/list", h.clientList)
	register(http.MethodGet, "/v3/admin/ns/client/publish/list", h.publishedServiceList)
	register(http.MethodGet, "/v3/admin/ns/client/service/publisher/list", h.publishedClientList)
	register(http.MethodGet, "/v3/admin/ns/client/service/subscriber/list", h.subscriberClientList)
	register(http.MethodGet, "/v3/admin/ns/client/subscribe/list", h.subscribedServiceList)

	// Naming health + ops.
	register(http.MethodPut, "/v3/admin/ns/health/instance", h.updateInstanceHealth)
	register(http.MethodGet, "/v3/admin/ns/ops/metrics", h.namingMetrics)
	register(http.MethodGet, "/v3/admin/ns/ops/switches", h.getSwitches)
	register(http.MethodPut, "/v3/admin/ns/ops/switches", h.updateSwitches)

	// AI agent spec version metadata.
	register(http.MethodGet, "/v3/admin/ai/agentspecs/version/meta", h.agentSpecVersionMeta)

	// Plugin config + status.
	register(http.MethodPut, "/v3/console/plugin/config", h.updatePluginConfig)
	register(http.MethodPut, "/v3/console/plugin/status", h.updatePluginStatus)
}

// --- Config ---

func (h stubHandler) publishConfigMetadata(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.config.UpdateMetadata(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		formValue(r, "desc"),
		formValue(r, "configTags"),
	); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h stubHandler) searchConfigByContent(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.config.SearchByContent(
		formValue(r, "namespaceId"),
		formValue(r, "content"),
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

// --- Derby ops (not applicable) ---

func (h stubHandler) derbyOps(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"applicable": false,
		"reason":     "gonacos does not embed a Derby database; this endpoint is a no-op",
	})
}

func (h stubHandler) importDerby(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"applicable": false,
		"reason":     "gonacos does not embed a Derby database; import is not supported",
	})
}

// --- Naming client tracking ---

func (h stubHandler) clientList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	clients := h.naming.ListClients(formValue(r, "clientId"))
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"clientId": formValue(r, "clientId"),
		"clients":  clients,
		"count":    len(clients),
	})
}

func (h stubHandler) clientDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	clientID := formValue(r, "clientId")
	if clientID == "" {
		clientID = formValue(r, "clientIp")
	}
	detail, err := h.naming.GetClient(clientID)
	if err != nil {
		writeNamingStubError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, detail)
}

func (h stubHandler) clientDistro(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	// In standalone mode, the current server is responsible for all clients.
	// In cluster mode, this would consult the distro mapper.
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"clientId":     formValue(r, "clientId"),
		"responsible":  "self",
		"distroStatus": "enabled",
	})
}

func (h stubHandler) publishedServiceList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	services := h.naming.PublishedServiceList(formValue(r, "clientId"))
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"clientId": formValue(r, "clientId"),
		"count":    len(services),
		"services": services,
	})
}

func (h stubHandler) subscribedServiceList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	services := h.naming.SubscribedServiceList(formValue(r, "clientId"))
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"clientId": formValue(r, "clientId"),
		"count":    len(services),
		"services": services,
	})
}

func (h stubHandler) publishedClientList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	clients := h.naming.PublisherClients(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
	)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"namespaceId": formValue(r, "namespaceId"),
		"groupName":   formValue(r, "groupName"),
		"serviceName": formValue(r, "serviceName"),
		"count":       len(clients),
		"clients":     clients,
	})
}

func (h stubHandler) subscriberClientList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	clients := h.naming.SubscriberClients(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
	)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"namespaceId": formValue(r, "namespaceId"),
		"groupName":   formValue(r, "groupName"),
		"serviceName": formValue(r, "serviceName"),
		"count":       len(clients),
		"clients":     clients,
	})
}

// --- Naming health + ops ---

func (h stubHandler) updateInstanceHealth(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	healthy := formValue(r, "healthy") == "true" || formValue(r, "healthy") == "1"
	port, _ := strconv.Atoi(formValue(r, "port"))
	if err := h.naming.UpdateInstanceHealth(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		formValue(r, "clusterName"),
		formValue(r, "ip"),
		port,
		healthy,
	); err != nil {
		writeNamingStubError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h stubHandler) namingMetrics(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, h.naming.Metrics())
}

func (h stubHandler) getSwitches(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, defaultNamingSwitches())
}

func (h stubHandler) updateSwitches(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	// Switches are runtime feature flags in Nacos Java. The Go server does not
	// implement configurable switches; accept the request and echo the input
	// so the console UI gets a 200 rather than a 501.
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"accepted": true,
		"entry":    formValue(r, "entry"),
		"value":    formValue(r, "value"),
		"reason":   "runtime switches are not configurable in gonacos; request accepted as no-op",
	})
}

// --- AI ---

func (h stubHandler) agentSpecVersionMeta(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	v, err := h.ai.GetAgentSpecVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIStubError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"id":          formValue(r, "id"),
		"version":     v.Version,
		"metadata":    v.Metadata,
		"md5":         v.MD5,
		"author":      v.Author,
		"publishedAt": v.PublishedAt,
		"labels":      v.Labels,
		"bizTags":     v.BizTags,
		"description": v.Description,
	})
}

// --- Plugin ---

func (h stubHandler) updatePluginConfig(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	pluginID := formValue(r, "pluginId")
	if pluginID == "" {
		pluginID = formValue(r, "id")
	}
	config := extractPluginConfig(r)
	plugin, err := h.cluster.UpdatePluginConfig(pluginID, config)
	if err != nil {
		writeClusterStubError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin)
}

func (h stubHandler) updatePluginStatus(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	pluginID := formValue(r, "pluginId")
	if pluginID == "" {
		pluginID = formValue(r, "id")
	}
	enabled := formValue(r, "enabled") == "true" || formValue(r, "enabled") == "1"
	plugin, err := h.cluster.UpdatePluginStatus(pluginID, enabled)
	if err != nil {
		writeClusterStubError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, plugin)
}

// --- helpers ---

func extractPluginConfig(r *http.Request) map[string]string {
	config := map[string]string{}
	const prefix = "config."
	for key, vals := range r.Form {
		if len(vals) == 0 {
			continue
		}
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			config[key[len(prefix):]] = vals[0]
		}
	}
	return config
}

func defaultNamingSwitches() map[string]any {
	return map[string]any{
		"autoDeregisterWhenInstanceDown": true,
		"doubleWriteEnabled":             false,
		"jsonConfigObserverEnabled":      true,
		"distroEnabled":                  true,
		"distroHotDataEnabled":           true,
		"smartReload":                   true,
	}
}

func writeNamingStubError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	if errors.Is(err, namingsvc.ErrServiceNotFound) || errors.Is(err, namingsvc.ErrInstanceNotFound) {
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}

func writeAIStubError(w http.ResponseWriter, err error) {
	protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
		Code:    protocol.CodeParameterValidateError,
		Message: err.Error(),
	})
}

func writeClusterStubError(w http.ResponseWriter, err error) {
	protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
		Code:    protocol.CodeParameterValidateError,
		Message: err.Error(),
	})
}
