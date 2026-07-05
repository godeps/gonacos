package config

import (
	"encoding/json"
	"testing"
)

func TestConfigSnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "app.yml",
		Content:     "key: value",
		Type:        "yaml",
		SrcUser:     "admin",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "app.yml",
		Content:     "key: updated",
		Type:        "yaml",
		SrcUser:     "admin",
	}); err != nil {
		t.Fatalf("publish update: %v", err)
	}
	if err := s.PublishBeta(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "app-beta.yml",
		Content:     "beta: true",
		Type:        "yaml",
		BetaIPs:     "10.0.0.1,10.0.0.2",
	}); err != nil {
		t.Fatalf("publish beta: %v", err)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "config" {
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

	restored := NewService()
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}

	item, err := restored.Get("public", "DEFAULT_GROUP", "app.yml")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if item.Content != "key: updated" {
		t.Fatalf("content = %v, want 'key: updated'", item.Content)
	}
	history, err := restored.HistoryList("public", "DEFAULT_GROUP", "app.yml", 1, 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if history.TotalCount < 2 {
		t.Fatalf("history count = %d, want >=2", history.TotalCount)
	}
	beta, err := restored.GetBeta("public", "DEFAULT_GROUP", "app-beta.yml")
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if beta.Content != "beta: true" {
		t.Fatalf("beta content = %v", beta.Content)
	}
}

func TestConfigSnapshotEmptyService(t *testing.T) {
	t.Parallel()
	s := NewService()
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := json.Marshal(snap)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	restored := NewService()
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
}
