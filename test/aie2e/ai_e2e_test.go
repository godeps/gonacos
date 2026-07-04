// Package aie2e validates gonacos's AI endpoints end-to-end against a running
// gonacos server. It exercises prompt/skill/MCP/pipeline/apitomcp/template/
// plugin/dify/import paths through the public HTTP API.
package aie2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	serverPort    = 18868
	grpcPort      = 19868
	serverHost    = "127.0.0.1"
	adminPassword = "nacos"
)

var token string

func TestMain(m *testing.M) {
	binary := os.Getenv("GONACOS_BINARY")
	if binary == "" {
		binary = "/tmp/gonacos-test"
	}
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintf(os.Stderr, "gonacos binary not found at %s: %v\n", binary, err)
		os.Exit(1)
	}

	dumpDir := filepath.Join(filepath.Dir(binary), ".gonacos-aie2e")
	_ = os.RemoveAll(dumpDir)

	if conn, err := net.Dial("tcp", net.JoinHostPort(serverHost, strconv.Itoa(serverPort))); err == nil {
		conn.Close()
		fmt.Fprintf(os.Stderr, "port %d already in use\n", serverPort)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "serve", net.JoinHostPort(serverHost, strconv.Itoa(serverPort)))
	cmd.Dir = filepath.Dir(binary)
	// Isolate snapshot data so each run starts fresh.
	cmd.Env = append(os.Environ(), "GONACOS_DATA_DIR="+dumpDir)
	// Discard server output to keep test logs clean.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "start gonacos: %v\n", err)
		os.Exit(1)
	}

	if !waitForServer(serverHost, serverPort, 10*time.Second) {
		cancel()
		fmt.Fprintf(os.Stderr, "gonacos did not become ready\n")
		os.Exit(1)
	}
	if !bootstrapAdmin() {
		cancel()
		fmt.Fprintf(os.Stderr, "bootstrap admin failed\n")
		os.Exit(1)
	}
	if !loginAdmin() {
		cancel()
		fmt.Fprintf(os.Stderr, "login failed\n")
		os.Exit(1)
	}

	exitCode := m.Run()
	cancel()
	_, _ = cmd.Process.Wait()
	os.Exit(exitCode)
}

