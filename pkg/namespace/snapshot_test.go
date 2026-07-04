package namespace

import (
	"encoding/json"
	"testing"
)

func TestNamespaceSnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.Create("dev", "Development", "dev namespace"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create("staging", "Staging", "staging namespace"); err != nil {
		t.Fatalf("create staging: %v", err)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "namespace" {
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
	list := restored.List()
	ids := map[string]bool{}
	for _, ns := range list {
		ids[ns.Namespace] = true
	}
	for _, want := range []string{"public", "dev", "staging"} {
		if !ids[want] {
			t.Fatalf("missing namespace %q after restore; have %v", want, ids)
		}
	}
}

func TestNamespaceRestoreReSeedsPublicIfMissing(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.Restore([]any{
		map[string]any{"namespace": "custom", "namespaceShowName": "Custom"},
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !s.Exists("public") {
		t.Fatal("public namespace should be re-seeded after restore")
	}
	if !s.Exists("custom") {
		t.Fatal("custom namespace should exist after restore")
	}
}

func TestNamespaceRestoreRejectsBadShape(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.Restore("not a list"); err == nil {
		t.Fatal("expected error for non-list shape")
	}
}
