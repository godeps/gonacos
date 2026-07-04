package auth

import (
	"encoding/json"
	"testing"
)

func TestAuthSnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService()
	if _, err := s.BootstrapAdmin("initial-password"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := s.CreateUser("alice", "alice-password"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := s.CreateRole("devs", "alice"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := s.CreatePermission("devs", "config:*", "rw"); err != nil {
		t.Fatalf("create permission: %v", err)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "auth" {
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

	originalRaw, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot original: %v", err)
	}
	original, _ := originalRaw.(authSnapshot)
	afterRaw, err := restored.Snapshot()
	if err != nil {
		t.Fatalf("snapshot restored: %v", err)
	}
	after, _ := afterRaw.(authSnapshot)
	if len(original.Users) != len(after.Users) {
		t.Fatalf("users = %d, want %d", len(after.Users), len(original.Users))
	}
	if len(original.Permissions) != len(after.Permissions) {
		t.Fatalf("permissions = %d, want %d", len(after.Permissions), len(original.Permissions))
	}

	// Password hash should be preserved exactly.
	for i, u := range original.Users {
		if after.Users[i].Password != u.Password {
			t.Fatalf("password for %s not preserved", u.Username)
		}
		if after.Users[i].Salt != u.Salt {
			t.Fatalf("salt for %s not preserved", u.Username)
		}
	}

	// Restored admin should be able to login with the original password.
	if _, err := restored.Login("nacos", "initial-password"); err != nil {
		t.Fatalf("login after restore: %v", err)
	}
}

func TestAuthRestoreRejectsBadShape(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.Restore("not an object"); err == nil {
		t.Fatal("expected error for non-object shape")
	}
}