func waitForServer(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func bootstrapAdmin() bool {
	u := fmt.Sprintf("http://%s/v3/auth/user/admin", net.JoinHostPort(serverHost, strconv.Itoa(serverPort)))
	resp, err := http.Post(u, "application/x-www-form-urlencoded", strings.NewReader("password="+adminPassword))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// Status 200 = newly created. Status 400 with code 409 = already exists.
	if resp.StatusCode == 200 {
		return true
	}
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Code == 409 {
		return true
	}
	return false
}

func loginAdmin() bool {
	u := fmt.Sprintf("http://%s/v3/auth/user/login", net.JoinHostPort(serverHost, strconv.Itoa(serverPort)))
	resp, err := http.Post(u, "application/x-www-form-urlencoded", strings.NewReader("username=nacos&password="+adminPassword))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	if body.Data.AccessToken == "" {
		return false
	}
	token = body.Data.AccessToken
	return true
}

// postForm sends a POST with form-encoded body and returns the body.
func postForm(t *testing.T, path string, form url.Values) (int, []byte) {
	t.Helper()
	return doForm(t, http.MethodPost, path, form)
}

// deleteForm sends a DELETE with form-encoded body and returns the body.
func deleteForm(t *testing.T, path string, form url.Values) (int, []byte) {
	t.Helper()
	return doForm(t, http.MethodDelete, path, form)
}

// doForm sends an arbitrary method with form-encoded body.
func doForm(t *testing.T, method, path string, form url.Values) (int, []byte) {
	t.Helper()
	u := "http://" + net.JoinHostPort(serverHost, strconv.Itoa(serverPort)) + path
	req, err := http.NewRequest(method, u, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// getForm sends a GET with query params and returns the body.
func getForm(t *testing.T, path string, form url.Values) (int, []byte) {
	t.Helper()
	u := "http://" + net.JoinHostPort(serverHost, strconv.Itoa(serverPort)) + path + "?" + form.Encode()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// postJSON sends a POST with a JSON body and returns the body.
func postJSON(t *testing.T, path string, body any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(body)
	u := "http://" + net.JoinHostPort(serverHost, strconv.Itoa(serverPort)) + path
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

// resultEnvelope is the standard gonacos response envelope.
type resultEnvelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	Timestamp int64           `json:"timestamp"`
}

func decodeData(t *testing.T, body []byte, dst any) {
	t.Helper()
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v (body: %s)", err, string(body))
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, message = %s (body: %s)", env.Code, env.Message, string(body))
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode data: %v (data: %s)", err, string(env.Data))
	}
}

// TestE2EPromptLifecycle exercises the prompt CRUD + lifecycle (draft → submit → publish → online).
func TestE2EPromptLifecycle(t *testing.T) {
	id := "e2e-prompt-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// Create draft.
	form := url.Values{
		"id":          {id},
		"name":        {id},
		"content":     {"You are a helpful assistant."},
		"author":      {"e2e"},
		"description": {"e2e prompt"},
	}
	status, body := postForm(t, "/v3/admin/ai/prompt/draft", form)
	if status != 200 {
		t.Fatalf("create draft: status %d, body %s", status, string(body))
	}

	// Submit.
	status, body = postForm(t, "/v3/admin/ai/prompt/submit", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("submit: status %d, body %s", status, string(body))
	}

	// Publish.
	status, body = postForm(t, "/v3/admin/ai/prompt/publish", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("publish: status %d, body %s", status, string(body))
	}

	// List.
	status, body = getForm(t, "/v3/admin/ai/prompt/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	found := false
	for _, p := range list {
		if p["id"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("prompt %q not found in list", id)
	}

	// Detail.
	status, body = getForm(t, "/v3/admin/ai/prompt/detail", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("detail: status %d, body %s", status, string(body))
	}
	var detail map[string]any
	decodeData(t, body, &detail)
	if detail["id"] != id {
		t.Fatalf("detail id = %v", detail["id"])
	}

	// Online + Offline.
	status, body = postForm(t, "/v3/admin/ai/prompt/online", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("online: status %d, body %s", status, string(body))
	}
	status, body = postForm(t, "/v3/admin/ai/prompt/offline", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("offline: status %d, body %s", status, string(body))
	}

	// Delete.
	status, body = deleteForm(t, "/v3/admin/ai/prompt", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("delete: status %d, body %s", status, string(body))
	}
}

// TestE2ESkillLifecycle exercises the skill CRUD + lifecycle.
func TestE2ESkillLifecycle(t *testing.T) {
	id := "e2e-skill-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	form := url.Values{
		"id":          {id},
		"name":        {id},
		"content":     {"skill body"},
		"author":      {"e2e"},
		"description": {"e2e skill"},
	}
	status, body := postForm(t, "/v3/admin/ai/skills/draft", form)
	if status != 200 {
		t.Fatalf("create draft: status %d, body %s", status, string(body))
	}
	status, body = postForm(t, "/v3/admin/ai/skills/submit", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("submit: status %d, body %s", status, string(body))
	}
	status, body = postForm(t, "/v3/admin/ai/skills/publish", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("publish: status %d, body %s", status, string(body))
	}
	status, body = getForm(t, "/v3/admin/ai/skills/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	found := false
	for _, s := range list {
		if s["id"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("skill %q not found", id)
	}
	status, body = deleteForm(t, "/v3/admin/ai/skills", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("delete: status %d, body %s", status, string(body))
	}
}

// TestE2EMcpServerLifecycle exercises MCP server CRUD + tool import.
func TestE2EMcpServerLifecycle(t *testing.T) {
	id := "e2e-mcp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	form := url.Values{
		"id":       {id},
		"name":     {id},
		"protocol": {"http"},
		"endpoint": {""},
	}
	status, body := postForm(t, "/v3/admin/ai/mcp", form)
	if status != 200 {
		t.Fatalf("create mcp: status %d, body %s", status, string(body))
	}
	status, body = getForm(t, "/v3/admin/ai/mcp/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	found := false
	for _, m := range list {
		if m["id"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mcp %q not found", id)
	}
	// Import tools via console endpoint (returns local tools when no endpoint).
	status, body = getForm(t, "/v3/console/ai/mcp/importToolsFromMcp", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("import: status %d, body %s", status, string(body))
	}
	// Delete via DELETE method on /v3/admin/ai/mcp.
	status, body = deleteForm(t, "/v3/admin/ai/mcp", url.Values{"id": {id}})
	if status != 200 {
		t.Fatalf("delete: status %d, body %s", status, string(body))
	}
}

// TestE2EPipelineCRUD exercises pipeline create/list/get/delete.
func TestE2EPipelineCRUD(t *testing.T) {
	id := "e2e-pipe-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	form := url.Values{
		"pipelineId": {id},
		"name":       {id},
		"description": {"e2e pipeline"},
	}
	status, body := postForm(t, "/v3/admin/ai/pipelines/create", form)
	if status != 200 {
		t.Fatalf("create: status %d, body %s", status, string(body))
	}
	status, body = getForm(t, "/v3/admin/ai/pipelines/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	found := false
	for _, p := range list {
		if p["pipelineId"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pipeline %q not found", id)
	}
	status, body = getForm(t, "/v3/admin/ai/pipelines/detail", url.Values{"pipelineId": {id}})
	if status != 200 {
		t.Fatalf("detail: status %d, body %s", status, string(body))
	}
	status, body = deleteForm(t, "/v3/admin/ai/pipelines/delete", url.Values{"pipelineId": {id}})
	if status != 200 {
		t.Fatalf("delete: status %d, body %s", status, string(body))
	}
}

// TestE2EApitomcpCRUD exercises the API→MCP converter CRUD.
func TestE2EApitomcpCRUD(t *testing.T) {
	yaml := `
server:
  name: e2e-pingapi
tools:
  - name: ping
    description: ping
    requestTemplate:
      method: GET
      url: http://example.com
    responseTemplate:
      body: "PONG"
`
	form := url.Values{"yaml": {yaml}}
	status, body := postForm(t, "/v3/admin/ai/apitomcp/create", form)
	if status != 200 {
		t.Fatalf("create: status %d, body %s", status, string(body))
	}
	status, body = getForm(t, "/v3/admin/ai/apitomcp/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	found := false
	for _, c := range list {
		if c["name"] == "e2e-pingapi" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("apitomcp config not found")
	}
	status, body = deleteForm(t, "/v3/admin/ai/apitomcp/delete", url.Values{"name": {"e2e-pingapi"}})
	if status != 200 {
		t.Fatalf("delete: status %d, body %s", status, string(body))
	}
}

// TestE2ETemplateList verifies the MCP template list endpoint returns builtins.
func TestE2ETemplateList(t *testing.T) {
	status, body := getForm(t, "/v3/admin/ai/mcp/templates/list", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	if len(list) == 0 {
		t.Fatalf("expected built-in templates")
	}
}

// TestE2EDifyConfig exercises the Dify config get/set endpoints.
func TestE2EDifyConfig(t *testing.T) {
	// Get initial config (configured=false).
	status, body := getForm(t, "/v3/admin/ai/dify/config", url.Values{})
	if status != 200 {
		t.Fatalf("get config: status %d, body %s", status, string(body))
	}
	// Set a new config.
	form := url.Values{"endpoint": {"https://api.dify.ai"}, "apiKey": {"my-key"}}
	status, body = postForm(t, "/v3/admin/ai/dify/config", form)
	if status != 200 {
		t.Fatalf("set config: status %d, body %s", status, string(body))
	}
	// Verify the config is now configured=true.
	status, body = getForm(t, "/v3/admin/ai/dify/config", url.Values{})
	if status != 200 {
		t.Fatalf("get config after set: status %d, body %s", status, string(body))
	}
	var cfg map[string]any
	decodeData(t, body, &cfg)
	if !cfg["configured"].(bool) {
		t.Fatalf("expected configured=true")
	}
	if cfg["endpoint"] != "https://api.dify.ai" {
		t.Fatalf("endpoint = %v", cfg["endpoint"])
	}
}

// TestE2EDifyManifest verifies the Dify manifest endpoint.
func TestE2EDifyManifest(t *testing.T) {
	form := url.Values{"routerUrl": {"/v3/ai/mcp/router"}, "toolName": {"dify.run"}}
	status, body := getForm(t, "/v3/admin/ai/dify/manifest", form)
	if status != 200 {
		t.Fatalf("manifest: status %d, body %s", status, string(body))
	}
	var m map[string]any
	decodeData(t, body, &m)
	if m["routerUrl"] != "/v3/ai/mcp/router" {
		t.Fatalf("routerUrl = %v", m["routerUrl"])
	}
	if m["toolName"] != "dify.run" {
		t.Fatalf("toolName = %v", m["toolName"])
	}
}

// TestE2EPluginList verifies the plugin list endpoint. By default no plugin
// manager is configured, so this returns 503.
func TestE2EPluginList(t *testing.T) {
	status, _ := getForm(t, "/v3/admin/ai/plugins/list", url.Values{})
	// Without a plugin manager configured, this returns 503. That's expected
	// for the default gonacos build.
	if status != http.StatusServiceUnavailable && status != http.StatusOK {
		t.Fatalf("expected 503 or 200, got %d", status)
	}
}

// TestE2EImportSources verifies listing import sources returns at least the
// builtin source.
func TestE2EImportSources(t *testing.T) {
	status, body := getForm(t, "/v3/admin/ai/import/sources", url.Values{})
	if status != 200 {
		t.Fatalf("list: status %d, body %s", status, string(body))
	}
	var list []map[string]any
	decodeData(t, body, &list)
	if len(list) == 0 {
		t.Fatalf("expected at least one import source")
	}
}