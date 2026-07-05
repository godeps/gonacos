package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	configsvc "github.com/godeps/gonacos/pkg/config"
	"github.com/godeps/gonacos/pkg/protocol"
)

type configHandler struct {
	service *configsvc.Service
	mode    string
	audit   AuditLogger
}

func registerConfigRoutes(register func(string, string, http.HandlerFunc), service *configsvc.Service, audit AuditLogger) {
	admin := configHandler{service: service, mode: "admin", audit: audit}
	console := configHandler{service: service, mode: "console", audit: audit}
	client := configHandler{service: service, mode: "client", audit: audit}

	for _, base := range []string{"/v3/admin/cs/config"} {
		register(http.MethodGet, base, admin.detail)
		register(http.MethodPost, base, admin.publish)
		register(http.MethodPut, base, admin.updateMetadata)
		register(http.MethodDelete, base, admin.delete)
		register(http.MethodGet, base+"/list", admin.list)
		register(http.MethodDelete, base+"/batch", admin.batchDelete)
		register(http.MethodPost, base+"/clone", admin.clone)
		register(http.MethodGet, base+"/export", admin.exportConfig)
		register(http.MethodPost, base+"/import", admin.importConfig)
		register(http.MethodGet, base+"/listener", admin.configListener)
		register(http.MethodGet, base+"/beta", admin.betaDetail)
		register(http.MethodDelete, base+"/beta", admin.betaDelete)
		register(http.MethodGet, base+"/gray", admin.grayDetail)
		register(http.MethodPost, base+"/gray", admin.grayPublish)
		register(http.MethodDelete, base+"/gray", admin.grayDelete)
	}
	register(http.MethodGet, "/v3/admin/cs/listener", admin.ipListener)
	for _, base := range []string{"/v3/admin/cs/capacity"} {
		register(http.MethodGet, base, admin.capacityQuery)
		register(http.MethodPost, base, admin.capacityUpdate)
	}
	register(http.MethodGet, "/v3/admin/cs/metrics/ip", admin.clientMetrics)
	register(http.MethodGet, "/v3/admin/cs/metrics/cluster", admin.clusterClientMetrics)
	register(http.MethodPost, "/v3/admin/cs/ops/localCache", admin.localCacheRefresh)
	for _, base := range []string{"/v3/admin/cs/history"} {
		register(http.MethodGet, base, admin.historyDetail)
		register(http.MethodGet, base+"/list", admin.historyList)
		register(http.MethodGet, base+"/previous", admin.historyPrevious)
		register(http.MethodGet, base+"/configs", admin.historyConfigs)
	}
	for _, base := range []string{"/v3/console/cs/config"} {
		register(http.MethodGet, base, console.detail)
		register(http.MethodPost, base, console.publish)
		register(http.MethodDelete, base, console.delete)
		register(http.MethodGet, base+"/list", console.list)
		register(http.MethodDelete, base+"/batchDelete", console.batchDelete)
		register(http.MethodPost, base+"/clone", console.clone)
		register(http.MethodGet, base+"/export2", console.exportConfig)
		register(http.MethodPost, base+"/import", console.importConfig)
		register(http.MethodGet, base+"/listener", console.configListener)
		register(http.MethodGet, base+"/listener/ip", console.ipListener)
		register(http.MethodGet, base+"/beta", console.betaDetail)
		register(http.MethodDelete, base+"/beta", console.betaDelete)
	}
	for _, base := range []string{"/v3/console/cs/history"} {
		register(http.MethodGet, base, console.historyDetail)
		register(http.MethodGet, base+"/list", console.historyList)
		register(http.MethodGet, base+"/previous", console.historyPrevious)
		register(http.MethodGet, base+"/configs", console.historyConfigs)
	}
	register(http.MethodGet, "/v3/client/cs/config", client.clientQuery)
}

