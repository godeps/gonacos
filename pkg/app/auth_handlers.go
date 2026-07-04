package app

import (
	"errors"
	"net/http"

	"github.com/godeps/gonacos/pkg/auth"
	"github.com/godeps/gonacos/pkg/protocol"
)

type authHandler struct {
	service *auth.Service
}

func registerAuthRoutes(register func(string, string, http.HandlerFunc), service *auth.Service) {
	h := authHandler{service: service}

	for _, base := range []string{"/v3/auth/user"} {
		register(http.MethodPost, base, h.createUser)
		register(http.MethodPost, base+"/admin", h.bootstrapAdmin)
		register(http.MethodDelete, base, h.deleteUser)
		register(http.MethodPut, base, h.updateUser)
		register(http.MethodGet, base+"/list", h.listUsers)
		register(http.MethodGet, base+"/search", h.searchUsers)
		register(http.MethodPost, base+"/login", h.login)
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
	user, err := h.service.CreateUser(formValue(r, "username"), formValue(r, "password"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
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
	if err := h.service.DeleteUser(formValue(r, "username")); err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, "delete user ok!")
}

func (h authHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.UpdateUser(formValue(r, "username"), formValue(r, "newPassword")); err != nil {
		writeAuthError(w, err)
		return
	}
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
	result, err := h.service.Login(formValue(r, "username"), formValue(r, "password"))
	if err != nil {
		writeAuthError(w, err)
		return
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
	if err := h.service.CreateRole(formValue(r, "role"), formValue(r, "username")); err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, "add role ok!")
}

func (h authHandler) deleteRole(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteRole(formValue(r, "role"), formValue(r, "username")); err != nil {
		writeAuthError(w, err)
		return
	}
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
	if err := h.service.CreatePermission(formValue(r, "role"), formValue(r, "resource"), formValue(r, "action")); err != nil {
		writeAuthError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, "add permission ok!")
}

func (h authHandler) deletePermission(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeletePermission(formValue(r, "role"), formValue(r, "resource"), formValue(r, "action")); err != nil {
		writeAuthError(w, err)
		return
	}
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
