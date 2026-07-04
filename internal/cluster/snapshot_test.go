package cluster

import (
	"encoding/json"
	"testing"
)

func TestClusterSnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "127.0.0.1", 8848, 9848, 9849)
	s.SetLogLevel("DEBUG")
	plugin, err := s.UpdatePluginConfig("nacos-auth", map[string]string{"token.ttl": "3600"})
	if err != nil {
		t.Fatalf("update plugin config: %v", err)
	}
	_ = plugin

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "cluster" {
		t.Fatalf("key = %v", s.SnapshotKey())
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	restored := NewService(ModeStandalone, "127.0.0.1", 8848, 9848, 9849)
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if restored.LogLevel() != "DEBUG" {
		t.Fatalf("log level = %v, want DEBUG", restored.LogLevel())
	}
	authPlugin, err := restored.GetPlugin("nacos-auth")
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	if authPlugin.Config["token.ttl"] != "3600" {
		t.Fatalf("plugin config = %v", authPlugin.Config)
	}
	self := restored.Self()
	if self == nil || !self.IsSelf {
		t.Fatal("self member should be preserved after restore")
	}
}

func TestClusterSnapshotPreservesSelf(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "127.0.0.1", 8848, 9848, 9849)
	snap, _ := s.Snapshot()
	raw, _ := json.Marshal(snap)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)

	restored := NewService(ModeStandalone, "10.0.0.99", 9000, 10000, 10001)
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}
	self := restored.Self()
	if self.IP != "10.0.0.99" {
		t.Fatalf("self IP = %v, want 10.0.0.99 (current process identity)", self.IP)
	}
}