func (h configHandler) publish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	req := configsvc.PublishRequest{
		NamespaceID:      formValue(r, "namespaceId"),
		GroupName:        formValue(r, "groupName"),
		DataID:           formValue(r, "dataId"),
		Content:          formValue(r, "content"),
		Type:             formValue(r, "type"),
		Desc:             formValue(r, "desc"),
		ConfigTags:       formValue(r, "configTags"),
		AppName:          formValue(r, "appName"),
		SrcUser:          formValue(r, "srcUser"),
		EncryptedDataKey: formValue(r, "encryptedDataKey"),
		BetaIPs:          r.Header.Get("betaIps"),
	}
	if err := h.service.Publish(req); err != nil {
		auditLog(h.audit, r, AuditActionConfigPublish, configResourceID(req.NamespaceID, req.GroupName, req.DataID), err.Error(), AuditResultFailure)
		writeConfigError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionConfigPublish, configResourceID(req.NamespaceID, req.GroupName, req.DataID), "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) updateMetadata(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	err := h.service.UpdateMetadata(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		formValue(r, "desc"),
		formValue(r, "configTags"),
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) detail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	item, err := h.service.Get(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId"))
	if err != nil {
		if h.mode == "console" && errors.Is(err, configsvc.ErrConfigNotFound) {
			protocol.WriteResult(w, http.StatusOK, nil)
			return
		}
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, item)
}

func (h configHandler) clientQuery(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	groupName := formValue(r, "groupName")
	dataID := formValue(r, "dataId")
	ip := clientIP(r)
	item, isBeta, err := h.service.GetForClient(ip, namespaceID, groupName, dataID)
	if err != nil {
		if errors.Is(err, configsvc.ErrConfigNotFound) {
			h.service.RemoveListener(ip, namespaceID, groupName, dataID)
			protocol.WriteEnvelope(w, http.StatusOK, protocol.CodeNotFound, configsvc.ErrConfigNotFound.Error(), configsvc.NotFoundQueryResponse())
			return
		}
		writeConfigError(w, err)
		return
	}
	h.service.TrackListener(ip, namespaceID, groupName, dataID, item.MD5)
	protocol.WriteResult(w, http.StatusOK, toQueryResponse(item, isBeta))
}

func toQueryResponse(item configsvc.Item, isBeta bool) configsvc.QueryResponse {
	resp := configsvc.ToQueryResponse(item)
	resp.Beta = isBeta
	return resp
}

