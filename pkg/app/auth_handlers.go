package app

import (
	"errors"
	"net/http"

	"github.com/godeps/gonacos/pkg/auth"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
)

type authHandler struct {
	service *auth.Service
	audit   AuditLogger
}

func registerAuthRoutes(register func(string, string, http.HandlerFunc), service *auth.Service, throttle *LoginThrottle, audit AuditLogger, registry *observability.Registry) {
	h := authHandler{service: service, audit: audit}

	for _, base := range []string{"/v3/auth/user"} {
		register(http.MethodPost, base, h.createUser)
		register(http.MethodPost, base+"/admin", h.bootstrapAdmin)
		register(http.MethodDelete, base, h.deleteUser)
		register(http.MethodPut, base, h.updateUser)
		register(http.MethodGet, base+"/list", h.listUsers)
		register(http.MethodGet, base+"/search", h.searchUsers)
		loginHandler := h.login
		if throttle != nil {
			loginHandler = newLoginThrottleMiddleware(throttle, h.login, registry).ServeHTTP
		}
		register(http.MethodPost, base+"/login", loginHandler)
	}
	for _, base := range []string{"/v3/auth/role"} {
		register(http.MethodPost, base, h.createRole)
		register(http.MethodDelete, base, h.deleteRole)
		register(http.MethodGet, base+"/list", h.listRoles)
		register(http.MethodGet, base+"/search", h.searchRoles)
	}
	for _, base := range []string{"/v3/auth/permission"} {
		register(http.MethodPost, base, h.createPermission)
		register(http.MethodDelete, base, h.deletePermission)
		register(http.MethodGet, base+"/list", h.listPermissions)
		register(http.MethodGet, base+"/has", h.hasPermission)
	}
}

func (h authHandler) createUser(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	username := formValue(r, "username")
	user, err := h.service.CreateUser(username, formValue(r, "password"))
	if err != nil {
		auditLog(h.audit, r, AuditActionUserCreate, username, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionUserCreate, user.Username, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, user.Username)
}

func (h authHandler) bootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	user, err := h.service.BootstrapAdmin(formValue(r, "password"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, user)
}

func (h authHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	username := formValue(r, "username")
	if err := h.service.DeleteUser(username); err != nil {
		auditLog(h.audit, r, AuditActionUserDelete, username, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionUserDelete, username, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "delete user ok!")
}

func (h authHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	username := formValue(r, "username")
	if err := h.service.UpdateUser(username, formValue(r, "newPassword")); err != nil {
		auditLog(h.audit, r, AuditActionUserUpdate, username, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionUserUpdate, username, "", AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "update password ok!")
}

func (h authHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListUsers(
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
		formValue(r, "username"),
		formValue(r, "search"),
	)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h authHandler) searchUsers(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	names, err := h.service.SearchUsers(formValue(r, "username"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, names)
}

func (h authHandler) login(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	username := formValue(r, "username")
	result, err := h.service.Login(username, formValue(r, "password"))
	if err != nil {
		// Login runs before the auth middleware populates claims, so we
		// synthesize the event with the form-supplied username. The IP
		// still comes from the request.
		if h.audit != nil {
			event := withAuditUser(r, AuditActionLoginFailed, username)
			event.Result = AuditResultFailure
			event.Detail = err.Error()
			h.audit.Log(event)
		}
		writeAuthError(w, err)
		return
	}
	if h.audit != nil {
		event := withAuditUser(r, AuditActionLogin, username)
		event.Result = AuditResultSuccess
		h.audit.Log(event)
	}
	w.Header().Set(auth.AuthorizationHeader, auth.TokenPrefix+result.AccessToken)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"accessToken": result.AccessToken,
		"tokenTtl":    result.TokenTTL,
		"globalAdmin": result.GlobalAdmin,
		"username":    result.Username,
	})
}

func (h authHandler) createRole(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	role := formValue(r, "role")
	username := formValue(r, "username")
	if err := h.service.CreateRole(role, username); err != nil {
		auditLog(h.audit, r, AuditActionRoleCreate, role, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionRoleCreate, role, "user="+username, AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "add role ok!")
}

func (h authHandler) deleteRole(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	role := formValue(r, "role")
	username := formValue(r, "username")
	if err := h.service.DeleteRole(role, username); err != nil {
		auditLog(h.audit, r, AuditActionRoleDelete, role, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionRoleDelete, role, "user="+username, AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "delete role ok!")
}

func (h authHandler) listRoles(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListRoles(
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
		formValue(r, "username"),
		formValue(r, "role"),
		formValue(r, "search"),
	)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h authHandler) searchRoles(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	names, err := h.service.SearchRoles(formValue(r, "role"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, names)
}

func (h authHandler) createPermission(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	role := formValue(r, "role")
	resource := formValue(r, "resource")
	action := formValue(r, "action")
	if err := h.service.CreatePermission(role, resource, action); err != nil {
		auditLog(h.audit, r, AuditActionPermissionCreate, role, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionPermissionCreate, role, "resource="+resource+" action="+action, AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "add permission ok!")
}

func (h authHandler) deletePermission(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	role := formValue(r, "role")
	resource := formValue(r, "resource")
	action := formValue(r, "action")
	if err := h.service.DeletePermission(role, resource, action); err != nil {
		auditLog(h.audit, r, AuditActionPermissionDelete, role, err.Error(), AuditResultFailure)
		writeAuthError(w, err)
		return
	}
	auditLog(h.audit, r, AuditActionPermissionDelete, role, "resource="+resource+" action="+action, AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, "delete permission ok!")
}

func (h authHandler) listPermissions(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	page, err := h.service.ListPermissions(
		parseInt(formValue(r, "pageNo"), 1),
		parseInt(formValue(r, "pageSize"), 100),
		formValue(r, "role"),
		formValue(r, "search"),
	)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, page)
}

func (h authHandler) hasPermission(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	exists, err := h.service.HasPermission(formValue(r, "role"), formValue(r, "resource"), formValue(r, "action"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, exists)
}

func writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, auth.ErrMissingUsername),
		errors.Is(err, auth.ErrMissingPassword),
		errors.Is(err, auth.ErrMissingRole),
		errors.Is(err, auth.ErrMissingResource),
		errors.Is(err, auth.ErrMissingAction):
		code = protocol.CodeParameterMissing
	case errors.Is(err, auth.ErrUserNotFound),
		errors.Is(err, auth.ErrRoleNotFound),
		errors.Is(err, auth.ErrPermissionNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, auth.ErrUserExists),
		errors.Is(err, auth.ErrRoleExists),
		errors.Is(err, auth.ErrPermissionExists),
		errors.Is(err, auth.ErrAdminExists):
		code = protocol.CodeConflict
	case errors.Is(err, auth.ErrInvalidCredentials),
		errors.Is(err, auth.ErrInvalidToken),
		errors.Is(err, auth.ErrExpiredToken),
		errors.Is(err, auth.ErrAccessDenied):
		status = http.StatusUnauthorized
		code = protocol.CodeAccessDenied
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
