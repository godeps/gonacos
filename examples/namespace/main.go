// Command namespace demonstrates namespace CRUD against a running gonacos
// server via the v3 HTTP API. It exercises list, create, get, delete.
//
// Prerequisites:
//   - gonacos server running on 127.0.0.1:8848
//   - admin user bootstrapped (POST /v3/auth/user/admin?password=nacos)
//
// Run from the repo root:
//
//	GOWORK=off go run ./examples/namespace
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
	token := login(baseURL, username, password)
	fmt.Printf("✓ login as %s (token length=%d)\n", username, len(token))

	// 1. List namespaces — should include the built-in "public".
	list := listNamespaces(baseURL, token)
	if !hasNamespaceID(list, "public") {
		log.Fatalf("list: expected built-in 'public' namespace, got %v", list)
	}
	fmt.Printf("✓ list namespaces (%d entries, includes 'public')\n", len(list))

	// 2. Create a new namespace.
	nsID := fmt.Sprintf("example-ns-%d", time.Now().UnixNano())
	createNamespace(baseURL, token, nsID, "Example Namespace", "created by examples/namespace")
	fmt.Printf("✓ create namespace %q\n", nsID)

	// 3. Get its detail.
	detail := getNamespace(baseURL, token, nsID)
	if detail["namespace"] != nsID {
		log.Fatalf("get: namespace = %q, want %q", detail["namespace"], nsID)
	}
	fmt.Printf("✓ get namespace detail (showName=%q desc=%q)\n",
		detail["namespaceShowName"], detail["namespaceDesc"])

	// 4. List again — should now include the new one.
	list = listNamespaces(baseURL, token)
	if !hasNamespaceID(list, nsID) {
		log.Fatalf("list after create: %q not in %v", nsID, list)
	}
	fmt.Printf("✓ list namespaces (%d entries, includes %q)\n", len(list), nsID)

	// 5. Delete it.
	deleteNamespace(baseURL, token, nsID)
	fmt.Printf("✓ delete namespace %q\n", nsID)

	// 6. List — should not include the deleted one.
	list = listNamespaces(baseURL, token)
	if hasNamespaceID(list, nsID) {
		log.Fatalf("list after delete: %q still present in %v", nsID, list)
	}
	fmt.Printf("✓ list namespaces (%d entries, %q gone)\n", len(list), nsID)

	fmt.Println("\nnamespace example: ALL STEPS PASSED")
}

// — HTTP helpers — //

func login(base, user, pass string) string {
	body := url.Values{"username": {user}, "password": {pass}}.Encode()
	resp := doRequest(base+"/v3/auth/user/login", http.MethodPost, body, "application/x-www-form-urlencoded", "")
	defer resp.Body.Close()
	var result struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("login decode: %v", err)
	}
	if result.Code != 0 || result.Data.AccessToken == "" {
		log.Fatalf("login: code=%d token=%q", result.Code, result.Data.AccessToken)
	}
	return result.Data.AccessToken
}

func listNamespaces(base, token string) []map[string]any {
	resp := doRequest(base+"/v3/console/core/namespace/list", http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int              `json:"code"`
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("list namespaces decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("list namespaces: code=%d body=%s", result.Code, string(b))
	}
	return result.Data
}

func getNamespace(base, token, nsID string) map[string]any {
	resp := doRequest(base+"/v3/console/core/namespace?namespaceId="+url.QueryEscape(nsID), http.MethodGet, "", "", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Fatalf("get namespace decode: %v (body=%s)", err, string(b))
	}
	if result.Code != 0 {
		log.Fatalf("get namespace: code=%d body=%s", result.Code, string(b))
	}
	return result.Data
}

func createNamespace(base, token, id, name, desc string) {
	body := url.Values{
		"namespaceId":   {id},
		"namespaceName": {name},
		"namespaceDesc": {desc},
	}.Encode()
	resp := doRequest(base+"/v3/admin/core/namespace", http.MethodPost, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("create namespace: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
}

func deleteNamespace(base, token, id string) {
	body := url.Values{"namespaceId": {id}}.Encode()
	resp := doRequest(base+"/v3/admin/core/namespace", http.MethodDelete, body, "application/x-www-form-urlencoded", token)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &result)
	if result.Code != 0 {
		log.Fatalf("delete namespace: code=%d msg=%q body=%s", result.Code, result.Message, string(b))
	}
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

func hasNamespaceID(list []map[string]any, id string) bool {
	for _, ns := range list {
		if v, _ := ns["namespace"].(string); v == id {
			return true
		}
	}
	return false
}
