package config

import (
	"strings"
	"testing"
)

// TestPublishRejectsContentExceedingMaxSize verifies that Publish returns
// ErrConfigTooLarge when content exceeds the per-group MaxSize.
func TestPublishRejectsContentExceedingMaxSize(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.UpdateCapacity("public", "G", 1000, 10, 0, 0); err != nil {
		t.Fatalf("update capacity: %v", err)
	}
	err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "big.txt",
		Content:     "this content is too long",
		Type:        "text",
	})
	if err != ErrConfigTooLarge {
		t.Fatalf("err = %v, want ErrConfigTooLarge", err)
	}
}

// TestPublishRejectsQuotaExceeded verifies that Publish returns
// ErrQuotaExceeded when the per-group config count reaches Quota.
func TestPublishRejectsQuotaExceeded(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.UpdateCapacity("public", "G", 2, 0, 0, 0); err != nil {
		t.Fatalf("update capacity: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Publish(PublishRequest{
			NamespaceID: "public",
			GroupName:   "G",
			DataID:      "c" + string(rune('1'+i)),
			Content:     "x",
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "c3",
		Content:     "x",
	})
	if err != ErrQuotaExceeded {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
}

// TestPublishAllowsUpdateOnExistingConfig verifies that updating an existing
// config does not trigger the Quota check (only new inserts count).
func TestPublishAllowsUpdateOnExistingConfig(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.UpdateCapacity("public", "G", 1, 0, 0, 0); err != nil {
		t.Fatalf("update capacity: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "c1",
		Content:     "v1",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Update the same config — should not exceed quota.
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "c1",
		Content:     "v2",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// TestPublishDefaultMaxSize verifies the default MaxSize (100 KiB) is
// enforced when no explicit capacity is set.
func TestPublishDefaultMaxSize(t *testing.T) {
	t.Parallel()
	s := NewService()
	big := strings.Repeat("x", 100*1024+1)
	err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "big.txt",
		Content:     big,
	})
	if err != ErrConfigTooLarge {
		t.Fatalf("err = %v, want ErrConfigTooLarge", err)
	}
}

// TestPublishGrayEnforcesMaxSize verifies PublishGray also checks content
// size against MaxSize.
func TestPublishGrayEnforcesMaxSize(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.UpdateCapacity("public", "G", 1000, 10, 0, 0); err != nil {
		t.Fatalf("update capacity: %v", err)
	}
	err := s.PublishGray(GrayRequest{
		PublishRequest: PublishRequest{
			NamespaceID: "public",
			GroupName:   "G",
			DataID:      "c1",
			Content:     "this is too long",
		},
		GrayName: "beta",
	})
	if err != ErrConfigTooLarge {
		t.Fatalf("err = %v, want ErrConfigTooLarge", err)
	}
}

// TestApplyRemotePublishEnforcesCapacity verifies remote-synced publishes
// also respect capacity limits.
func TestApplyRemotePublishEnforcesCapacity(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.UpdateCapacity("public", "G", 1000, 10, 0, 0); err != nil {
		t.Fatalf("update capacity: %v", err)
	}
	err := s.ApplyRemotePublish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "remote.txt",
		Content:     "this content is too long",
	})
	if err != ErrConfigTooLarge {
		t.Fatalf("err = %v, want ErrConfigTooLarge", err)
	}
}
