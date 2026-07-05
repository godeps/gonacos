// Command auth demonstrates the auth/RBAC HTTP API against a running
// gonacos server. It exercises login, user CRUD, role CRUD, and permission
// CRUD with the access token attached to every protected request.
//
// Prerequisites:
//   - gonacos server running on 127.0.0.1:8848
//   - admin user bootstrapped (POST /v3/auth/user/admin?password=nacos)
//
// Run from the repo root:
//
//	GOWORK=off go run ./examples/auth
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL  = "http://127.0.0.1:8848"
	username = "nacos"
	password = "nacos"
)

func main() {
	adminToken := login(baseURL, username, password)
	fmt.Printf("✓ admin login (token length=%d)\n", len(adminToken))

	// 1. Create a non-admin user.
	userName := fmt.Sprintf("example-user-%d", time.Now().UnixNano())
	userPass := "user-p@ss"
	createUser(baseURL, adminToken, userName, userPass)
	fmt.Printf("✓ create user %q\n", userName)

	// 2. List users — should include admin + the new one.
	users := listUsers(baseURL, adminToken)
	if !containsUser(users, username) || !containsUser(users, userName) {
		log.Fatalf("list users: expected both %q and %q in %v", username, userName, users)
	}
	fmt.Printf("✓ list users (%d entries, includes admin + %q)\n", len(users), userName)

	// 3. Login as the new user (proves the password works and the user
	// can authenticate independently).
	userToken := login(baseURL, userName, userPass)
	fmt.Printf("✓ %q login (token length=%d)\n", userName, len(userToken))

	// 4. Create a role for the new user.
	roleName := fmt.Sprintf("ROLE_EXAMPLE_%d", time.Now().UnixNano())
	createRole(baseURL, adminToken, roleName, userName)
	fmt.Printf("✓ create role %q for user %q\n", roleName, userName)

	// 5. List roles — should include the new role.
	roles := listRoles(baseURL, adminToken, userName)
	if !containsRole(roles, roleName, userName) {
		log.Fatalf("list roles: expected %q for %q in %v", roleName, userName, roles)
	}
	fmt.Printf("✓ list roles (%d entries)\n", len(roles))

	// 6. Create a permission for the role.
	resource := "*"
	action := "r"
	createPermission(baseURL, adminToken, roleName, resource, action)
	fmt.Printf("✓ create permission (role=%q resource=%q action=%q)\n", roleName, resource, action)

	// 7. List permissions — should include the new permission.
	perms := listPermissions(baseURL, adminToken, roleName)
	if !containsPermission(perms, roleName, resource, action) {
		log.Fatalf("list permissions: expected (%q,%q,%q) in %v", roleName, resource, action, perms)
	}
	fmt.Printf("✓ list permissions (%d entries)\n", len(perms))

	// 8. hasPermission check — should return true.
	if !hasPermission(baseURL, adminToken, roleName, resource, action) {
		log.Fatalf("hasPermission: expected true for (%q,%q,%q)", roleName, resource, action)
	}
	fmt.Printf("✓ hasPermission(role=%q, resource=%q, action=%q) = true\n", roleName, resource, action)

	// 9. Cleanup: delete permission, role, user.
	deletePermission(baseURL, adminToken, roleName, resource, action)
	fmt.Printf("✓ delete permission\n")
	deleteRole(baseURL, adminToken, roleName, userName)
	fmt.Printf("✓ delete role\n")
	deleteUser(baseURL, adminToken, userName)
	fmt.Printf("✓ delete user\n")

	// 10. Verify the user is gone.
	users = listUsers(baseURL, adminToken)
	if containsUser(users, userName) {
		log.Fatalf("list after delete: %q still present in %v", userName, users)
	}
	fmt.Printf("✓ user %q no longer in list\n", userName)

	fmt.Println("\nauth example: ALL STEPS PASSED")
}

// — HTTP helpers — //

