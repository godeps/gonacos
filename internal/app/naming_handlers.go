package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/godeps/gonacos/internal/naming"
	"github.com/godeps/gonacos/internal/protocol"
)

type namingHandler struct {
	service *naming.Service
	mode    string
}

func registerNamingRoutes(register func(string, string, http.HandlerFunc), service *naming.Service) {
	console := namingHandler{service: service, mode: "console"}
	admin := namingHandler{service: service, mode: "admin"}
	client := namingHandler{service: service, mode: "client"}

	for _, base := range []string{"/v3/console/ns/service"} {
		register(http.MethodGet, base, console.serviceDetail)
		register(http.MethodPost, base, console.serviceCreate)
		register(http.MethodPut, base, console.serviceUpdate)
		register(http.MethodDelete, base, console.serviceDelete)
	}
	register(http.MethodGet, "/v3/console/ns/service/list", console.serviceList)
	register(http.MethodGet, "/v3/console/ns/service/selector/types", console.selectorTypes)
	register(http.MethodGet, "/v3/console/ns/service/subscribers", console.subscribers)
	register(http.MethodPut, "/v3/console/ns/service/cluster", console.clusterUpdate)

	for _, base := range []string{"/v3/console/ns/instance"} {
		register(http.MethodPut, base, console.instanceUpdate)
		register(http.MethodDelete, base, console.instanceDelete)
	}
	register(http.MethodGet, "/v3/console/ns/instance/list", console.instanceList)

	for _, base := range []string{"/v3/admin/ns/service"} {
		register(http.MethodGet, base, admin.serviceDetail)
		register(http.MethodPost, base, admin.serviceCreate)
		register(http.MethodPut, base, admin.serviceUpdate)
		register(http.MethodDelete, base, admin.serviceDelete)
	}
	register(http.MethodGet, "/v3/admin/ns/service/list", admin.serviceList)
	register(http.MethodGet, "/v3/admin/ns/service/selector/types", admin.selectorTypes)
	register(http.MethodGet, "/v3/admin/ns/service/subscribers", admin.subscribers)
	register(http.MethodPut, "/v3/admin/ns/cluster", admin.clusterUpdate)
	register(http.MethodGet, "/v3/admin/ns/instance", admin.instanceDetail)
	register(http.MethodPost, "/v3/admin/ns/instance", admin.instanceRegister)
	register(http.MethodPut, "/v3/admin/ns/instance", admin.instanceUpdate)
	register(http.MethodDelete, "/v3/admin/ns/instance", admin.instanceDelete)
	register(http.MethodGet, "/v3/admin/ns/instance/list", admin.instanceList)
	register(http.MethodPut, "/v3/admin/ns/instance/partial", admin.instancePartial)
	register(http.MethodPut, "/v3/admin/ns/instance/metadata/batch", admin.instanceBatchMetadata)
	register(http.MethodDelete, "/v3/admin/ns/instance/metadata/batch", admin.instanceBatchMetadataDelete)
	register(http.MethodGet, "/v3/admin/ns/health/checkers", admin.healthCheckers)

	register(http.MethodPost, "/v3/client/ns/instance", client.instanceRegister)
	register(http.MethodDelete, "/v3/client/ns/instance", client.instanceDelete)
	register(http.MethodGet, "/v3/client/ns/instance/list", client.instanceList)
}

func (h namingHandler) serviceCreate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	selector, err := parseSelector(formValue(r, "selector"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	err = h.service.CreateService(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		naming.ParseBool(formValue(r, "ephemeral")),
		naming.ParseThreshold(formValue(r, "protectThreshold")),
		metadata,
		selector,
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) serviceUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	selector, err := parseSelector(formValue(r, "selector"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	err = h.service.UpdateService(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		naming.ParseBool(formValue(r, "ephemeral")),
		naming.ParseThreshold(formValue(r, "protectThreshold")),
		metadata,
		selector,
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) serviceDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteService(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "serviceName")); err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) serviceDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	info, err := h.service.GetService(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "serviceName"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, info)
}

func (h namingHandler) serviceList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListServices(
		formValue(r, "namespaceId"),
		formValue(r, "groupNameParam"),
		formValue(r, "serviceNameParam"),
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
		naming.ParseBool(formValue(r, "ignoreEmptyService")),
		naming.ParseBool(formValue(r, "withInstances")),
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h namingHandler) selectorTypes(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, naming.SelectorTypes())
}