func (h configHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	groupName := formValue(r, "groupName")
	dataID := formValue(r, "dataId")
	if err := h.service.Delete(namespaceID, groupName, dataID); err != nil {
		auditLog(h.audit, r, AuditActionConfigDelete, configResourceID(namespaceID, groupName, dataID), err.Error(), AuditResultFailure)
		writeConfigError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionConfigDelete, configResourceID(namespaceID, groupName, dataID), "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) list(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	pageNo, err := parsePositiveInt(formValue(r, "pageNo"), 1)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	pageSize, err := parsePositiveInt(formValue(r, "pageSize"), 100)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	page, err := h.service.List(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		formValue(r, "search"),
		pageNo,
		pageSize,
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h configHandler) batchDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	ids := splitIDs(formValue(r, "ids"))
	if err := h.service.DeleteByIDs(ids); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) clone(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	targetNamespace := formValue(r, "targetNamespaceId")
	srcUser := formValue(r, "srcUser")
	if h.mode == "admin" {
		targetNamespace = formValue(r, "namespaceId")
		srcUser = formValue(r, "src_user")
	}
	if strings.TrimSpace(targetNamespace) == "" {
		writeConfigError(w, configsvc.ErrMissingNamespace)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: "read clone body: " + err.Error(),
		})
		return
	}
	requests, err := decodeCloneRequests(body, h.mode, targetNamespace, formValue(r, "policy"), srcUser)
	if err != nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: err.Error(),
		})
		return
	}
	result, err := h.service.Clone(requests)
	if err != nil {
		if errors.Is(err, configsvc.ErrNoSelectedConfig) {
			protocol.WriteEnvelope(w, http.StatusOK, protocol.CodeNoSelectedConfig, err.Error(), result)
			return
		}
		if errors.Is(err, configsvc.ErrConfigNotFound) {
			protocol.WriteEnvelope(w, http.StatusOK, protocol.CodeDataEmpty, err.Error(), result)
			return
		}
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h configHandler) exportConfig(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	items, err := h.service.GetByIDs(formValue(r, "namespaceId"), splitIDs(formValue(r, "ids")))
	if err != nil {
		writeConfigError(w, err)
		return
	}

	body, err := encodeConfigExportZip(items)
	if err != nil {
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment;filename=nacos_config_export_%d.zip", time.Now().UnixMilli()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h configHandler) importConfig(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(32 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeImportFailure(w, protocol.CodeImportedDataEmpty, configsvc.ErrImportDataEmpty.Error())
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		writeImportFailure(w, protocol.CodeImportedDataEmpty, configsvc.ErrImportDataEmpty.Error())
		return
	}
	entries, err := decodeConfigImportZip(body, formValue(r, "namespaceId"))
	if err != nil {
		if errors.Is(err, configsvc.ErrImportDataEmpty) {
			writeImportFailure(w, protocol.CodeImportedDataEmpty, configsvc.ErrImportDataEmpty.Error())
			return
		}
		writeImportFailure(w, protocol.CodeMetadataIllegal, configsvc.ErrMetadataIllegal.Error())
		return
	}
	result, err := h.service.Import(entries, formValue(r, "policy"))
	if err != nil {
		if errors.Is(err, configsvc.ErrImportDataEmpty) {
			writeImportFailure(w, protocol.CodeImportedDataEmpty, configsvc.ErrImportDataEmpty.Error())
			return
		}
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h configHandler) configListener(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	groupName := strings.TrimSpace(formValue(r, "groupName"))
	dataID := strings.TrimSpace(formValue(r, "dataId"))
	if err := validateListenerIdentity(namespaceID, groupName, dataID); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, configsvc.ListenerInfo{
		QueryType:       "config",
		ListenersStatus: h.service.ListenersByConfig(namespaceID, groupName, dataID),
	})
}

func (h configHandler) ipListener(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	ip := strings.TrimSpace(formValue(r, "ip"))
	if ip == "" {
		writeConfigError(w, configsvc.ErrMissingIP)
		return
	}
	namespaceID := formValue(r, "namespaceId")
	if strings.TrimSpace(namespaceID) != "" {
		if _, err := h.service.ConfigsByNamespace(namespaceID); err != nil {
			writeConfigError(w, err)
			return
		}
	}
	protocol.WriteResult(w, http.StatusOK, configsvc.ListenerInfo{
		QueryType:       "ip",
		ListenersStatus: h.service.ListenersByIP(ip, namespaceID),
	})
}

func (h configHandler) betaDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	item, err := h.service.GetBeta(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId"))
	if err != nil {
		if h.mode == "console" && errors.Is(err, configsvc.ErrConfigNotInBeta) {
			protocol.WriteResult(w, http.StatusOK, nil)
			return
		}
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, item)
}

