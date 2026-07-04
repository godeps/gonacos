package naming

import (
	"encoding/json"
	"testing"
)

func TestNamingSnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService()
	defer s.Stop()

	if err := s.CreateService("public", "DEFAULT_GROUP", "orders", true, 0.3, map[string]string{"app": "shop"}, Selector{Type: "random"}); err != nil {
		t.Fatalf("create service: %v", err)
	}
	if _, err := s.RegisterInstance(Instance{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		ServiceName: "orders",
		ClusterName: "DEFAULT",
		IP:          "10.0.0.1",
		Port:        8080,
		Weight:      1.0,
		Healthy:     true,
		Enabled:     true,
		Ephemeral:   true,
	}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	if err := s.AddSubscriber(Subscriber{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		ServiceName: "orders",
		ClusterName: "DEFAULT",
		Addr:        "10.0.0.2:8081",
		Agent:       "test",
		App:         "consumer",
	}); err != nil {
		t.Fatalf("add subscriber: %v", err)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "naming" {
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
	defer restored.Stop()
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}

	info, err := restored.GetService("public", "DEFAULT_GROUP", "orders")
	if err != nil {
		t.Fatalf("get service after restore: %v", err)
	}
	if info.Name != "orders" {
		t.Fatalf("name = %v", info.Name)
	}
	if info.ProtectThreshold != 0.3 {
		t.Fatalf("protectThreshold = %v", info.ProtectThreshold)
	}
	instances, err := restored.ListInstances("public", "DEFAULT_GROUP", "orders", "", false)
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}
	if instances[0].IP != "10.0.0.1" || instances[0].Port != 8080 {
		t.Fatalf("instance = %+v", instances[0])
	}
	subs, err := restored.ListSubscribers("public", "DEFAULT_GROUP", "orders", 1, 10)
	if err != nil {
		t.Fatalf("list subscribers: %v", err)
	}
	if subs.Count != 1 {
		t.Fatalf("subscribers = %d, want 1", subs.Count)
	}
}

func TestNamingSnapshotEmptyService(t *testing.T) {
	t.Parallel()
	s := NewService()
	defer s.Stop()
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := json.Marshal(snap)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	restored := NewService()
	defer restored.Stop()
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
}
