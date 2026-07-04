package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestManagerRegisterAndList verifies basic registration and listing.
func TestManagerRegisterAndList(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	if err := mgr.Register(NewEchoPlugin(""), nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	list := mgr.List()
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

// TestManagerRegisterDuplicate verifies duplicate registration is rejected.
func TestManagerRegisterDuplicate(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), nil)
	err := mgr.Register(NewEchoPlugin(""), nil)
	if !errors.Is(err, ErrPluginExists) {
		t.Fatalf("err = %v, want ErrPluginExists", err)
	}
}

// TestManagerEnableAndCall verifies an enabled plugin's tool is callable
// through the PluginBackend.
func TestManagerEnableAndCall(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), Config{"prefix": "resp"})
	ctx := context.Background()
	if err := mgr.Enable(ctx, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !mgr.IsEnabled("echo") {
		t.Fatalf("should be enabled")
	}

	backend, err := NewPluginBackend("echo", mgr)
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if backend.Name() != "echo" {
		t.Fatalf("name = %q", backend.Name())
	}

	tools, err := backend.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := backend.CallTool(ctx, "echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", res.Content[0])
	}
	if tc.Text != "resp: hi" {
		t.Fatalf("text = %q", tc.Text)
	}
}

// TestManagerDisable verifies a disabled plugin's tools are not listed.
func TestManagerDisable(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), nil)
	ctx := context.Background()
	mgr.Enable(ctx, "echo")
	mgr.Disable("echo")

	if mgr.IsEnabled("echo") {
		t.Fatalf("should be disabled")
	}
	backend, _ := NewPluginBackend("echo", mgr)
	tools, _ := backend.ListTools(ctx)
	if len(tools) != 0 {
		t.Fatalf("disabled plugin should list no tools: %+v", tools)
	}
}

// TestManagerUnregister verifies a plugin is removed after unregister.
func TestManagerUnregister(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), nil)
	ctx := context.Background()
	mgr.Enable(ctx, "echo")
	if err := mgr.Unregister(ctx, "echo"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if len(mgr.List()) != 0 {
		t.Fatalf("list should be empty")
	}
}

// TestManagerSetConfig verifies config updates re-init the plugin.
func TestManagerSetConfig(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), Config{"prefix": "old"})
	ctx := context.Background()
	mgr.Enable(ctx, "echo")

	if err := mgr.SetConfig("echo", Config{"prefix": "new"}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	// Plugin should be disabled after re-init.
	if mgr.IsEnabled("echo") {
		t.Fatalf("should be disabled after re-init")
	}
	mgr.Enable(ctx, "echo")
	backend, _ := NewPluginBackend("echo", mgr)
	res, _ := backend.CallTool(ctx, "echo", map[string]any{"message": "x"})
	tc, _ := res.Content[0].(*mcp.TextContent)
	if tc.Text != "new: x" {
		t.Fatalf("text = %q, want 'new: x'", tc.Text)
	}
}

// TestPluginBackendDisabledPlugin verifies calling a disabled plugin returns
// ErrMissingToolName.
func TestPluginBackendDisabledPlugin(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), nil)
	// Not enabled.
	backend, _ := NewPluginBackend("echo", mgr)
	_, err := backend.CallTool(context.Background(), "echo", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, mcprouter.ErrMissingToolName) {
		t.Fatalf("err = %v, want ErrMissingToolName", err)
	}
}

// TestManagerGet verifies the Get endpoint.
func TestManagerGet(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	mgr.Register(NewEchoPlugin(""), nil)
	info, err := mgr.Get("echo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.Meta.ID != "echo" {
		t.Fatalf("id = %q", info.Meta.ID)
	}
	if _, err := mgr.Get("ghost"); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// TestEchoPluginNoMessage verifies the echo plugin handles missing message
// by stringifying the args.
func TestEchoPluginNoMessage(t *testing.T) {
	t.Parallel()
	p := NewEchoPlugin("echo")
	resp, err := p.HandleMCPTool(context.Background(), ToolRequest{
		Tool: "echo",
		Args: map[string]any{"other": "value"},
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.Content == "" {
		t.Fatalf("content is empty")
	}
	if !strings.Contains(resp.Content, "other") {
		t.Fatalf("content should mention 'other': %q", resp.Content)
	}
}