func (h configHandler) betaDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteBeta(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId")); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

// grayDetail handles GET /v3/admin/cs/config/gray. It returns the named gray
// config for the given (namespace, group, dataId, grayName) tuple.
func (h configHandler) grayDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	grayName := formValue(r, "grayName")
	if grayName == "" {
		// No grayName: list all grays for the config.
		items := h.service.ListGray(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId"), "")
		protocol.WriteResult(w, http.StatusOK, items)
		return
	}
	item, err := h.service.GetGray(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId"), grayName)
	if err != nil {
		if h.mode == "console" && (errors.Is(err, configsvc.ErrGrayNotFound) || errors.Is(err, configsvc.ErrConfigNotInBeta)) {
			protocol.WriteResult(w, http.StatusOK, nil)
			return
		}
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, item)
}

// grayPublish handles POST /v3/admin/cs/config/gray. The grayMatchRuleExp is
// a JSON expression; for IP-based grays it carries a betaIps field that the
// server uses to match clients.
func (h configHandler) grayPublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	grayName := formValue(r, "grayName")
	if grayName == "" {
		writeConfigError(w, configsvc.ErrMissingGrayName)
		return
	}
	priority, _ := strconv.Atoi(formValue(r, "grayPriority"))
	req := configsvc.GrayRequest{
		PublishRequest: configsvc.PublishRequest{
			NamespaceID:      formValue(r, "namespaceId"),
			GroupName:        formValue(r, "groupName"),
			DataID:           formValue(r, "dataId"),
			Content:          formValue(r, "content"),
			Type:             formValue(r, "type"),
			SrcUser:          formValue(r, "srcUser"),
			EncryptedDataKey: formValue(r, "encryptedDataKey"),
		},
		GrayName:     grayName,
		GrayRule:     formValue(r, "grayMatchRuleExp"),
		GrayType:     formValue(r, "grayType"),
		GrayVersion:  formValue(r, "grayVersion"),
		GrayPriority: priority,
	}
	if err := h.service.PublishGray(req); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

// grayDelete handles DELETE /v3/admin/cs/config/gray. It removes the named
// gray version; the regular config is unaffected.
func (h configHandler) grayDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	grayName := formValue(r, "grayName")
	if grayName == "" {
		writeConfigError(w, configsvc.ErrMissingGrayName)
		return
	}
	if err := h.service.DeleteGray(formValue(r, "namespaceId"), formValue(r, "groupName"), formValue(r, "dataId"), grayName); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) capacityQuery(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	cap, err := h.service.GetCapacity(formValue(r, "namespaceId"), formValue(r, "groupName"))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, cap)
}

func (h configHandler) capacityUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	quota, _ := strconv.Atoi(formValue(r, "quota"))
	maxSize, _ := strconv.Atoi(formValue(r, "maxSize"))
	maxAggrCount, _ := strconv.Atoi(formValue(r, "maxAggrCount"))
	maxAggrSize, _ := strconv.Atoi(formValue(r, "maxAggrSize"))
	if err := h.service.UpdateCapacity(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		quota, maxSize, maxAggrCount, maxAggrSize,
	); err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h configHandler) clientMetrics(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if strings.TrimSpace(formValue(r, "ip")) == "" {
		writeConfigError(w, configsvc.ErrMissingIP)
		return
	}
	metrics := h.service.ClientMetrics(
		formValue(r, "ip"),
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
	)
	protocol.WriteResult(w, http.StatusOK, metrics)
}

func (h configHandler) clusterClientMetrics(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if strings.TrimSpace(formValue(r, "ip")) == "" {
		writeConfigError(w, configsvc.ErrMissingIP)
		return
	}
	metrics := h.service.ClusterClientMetrics(
		formValue(r, "ip"),
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
	)
	protocol.WriteResult(w, http.StatusOK, metrics)
}

func (h configHandler) localCacheRefresh(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	// In standalone/redis mode, local cache is the in-memory store itself.
	// This endpoint is a no-op that acknowledges the refresh request.
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"refreshed": true,
		"mode":      "standalone",
	})
}

func (h configHandler) historyList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	pageNo, err := parseRequiredPositiveInt(formValue(r, "pageNo"), "pageNo")
	if err != nil {
		writeConfigError(w, err)
		return
	}
	pageSize, err := parseRequiredPositiveInt(formValue(r, "pageSize"), "pageSize")
	if err != nil {
		writeConfigError(w, err)
		return
	}
	page, err := h.service.HistoryList(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		pageNo,
		pageSize,
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h configHandler) historyDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	item, err := h.service.HistoryDetail(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		formValue(r, "nid"),
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, item)
}