func (h namingHandler) clusterUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	healthChecker, err := naming.ParseMetadata(formValue(r, "healthChecker"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	err = h.service.UpdateCluster(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		formValue(r, "clusterName"),
		parseInt(formValue(r, "checkPort"), 0),
		naming.ParseBool(formValue(r, "useInstancePort4Check")),
		healthChecker,
		metadata,
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) subscribers(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListSubscribers(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h namingHandler) instanceRegister(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	inst := naming.Instance{
		NamespaceID: formValue(r, "namespaceId"),
		GroupName:   formValue(r, "groupName"),
		ServiceName: formValue(r, "serviceName"),
		ClusterName: formValue(r, "clusterName"),
		IP:          formValue(r, "ip"),
		Port:        naming.ParsePort(formValue(r, "port")),
		Weight:      naming.ParseWeight(formValue(r, "weight")),
		Healthy:     parseHealthyDefault(formValue(r, "healthy")),
		Enabled:     parseEnabledDefault(formValue(r, "enabled")),
		Ephemeral:   naming.ParseBool(formValue(r, "ephemeral")),
		Metadata:    metadata,
		AppName:     formValue(r, "appName"),
	}
	registered, err := h.service.RegisterInstance(inst)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, registered)
}

func (h namingHandler) instanceUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	inst := naming.Instance{
		NamespaceID: formValue(r, "namespaceId"),
		GroupName:   formValue(r, "groupName"),
		ServiceName: formValue(r, "serviceName"),
		ClusterName: formValue(r, "clusterName"),
		IP:          formValue(r, "ip"),
		Port:        naming.ParsePort(formValue(r, "port")),
		Weight:      naming.ParseWeight(formValue(r, "weight")),
		Healthy:     naming.ParseBool(formValue(r, "healthy")),
		Enabled:     naming.ParseBool(formValue(r, "enabled")),
		Ephemeral:   naming.ParseBool(formValue(r, "ephemeral")),
		Metadata:    metadata,
	}
	if err := h.service.UpdateInstance(inst); err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) instanceDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeregisterInstance(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		formValue(r, "clusterName"),
		formValue(r, "ip"),
		naming.ParsePort(formValue(r, "port")),
		formValue(r, "instanceId"),
	); err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) instanceDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	instances, err := h.service.ListInstances(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		formValue(r, "clusterName"),
		naming.ParseBool(formValue(r, "healthyOnly")),
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, instances)
}

func (h namingHandler) instanceList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListInstancesPaginated(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		formValue(r, "clusterName"),
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
		naming.ParseBool(formValue(r, "healthyOnly")),
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h namingHandler) instancePartial(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		writeNamingError(w, err)
		return
	}
	inst := naming.Instance{
		NamespaceID: formValue(r, "namespaceId"),
		GroupName:   formValue(r, "groupName"),
		ServiceName: formValue(r, "serviceName"),
		ClusterName: formValue(r, "clusterName"),
		IP:          formValue(r, "ip"),
		Port:        naming.ParsePort(formValue(r, "port")),
		Metadata:    metadata,
		Weight:      naming.ParseWeight(formValue(r, "weight")),
		Healthy:     naming.ParseBool(formValue(r, "healthy")),
		Enabled:     naming.ParseBool(formValue(r, "enabled")),
	}
	if err := h.service.UpdateInstance(inst); err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namingHandler) instanceBatchMetadata(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	updates, err := parseBatchMetadataUpdates(r)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	result, err := h.service.BatchUpdateInstanceMetadata(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		updates,
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h namingHandler) instanceBatchMetadataDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	ids := strings.Split(formValue(r, "instanceIds"), ",")
	keys := strings.Split(formValue(r, "metadataKeys"), ",")
	result, err := h.service.BatchDeleteInstanceMetadata(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "serviceName"),
		ids,
		keys,
	)
	if err != nil {
		writeNamingError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h namingHandler) healthCheckers(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, []map[string]string{
		{"type": "none"},
		{"type": "http"},
		{"type": "tcp"},
	})
}

func parseSelector(s string) (naming.Selector, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return naming.Selector{}, nil
	}
	var sel naming.Selector
	if err := json.Unmarshal([]byte(s), &sel); err == nil {
		return sel, nil
	}
	return naming.Selector{Type: s}, nil
}

func parseBatchMetadataUpdates(r *http.Request) ([]naming.InstanceMetadataUpdate, error) {
	raw := formValue(r, "updates")
	if raw != "" {
		var updates []naming.InstanceMetadataUpdate
		if err := json.Unmarshal([]byte(raw), &updates); err != nil {
			return nil, err
		}
		return updates, nil
	}
	metadata, err := naming.ParseMetadata(formValue(r, "metadata"))
	if err != nil {
		return nil, err
	}
	id := formValue(r, "instanceId")
	if id == "" {
		return nil, errors.New("instanceId is required")
	}
	return []naming.InstanceMetadataUpdate{{InstanceID: id, Metadata: metadata}}, nil
}

func parseHealthyDefault(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	return s == "true" || s == "1"
}

func parseEnabledDefault(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	return s == "true" || s == "1"
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func writeNamingError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, naming.ErrMissingNamespaceID),
		errors.Is(err, naming.ErrMissingGroupName),
		errors.Is(err, naming.ErrMissingServiceName),
		errors.Is(err, naming.ErrMissingClusterName),
		errors.Is(err, naming.ErrMissingInstanceID):
		code = protocol.CodeParameterMissing
	case errors.Is(err, naming.ErrServiceNotFound),
		errors.Is(err, naming.ErrInstanceNotFound),
		errors.Is(err, naming.ErrClusterNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, naming.ErrServiceExists):
		code = protocol.CodeConflict
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