func login(base, user, pass string) string {
	body := url.Values{"username": {user}, "password": {pass}}.Encode()
	resp := doRequest(base+"/v3/auth/user/login", http.MethodPost, body, "application/x-www-form-urlencoded", "")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("login decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 || result.Data.AccessToken == "" {
		log.Fatalf("login as %q: code=%d body=%s", user, result.Code, string(b))
	}
	return result.Data.AccessToken
}

func createUser(base, token, user, pass string) {
	body := url.Values{"username": {user}, "password": {pass}}.Encode()
	resp := doRequest(base+"/v3/auth/user", http.MethodPost, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("create user: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func deleteUser(base, token, user string) {
	body := url.Values{"username": {user}}.Encode()
	resp := doRequest(base+"/v3/auth/user", http.MethodDelete, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("delete user: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func listUsers(base, token string) []map[string]any {
	resp := doRequest(base+"/v3/auth/user/list", http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int `json:"code"`
		Data struct {
			PageItems []map[string]any `json:"pageItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("list users decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("list users: code=%d body=%s", result.Code, string(b))
	}
	return result.Data.PageItems
}

func createRole(base, token, role, user string) {
	body := url.Values{"role": {role}, "username": {user}}.Encode()
	resp := doRequest(base+"/v3/auth/role", http.MethodPost, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("create role: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func deleteRole(base, token, role, user string) {
	body := url.Values{"role": {role}, "username": {user}}.Encode()
	resp := doRequest(base+"/v3/auth/role", http.MethodDelete, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("delete role: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func listRoles(base, token, user string) []map[string]any {
	resp := doRequest(base+"/v3/auth/role/list?username="+url.QueryEscape(user), http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int `json:"code"`
		Data struct {
			PageItems []map[string]any `json:"pageItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("list roles decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("list roles: code=%d body=%s", result.Code, string(b))
	}
	return result.Data.PageItems
}

func createPermission(base, token, role, resource, action string) {
	body := url.Values{"role": {role}, "resource": {resource}, "action": {action}}.Encode()
	resp := doRequest(base+"/v3/auth/permission", http.MethodPost, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("create permission: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func deletePermission(base, token, role, resource, action string) {
	body := url.Values{"role": {role}, "resource": {resource}, "action": {action}}.Encode()
	resp := doRequest(base+"/v3/auth/permission", http.MethodDelete, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int
		Message string
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("delete permission: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func listPermissions(base, token, role string) []map[string]any {
	resp := doRequest(base+"/v3/auth/permission/list?role="+url.QueryEscape(role), http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int `json:"code"`
		Data struct {
			PageItems []map[string]any `json:"pageItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("list permissions decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("list permissions: code=%d body=%s", result.Code, string(b))
	}
	return result.Data.PageItems
}

func hasPermission(base, token, role, resource, action string) bool {
	resp := doRequest(base+"/v3/auth/permission/has?role="+url.QueryEscape(role)+"&resource="+url.QueryEscape(resource)+"&action="+url.QueryEscape(action), http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int  `json:"code"`
		Data bool `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("hasPermission decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("hasPermission: code=%d body=%s", result.Code, string(b))
	}
	return result.Data
}

func doRequest(url, method, body, contentType, token string) *http.Response {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		log.Fatalf("new request %s %s: %v", method, url, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("do request %s %s: %v", method, url, err)
	}
	return resp
}

func containsUser(users []map[string]any, name string) bool {
	for _, u := range users {
		if v, _ := u["username"].(string); v == name {
			return true
		}
	}
	return false
}

func containsRole(roles []map[string]any, role, user string) bool {
	for _, r := range roles {
		roleV, _ := r["role"].(string)
		userV, _ := r["username"].(string)
		if roleV == role && (user == "" || userV == user) {
			return true
		}
	}
	return false
}

func containsPermission(perms []map[string]any, role, resource, action string) bool {
	for _, p := range perms {
		roleV, _ := p["role"].(string)
		resV, _ := p["resource"].(string)
		actV, _ := p["action"].(string)
		if roleV == role && resV == resource && actV == action {
			return true
		}
	}
	return false
}