func (h configHandler) historyPrevious(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	item, err := h.service.PreviousHistory(
		formValue(r, "namespaceId"),
		formValue(r, "groupName"),
		formValue(r, "dataId"),
		formValue(r, "id"),
	)
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, item)
}

func (h configHandler) historyConfigs(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	items, err := h.service.ConfigsByNamespace(formValue(r, "namespaceId"))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, items)
}

func parsePositiveInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, configsvc.ErrInvalidNamespace
	}
	return n, nil
}

func parseRequiredPositiveInt(value, field string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if field == "pageNo" {
			return 0, configsvc.ErrInvalidPageNo
		}
		return 0, configsvc.ErrInvalidPageSize
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		if field == "pageNo" {
			return 0, configsvc.ErrInvalidPageNo
		}
		return 0, configsvc.ErrInvalidPageSize
	}
	return n, nil
}

func writeConfigError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, configsvc.ErrMissingDataID), errors.Is(err, configsvc.ErrMissingGroup), errors.Is(err, configsvc.ErrMissingContent), errors.Is(err, configsvc.ErrMissingIDs), errors.Is(err, configsvc.ErrMissingNamespace), errors.Is(err, configsvc.ErrMissingHistoryID), errors.Is(err, configsvc.ErrMissingConfigID), errors.Is(err, configsvc.ErrMissingIP), errors.Is(err, configsvc.ErrMissingGrayName):
		code = protocol.CodeParameterMissing
	case errors.Is(err, configsvc.ErrConfigNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, configsvc.ErrConfigNotInBeta), errors.Is(err, configsvc.ErrGrayNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, configsvc.ErrAccessDenied):
		status = http.StatusForbidden
		code = protocol.CodeAccessDenied
	case errors.Is(err, configsvc.ErrImportDataEmpty):
		code = protocol.CodeImportedDataEmpty
	case errors.Is(err, configsvc.ErrMetadataIllegal):
		code = protocol.CodeMetadataIllegal
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}

func validateListenerIdentity(namespaceID, groupName, dataID string) error {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID != "" && strings.ContainsAny(namespaceID, " \t\r\n") {
		return configsvc.ErrInvalidNamespace
	}
	if dataID == "" {
		return configsvc.ErrMissingDataID
	}
	if groupName == "" {
		return configsvc.ErrMissingGroup
	}
	return nil
}

func writeImportFailure(w http.ResponseWriter, code int, message string) {
	protocol.WriteEnvelope(w, http.StatusOK, code, message, map[string]any{})
}

func splitIDs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

// configResourceID formats a config identifier as namespace/group/dataId for
// the audit log's Resource field. Empty namespace is shown as "public" to
// match Nacos conventions.
func configResourceID(namespaceID, groupName, dataID string) string {
	if namespaceID == "" {
		namespaceID = "public"
	}
	return namespaceID + "/" + groupName + "/" + dataID
}

type clonePayload struct {
	ConfigID        any    `json:"configId"`
	CfgID           any    `json:"cfgId"`
	TargetDataID    string `json:"targetDataId"`
	TargetGroupName string `json:"targetGroupName"`
	DataID          string `json:"dataId"`
	Group           string `json:"group"`
}

func decodeCloneRequests(body []byte, mode, targetNamespace, policy, srcUser string) ([]configsvc.CloneRequest, error) {
	var payload []clonePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	requests := make([]configsvc.CloneRequest, 0, len(payload))
	for _, item := range payload {
		req := configsvc.CloneRequest{
			TargetNamespace: targetNamespace,
			Policy:          policy,
			SrcUser:         srcUser,
		}
		if mode == "admin" {
			req.SourceID = stringifyID(item.ConfigID)
			req.TargetDataID = item.TargetDataID
			req.TargetGroupName = item.TargetGroupName
		} else {
			req.SourceID = stringifyID(item.CfgID)
			req.TargetDataID = item.DataID
			req.TargetGroupName = item.Group
		}
		requests = append(requests, req)
	}
	return requests, nil
}

