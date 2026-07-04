package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthBootstrapAndLogin(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/auth/user/admin", url.Values{
		"password": {"adminpass123"},
	}, http.StatusOK)

	rec := postFormRaw(t, handler, http.MethodPost, "/v3/auth/user/login", url.Values{
		"username": {"nacos"},
		"password": {"adminpass123"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := json.Marshal(body.Data)
	var login map[string]any
	if err := json.Unmarshal(data, &login); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	if login["accessToken"] == "" || login["globalAdmin"] != true {
		t.Fatalf("login data = %+v", login)
	}
	if !strings.HasPrefix(rec.Header().Get("Authorization"), "Bearer ") {
		t.Fatalf("authorization header = %q", rec.Header().Get("Authorization"))
	}
}

func TestAuthBootstrapDuplicateReturns409(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/auth/user/admin", url.Values{"password": {"pass123"}}, http.StatusOK)
	result := postForm(t, handler, http.MethodPost, "/v3/auth/user/admin", url.Values{"password": {"other"}}, http.StatusBadRequest)
	if result.Code != 409 {
		t.Fatalf("code = %d, want 409", result.Code)
	}
}

func TestAuthLoginInvalidCredentialsReturns401(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/auth/user/admin", url.Values{"password": {"pass123"}}, http.StatusOK)
	result := postForm(t, handler, http.MethodPost, "/v3/auth/user/login", url.Values{
		"username": {"nacos"},
		"password": {"wrongpass"},
	}, http.StatusUnauthorized)
	if result.Code != 403 {
		t.Fatalf("code = %d, want 403", result.Code)
	}
}

func TestAuthCreateUserAndList(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"alice"},
		"password": {"alicepass"},
	}, auth, http.StatusOK)

	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/user/list?pageNo=1&pageSize=10", nil, auth, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		TotalCount int `json:"totalCount"`
		PageItems  []struct {
			Username string `json:"username"`
		} `json:"pageItems"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal: %v (data=%s)", err, data)
	}
	if page.TotalCount < 2 {
		t.Fatalf("count = %d, want >= 2", page.TotalCount)
	}
}

func TestAuthCreateUserDuplicateReturns409(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"alice"},
		"password": {"alicepass"},
	}, auth, http.StatusOK)
	result := postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"alice"},
		"password": {"alicepass"},
	}, auth, http.StatusBadRequest)
	if result.Code != 409 {
		t.Fatalf("code = %d, want 409", result.Code)
	}
}

func TestAuthDeleteUser(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"bob"},
		"password": {"bobpass"},
	}, auth, http.StatusOK)
	postFormWithHeaders(t, handler, http.MethodDelete, "/v3/auth/user", url.Values{
		"username": {"bob"},
	}, auth, http.StatusOK)

	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/user/search?username=bob", nil, auth, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("search bob: %v", names)
	}
}

func TestAuthUpdatePassword(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPut, "/v3/auth/user", url.Values{
		"username":    {"nacos"},
		"newPassword": {"newpass123"},
	}, auth, http.StatusOK)

	// Updating the admin password revokes tokens, so the old token is now invalid.
	// Login with the new password to verify the update took effect.
	postForm(t, handler, http.MethodPost, "/v3/auth/user/login", url.Values{
		"username": {"nacos"},
		"password": {"adminpass"},
	}, http.StatusUnauthorized)
	postForm(t, handler, http.MethodPost, "/v3/auth/user/login", url.Values{
		"username": {"nacos"},
		"password": {"newpass123"},
	}, http.StatusOK)
}

func TestAuthRoleCRUD(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"carol"},
		"password": {"carolpass"},
	}, auth, http.StatusOK)
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/role", url.Values{
		"role":     {"ops"},
		"username": {"carol"},
	}, auth, http.StatusOK)

	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/role/list?pageNo=1&pageSize=10&username=carol", nil, auth, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		TotalCount int `json:"totalCount"`
		PageItems  []struct {
			Role     string `json:"role"`
			Username string `json:"username"`
		} `json:"pageItems"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.TotalCount != 1 || page.PageItems[0].Role != "ops" {
		t.Fatalf("page = %+v", page)
	}

	postFormWithHeaders(t, handler, http.MethodDelete, "/v3/auth/role", url.Values{
		"role":     {"ops"},
		"username": {"carol"},
	}, auth, http.StatusOK)
}

func TestAuthPermissionCRUD(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/role", url.Values{
		"role":     {"ops"},
		"username": {"nacos"},
	}, auth, http.StatusOK)
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/permission", url.Values{
		"role":     {"ops"},
		"resource": {"namespace:public"},
		"action":   {"r"},
	}, auth, http.StatusOK)

	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/permission?role=ops&resource=namespace:public&action=r", nil, auth, http.StatusOK)
	if body.Data != true {
		t.Fatalf("permission exists = %v, want true", body.Data)
	}

	postFormWithHeaders(t, handler, http.MethodDelete, "/v3/auth/permission", url.Values{
		"role":     {"ops"},
		"resource": {"namespace:public"},
		"action":   {"r"},
	}, auth, http.StatusOK)

	body = doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/permission?role=ops&resource=namespace:public&action=r", nil, auth, http.StatusOK)
	if body.Data != false {
		t.Fatalf("permission exists = %v, want false", body.Data)
	}
}

func TestAuthPermissionValidation(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	missing := postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/permission", url.Values{
		"role":     {""},
		"resource": {"res"},
		"action":   {"r"},
	}, auth, http.StatusBadRequest)
	if missing.Code != 10000 {
		t.Fatalf("code = %d, want 10000", missing.Code)
	}
}

func TestAuthSearchUsers(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	auth := adminAuthHeader(t, handler, "adminpass")
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/auth/user", url.Values{
		"username": {"alice"},
		"password": {"alicepass"},
	}, auth, http.StatusOK)

	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/auth/user/search?username=ali", nil, auth, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "alice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("alice not found: %v", names)
	}
}

func postFormRaw(t *testing.T, handler http.Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// adminAuthHeader bootstraps the admin user with the given password, logs in,
// and returns the Authorization header for use in admin-only route tests.
func adminAuthHeader(t *testing.T, handler http.Handler, password string) map[string]string {
	t.Helper()
	postForm(t, handler, http.MethodPost, "/v3/auth/user/admin", url.Values{"password": {password}}, http.StatusOK)
	rec := postFormRaw(t, handler, http.MethodPost, "/v3/auth/user/login", url.Values{
		"username": {"nacos"},
		"password": {password},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	data, _ := json.Marshal(body.Data)
	var login map[string]any
	if err := json.Unmarshal(data, &login); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	token, _ := login["accessToken"].(string)
	if token == "" {
		t.Fatalf("missing accessToken in login response: %+v", login)
	}
	return map[string]string{"Authorization": "Bearer " + token}
}
