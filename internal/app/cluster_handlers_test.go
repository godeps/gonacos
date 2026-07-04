package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestClusterNodeSelfAndList(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/cluster/node/self", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var self map[string]any
	if err := json.Unmarshal(data, &self); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if self["isSelf"] != true || self["state"] != "UP" {
		t.Fatalf("self = %+v", self)
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/admin/core/cluster/node/list", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var members []map[string]any
	if err := json.Unmarshal(data, &members); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/core/cluster/nodes", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	if err := json.Unmarshal(data, &members); err != nil {
		t.Fatalf("unmarshal console: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("console members = %d, want 1", len(members))
	}
}

func TestClusterLookupUpdate(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := postForm(t, handler, http.MethodPut, "/v3/admin/core/cluster/lookup", url.Values{
		"type": {"health"},
	}, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var members []map[string]any
	if err := json.Unmarshal(data, &members); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}
}

func TestPluginListAndDetail(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/plugin/list", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var plugins []map[string]any
	if err := json.Unmarshal(data, &plugins); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(plugins) == 0 {
		t.Fatalf("no plugins")
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/admin/core/plugin/detail?pluginId=nacos-default", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var plugin map[string]any
	if err := json.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if plugin["id"] != "nacos-default" {
		t.Fatalf("plugin id = %v", plugin["id"])
	}
}

func TestPluginStatusToggle(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := postForm(t, handler, http.MethodPut, "/v3/admin/core/plugin/status", url.Values{
		"pluginId": {"nacos-auth"},
		"enabled":  {"false"},
	}, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var plugin map[string]any
	if err := json.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if plugin["enabled"] != false {
		t.Fatalf("enabled = %v, want false", plugin["enabled"])
	}

	postForm(t, handler, http.MethodPut, "/v3/admin/core/plugin/status", url.Values{
		"pluginId": {"nacos-auth"},
		"enabled":  {"true"},
	}, http.StatusOK)
}

func TestPluginConfigUpdate(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := postForm(t, handler, http.MethodPut, "/v3/admin/core/plugin/config", url.Values{
		"pluginId": {"nacos-default"},
		"config":   {`{"key":"value"}`},
	}, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var plugin map[string]any
	if err := json.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	config, _ := plugin["config"].(map[string]any)
	if config["key"] != "value" {
		t.Fatalf("config = %+v", config)
	}
}

func TestPluginAvailability(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/plugin/availability", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var avail []map[string]any
	if err := json.Unmarshal(data, &avail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(avail) == 0 {
		t.Fatalf("no availability entries")
	}
}

func TestOpsIDsAndLog(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/ops/ids", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var ids map[string]any
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ids["mode"] != "standalone" {
		t.Fatalf("mode = %v", ids["mode"])
	}

	body = postForm(t, handler, http.MethodPut, "/v3/admin/core/ops/log", url.Values{
		"logLevel": {"DEBUG"},
	}, http.StatusOK)
	if body.Data != "DEBUG" {
		t.Fatalf("logLevel = %v, want DEBUG", body.Data)
	}
}

func TestOpsRaftStandaloneUnavailable(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := postForm(t, handler, http.MethodPost, "/v3/admin/core/ops/raft", url.Values{
		"command": {"snapshot"},
		"groupId": {"group-1"},
	}, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var raft map[string]any
	if err := json.Unmarshal(data, &raft); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raft["available"] != false {
		t.Fatalf("raft available in standalone: %+v", raft)
	}
}

func TestLoaderStubs(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/loader/cluster", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var metrics map[string]any
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if metrics["mode"] != "standalone" {
		t.Fatalf("mode = %v", metrics["mode"])
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/admin/core/loader/current", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var clients map[string]any
	if err := json.Unmarshal(data, &clients); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if clients["count"] != float64(0) {
		t.Fatalf("count = %v", clients["count"])
	}

	body = postForm(t, handler, http.MethodPost, "/v3/admin/core/loader/reloadClient", url.Values{
		"clientId": {"client-1"},
	}, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var reload map[string]any
	if err := json.Unmarshal(data, &reload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reload["reloaded"] != false {
		t.Fatalf("reloaded = %v", reload["reloaded"])
	}

	body = postForm(t, handler, http.MethodPost, "/v3/admin/core/loader/reloadCurrent", nil, http.StatusOK)
	body = postForm(t, handler, http.MethodPost, "/v3/admin/core/loader/smartReloadCluster", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	if err := json.Unmarshal(data, &reload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reload["reloaded"] != float64(0) {
		t.Fatalf("smart reload = %+v", reload)
	}
}

func TestPluginDetailMissingReturns400(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	result := doJSON(t, handler, http.MethodGet, "/v3/admin/core/plugin/detail", nil, http.StatusBadRequest)
	if result.Code != 10000 {
		t.Fatalf("code = %d, want 10000 (parameter missing)", result.Code)
	}
}

func TestPluginDetailNotFoundReturns404(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	result := doJSON(t, handler, http.MethodGet, "/v3/admin/core/plugin/detail?pluginId=missing", nil, http.StatusNotFound)
	if result.Code != 404 {
		t.Fatalf("code = %d, want 404", result.Code)
	}
}

func TestClusterSnapshotRestoreStatus(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	// 1. Status endpoint reflects standalone mode.
	statusBody := doJSON(t, handler, http.MethodGet, "/v3/admin/core/cluster/status", nil, http.StatusOK)
	statusData, _ := json.Marshal(statusBody.Data)
	var status struct {
		Mode              string `json:"mode"`
		MemberCount       int    `json:"memberCount"`
		SnapshotAvailable bool   `json:"snapshotAvailable"`
		SnapshotKey       string `json:"snapshotKey"`
	}
	if err := json.Unmarshal(statusData, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Mode != "standalone" {
		t.Fatalf("mode = %s, want standalone", status.Mode)
	}
	if status.MemberCount != 1 {
		t.Fatalf("memberCount = %d, want 1", status.MemberCount)
	}
	if !status.SnapshotAvailable {
		t.Fatal("snapshotAvailable = false, want true")
	}
	if status.SnapshotKey != "cluster" {
		t.Fatalf("snapshotKey = %s, want cluster", status.SnapshotKey)
	}

	// 2. Snapshot returns the cluster state.
	snapBody := doJSON(t, handler, http.MethodGet, "/v3/admin/core/cluster/snapshot", nil, http.StatusOK)
	snapData, _ := json.Marshal(snapBody.Data)
	var snap struct {
		Members  []map[string]any `json:"members"`
		Plugins  []map[string]any `json:"plugins"`
		LogLevel string           `json:"logLevel"`
	}
	if err := json.Unmarshal(snapData, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(snap.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(snap.Members))
	}
	if snap.Members[0]["isSelf"] != true {
		t.Fatal("self member not marked")
	}
	if snap.LogLevel != "INFO" {
		t.Fatalf("logLevel = %s, want INFO", snap.LogLevel)
	}
	if len(snap.Plugins) == 0 {
		t.Fatal("plugins empty")
	}

	// 3. Restore rejects malformed bodies.
	restoreErr := postJSON(t, handler, "/v3/admin/core/cluster/restore", "not-json", http.StatusBadRequest)
	if restoreErr.Code != 10001 {
		t.Fatalf("code = %d, want 10001 (parameter validate)", restoreErr.Code)
	}

	// 4. Restore accepts a valid envelope and re-applies state.
	restoreReq := postJSON(t, handler, "/v3/admin/core/cluster/restore",
		`{"data":{"members":[{"id":"restored-node","ip":"10.0.0.5","port":8848,"state":"UP","isSelf":false}],"plugins":[],"logLevel":"DEBUG"}}`,
		http.StatusOK)
	if restoreReq.Data == nil {
		t.Fatal("restore data empty")
	}
	restoredData, _ := json.Marshal(restoreReq.Data)
	var restored struct {
		Restored string `json:"restored"`
	}
	if err := json.Unmarshal(restoredData, &restored); err != nil {
		t.Fatalf("unmarshal restore: %v", err)
	}
	if restored.Restored != "cluster" {
		t.Fatalf("restored = %s, want cluster", restored.Restored)
	}

	// 5. Status now shows 2 members (self + restored node).
	statusBody2 := doJSON(t, handler, http.MethodGet, "/v3/admin/core/cluster/status", nil, http.StatusOK)
	statusData2, _ := json.Marshal(statusBody2.Data)
	var status2 struct {
		MemberCount int    `json:"memberCount"`
		LogLevel    string `json:"logLevel"`
	}
	_ = json.Unmarshal(statusData2, &status2)
	if status2.MemberCount != 2 {
		t.Fatalf("memberCount after restore = %d, want 2", status2.MemberCount)
	}
	if status2.LogLevel != "DEBUG" {
		t.Fatalf("logLevel after restore = %s, want DEBUG", status2.LogLevel)
	}
}