func stringifyID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func encodeConfigExportZip(items []configsvc.Item) ([]byte, error) {
	var out bytes.Buffer
	zipWriter := zip.NewWriter(&out)
	var metadata strings.Builder
	metadata.WriteString("metadata:\n")
	for _, item := range items {
		entryName := item.GroupName + "/" + item.DataID
		writer, err := zipWriter.Create(entryName)
		if err != nil {
			return nil, fmt.Errorf("create config zip entry: %w", err)
		}
		if _, err := writer.Write([]byte(item.Content)); err != nil {
			return nil, fmt.Errorf("write config zip entry: %w", err)
		}
		metadata.WriteString("- dataId: " + item.DataID + "\n")
		metadata.WriteString("  group: " + item.GroupName + "\n")
		metadata.WriteString("  type: " + item.Type + "\n")
		metadata.WriteString("  appName: '" + item.AppName + "'\n")
		metadata.WriteString("  desc: " + item.Desc + "\n")
	}
	writer, err := zipWriter.Create(".metadata.yml")
	if err != nil {
		return nil, fmt.Errorf("create metadata zip entry: %w", err)
	}
	if _, err := writer.Write([]byte(metadata.String())); err != nil {
		return nil, fmt.Errorf("write metadata zip entry: %w", err)
	}
	if err := zipWriter.Close(); err != nil {
		return nil, fmt.Errorf("close config zip: %w", err)
	}
	return out.Bytes(), nil
}

func decodeConfigImportZip(body []byte, namespaceID string) ([]configsvc.ImportEntry, error) {
	if len(body) == 0 {
		return nil, configsvc.ErrImportDataEmpty
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, configsvc.ErrMetadataIllegal
	}
	files := map[string]string{}
	metadata := ""
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			return nil, configsvc.ErrMetadataIllegal
		}
		content, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			return nil, configsvc.ErrMetadataIllegal
		}
		if file.Name == ".metadata.yml" {
			metadata = string(content)
			continue
		}
		files[file.Name] = string(content)
	}
	entries, err := parseImportMetadata(metadata, namespaceID)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		content, ok := files[entries[i].GroupName+"/"+entries[i].DataID]
		if !ok {
			return nil, configsvc.ErrMetadataIllegal
		}
		entries[i].Content = content
	}
	return entries, nil
}

func parseImportMetadata(metadata, namespaceID string) ([]configsvc.ImportEntry, error) {
	if strings.TrimSpace(metadata) == "" {
		return nil, configsvc.ErrMetadataIllegal
	}
	var entries []configsvc.ImportEntry
	var current *configsvc.ImportEntry
	for _, line := range strings.Split(metadata, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "metadata:" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			entry := configsvc.ImportEntry{NamespaceID: namespaceID}
			entries = append(entries, entry)
			current = &entries[len(entries)-1]
			assignImportMetadataField(current, strings.TrimPrefix(trimmed, "- "))
			continue
		}
		if current == nil {
			return nil, configsvc.ErrMetadataIllegal
		}
		assignImportMetadataField(current, trimmed)
	}
	if len(entries) == 0 {
		return nil, configsvc.ErrMetadataIllegal
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.DataID) == "" || strings.TrimSpace(entry.GroupName) == "" {
			return nil, configsvc.ErrMetadataIllegal
		}
	}
	return entries, nil
}

func assignImportMetadataField(entry *configsvc.ImportEntry, line string) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	value = strings.Trim(strings.TrimSpace(value), "'\"")
	switch strings.TrimSpace(name) {
	case "dataId":
		entry.DataID = value
	case "group":
		entry.GroupName = value
	case "type":
		entry.Type = value
	case "appName":
		entry.AppName = value
	case "desc":
		entry.Desc = value
	}
}
