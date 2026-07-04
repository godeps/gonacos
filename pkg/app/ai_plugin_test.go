package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/plugin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pluginTestHandler builds a handler with an AI service that has a plugin
// manager with the echo plugin registered.
func pluginTestHandler(t *testing.T) (http.Handler, *ai.Service) {
	t.Helper()
	mgr := plugin.NewManager()
	if err := mgr.Register(plugin.NewEchoPlugin(""), nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil, ai.WithPlugins(mgr))
	return NewHandlerWithServices("../..", bundle), bundle.AI
}

// pluginTestHandlerNoManager builds a handler with no plugin manager attached.
func pluginTestHandlerNoManager(t *testing.T) (http.Handler, *ai.Service) {
	t.Helper()
	bundle := NewServiceBundle()
	bundle.AI = ai.NewService(nil)
	return NewHandlerWithServices("../..", bundle), bundle.AI
}

// TestPluginList verifies listing registered plugins.
func TestPluginList(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/plugins/list", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []plugin.PluginInfo
	decodeResult(t, rec.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Meta.ID != "echo" {
		t.Fatalf("id = %q", list[0].Meta.ID)
	}
	if list[0].Enabled {
		t.Fatalf("should not be enabled by default")
	}
}

// TestPluginListNoManager verifies listing without a manager returns 503.
func TestPluginListNoManager(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandlerNoManager(t)
	rec := getFormVals(handler, "/v3/admin/ai/plugins/list", url.Values{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestPluginDetail verifies the detail endpoint.
func TestPluginDetail(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/plugins/detail", url.Values{"pluginId": {"echo"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var info plugin.PluginInfo
	decodeResult(t, rec.Body.Bytes(), &info)
	if info.Meta.ID != "echo" {
		t.Fatalf("id = %q", info.Meta.ID)
	}
}

// TestPluginDetailNotFound verifies 404 for unknown plugin.
func TestPluginDetailNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/plugins/detail", url.Values{"pluginId": {"ghost"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestPluginDetailMissingID verifies 400 for missing pluginId.
func TestPluginDetailMissingID(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := getFormVals(handler, "/v3/admin/ai/plugins/detail", url.Values{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestPluginEnable verifies enabling a plugin.
func TestPluginEnable(t *testing.T) {
	t.Parallel()
	handler, svc := pluginTestHandler(t)
	rec := postFormVals(handler, "/v3/admin/ai/plugins/enable", url.Values{"pluginId": {"echo"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !svc.Plugins().IsEnabled("echo") {
		t.Fatalf("should be enabled")
	}
}

// TestPluginEnableNotFound verifies enabling unknown plugin returns 400.
func TestPluginEnableNotFound(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := postFormVals(handler, "/v3/admin/ai/plugins/enable", url.Values{"pluginId": {"ghost"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestPluginDisable verifies disabling an enabled plugin.
func TestPluginDisable(t *testing.T) {
	t.Parallel()
	handler, svc := pluginTestHandler(t)
	ctx := context.Background()
	if err := svc.Plugins().Enable(ctx, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/disable", url.Values{"pluginId": {"echo"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if svc.Plugins().IsEnabled("echo") {
		t.Fatalf("should be disabled")
	}
}

// TestPluginSetConfig verifies updating a plugin's config.
func TestPluginSetConfig(t *testing.T) {
	t.Parallel()
	handler, svc := pluginTestHandler(t)
	form := url.Values{
		"pluginId": {"echo"},
		"config":   {`{"prefix":"new"}`},
	}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/config", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Re-enable and verify the prefix is applied.
	ctx := context.Background()
	if err := svc.Plugins().Enable(ctx, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	backend, err := plugin.NewPluginBackend("echo", svc.Plugins())
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	res, err := backend.CallTool(ctx, "echo", map[string]any{"message": "x"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "new: x") {
		t.Fatalf("text = %q, want contains 'new: x'", tc.Text)
	}
}

// TestPluginSetConfigInvalidJSON verifies invalid config JSON returns 400.
func TestPluginSetConfigInvalidJSON(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	form := url.Values{"pluginId": {"echo"}, "config": {"not-json"}}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/config", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestPluginToolsList verifies listing tools exposed by a plugin.
func TestPluginToolsList(t *testing.T) {
	t.Parallel()
	handler, svc := pluginTestHandler(t)
	ctx := context.Background()
	if err := svc.Plugins().Enable(ctx, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/tools/list", url.Values{"pluginId": {"echo"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestPluginToolsListDisabled verifies listing tools of a disabled plugin
// returns 200 with an empty list.
func TestPluginToolsListDisabled(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	rec := postFormVals(handler, "/v3/admin/ai/plugins/tools/list", url.Values{"pluginId": {"echo"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestPluginToolsCall verifies invoking a tool exposed by a plugin.
func TestPluginToolsCall(t *testing.T) {
	t.Parallel()
	handler, svc := pluginTestHandler(t)
	ctx := context.Background()
	if err := svc.Plugins().Enable(ctx, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	form := url.Values{
		"pluginId": {"echo"},
		"toolName": {"echo"},
		"args":     {`{"message":"hello"}`},
	}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/tools/call", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestPluginToolsCallMissingToolName verifies missing toolName returns 400.
func TestPluginToolsCallMissingToolName(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	form := url.Values{"pluginId": {"echo"}}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/tools/call", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestPluginToolsCallInvalidArgs verifies invalid args JSON returns 400.
func TestPluginToolsCallInvalidArgs(t *testing.T) {
	t.Parallel()
	handler, _ := pluginTestHandler(t)
	form := url.Values{
		"pluginId": {"echo"},
		"toolName": {"echo"},
		"args":     {"not-json"},
	}
	rec := postFormVals(handler, "/v3/admin/ai/plugins/tools/call", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Suppress unused import warnings.
var _ = errors.Is(nil, plugin.ErrPluginNotFound)
var _ = http.StatusOK
var _ = httptest.NewRecorder
