package app

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/godeps/gonacos/pkg/config"
	"github.com/godeps/gonacos/pkg/namespace"
	"github.com/godeps/gonacos/pkg/protocol"
)

type namespaceHandler struct {
	service *namespace.Service
	configs *config.Service
	admin   bool
	audit   AuditLogger
}

func registerNamespaceRoutes(register func(string, string, http.HandlerFunc), service *namespace.Service, configs *config.Service, audit AuditLogger) {
	console := namespaceHandler{service: service, configs: configs, audit: audit}
	admin := namespaceHandler{service: service, configs: configs, admin: true, audit: audit}

	for _, base := range []string{"/v3/console/core/namespace"} {
		register(http.MethodGet, base, console.detail)
		register(http.MethodPost, base, console.create)
		register(http.MethodPut, base, console.update)
		register(http.MethodDelete, base, console.delete)
		register(http.MethodGet, base+"/list", console.list)
		register(http.MethodGet, base+"/exist", console.exists)
	}
	for _, base := range []string{"/v3/admin/core/namespace"} {
		register(http.MethodGet, base, admin.detail)
		register(http.MethodPost, base, admin.create)
		register(http.MethodPut, base, admin.update)
		register(http.MethodDelete, base, admin.delete)
		register(http.MethodGet, base+"/list", admin.list)
		register(http.MethodGet, base+"/check", admin.exists)
		register(http.MethodGet, base+"/exist", admin.exists)
	}
}

func (h namespaceHandler) create(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	if !h.admin {
		namespaceID = formValue(r, "customNamespaceId")
	}
	if err := h.service.Create(namespaceID, formValue(r, "namespaceName"), formValue(r, "namespaceDesc")); err != nil {
		auditLog(h.audit, r, AuditActionNamespaceCreate, namespaceID, err.Error(), AuditResultFailure)
		writeNamespaceError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionNamespaceCreate, namespaceID, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namespaceHandler) update(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	if err := h.service.Update(namespaceID, formValue(r, "namespaceName"), formValue(r, "namespaceDesc")); err != nil {
		auditLog(h.audit, r, AuditActionNamespaceUpdate, namespaceID, err.Error(), AuditResultFailure)
		writeNamespaceError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionNamespaceUpdate, namespaceID, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namespaceHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	if err := h.service.Delete(namespaceID); err != nil {
		auditLog(h.audit, r, AuditActionNamespaceDelete, namespaceID, err.Error(), AuditResultFailure)
		writeNamespaceError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionNamespaceDelete, namespaceID, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h namespaceHandler) detail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	ns, err := h.service.Get(formValue(r, "namespaceId"))
	if err != nil {
		writeNamespaceError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, ns)
}

func (h namespaceHandler) list(w http.ResponseWriter, r *http.Request) {
	items := h.service.List()
	// Populate ConfigCount so the console can show how many configs each
	// namespace holds. Without this the field is always 0 — the namespace
	// service does not own config data, so we ask the config service for a
	// single batch count and merge it in. Namespaces with no configs keep
	// the zero value the service already set.
	if h.configs != nil && len(items) > 0 {
		counts := h.configs.CountAllByNamespace()
		for i := range items {
			if n, ok := counts[items[i].Namespace]; ok {
				items[i].ConfigCount = n
			}
		}
	}
	protocol.WriteResult(w, http.StatusOK, items)
}

func (h namespaceHandler) exists(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	namespaceID := formValue(r, "namespaceId")
	if !h.admin {
		if _, ok := r.Form["customNamespaceId"]; !ok {
			writeNamespaceError(w, namespace.ErrMissingNamespaceID)
			return
		}
		namespaceID = formValue(r, "customNamespaceId")
		protocol.WriteResult(w, http.StatusOK, h.service.Exists(namespaceID))
		return
	}
	if namespaceID == "" {
		writeNamespaceError(w, namespace.ErrMissingNamespaceID)
		return
	}
	if h.service.Exists(namespaceID) {
		protocol.WriteResult(w, http.StatusOK, 1)
		return
	}
	protocol.WriteResult(w, http.StatusOK, 0)
}

func parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: "parse form: " + err.Error(),
		})
		return false
	}
	if r.Body != nil && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
				Code:    protocol.CodeParameterValidateError,
				Message: "read form: " + err.Error(),
			})
			return false
		}
		values, err := url.ParseQuery(string(data))
		if err != nil {
			protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
				Code:    protocol.CodeParameterValidateError,
				Message: "parse form: " + err.Error(),
			})
			return false
		}
		for key, vals := range values {
			if _, ok := r.Form[key]; !ok {
				r.Form[key] = vals
			}
			if _, ok := r.PostForm[key]; !ok {
				r.PostForm[key] = vals
			}
		}
	}
	return true
}

func formValue(r *http.Request, key string) string {
	return r.Form.Get(key)
}

// clientIP extracts the client IP from a request, honoring the X-Forwarded-For
// and X-Real-IP headers that proxies set. Falls back to r.RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeNamespaceError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, namespace.ErrMissingNamespaceID), errors.Is(err, namespace.ErrMissingNamespaceName):
		code = protocol.CodeParameterMissing
	case errors.Is(err, namespace.ErrNamespaceNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, namespace.ErrNamespaceExists), errors.Is(err, namespace.ErrDeletePublic):
		code = protocol.CodeConflict
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
