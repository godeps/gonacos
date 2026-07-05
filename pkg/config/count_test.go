package config

import (
	"testing"
)

// TestCountByNamespace verifies that CountByNamespace returns the number of
// distinct (group, dataID) tuples in the namespace. Beta/gray variants do
// not double-count: a beta publish for the same (group, dataID) still
// counts as one config.
func TestCountByNamespace(t *testing.T) {
	t.Parallel()
	s := NewService()

	if got := s.CountByNamespace("public"); got != 0 {
		t.Fatalf("empty namespace: got %d, want 0", got)
	}

	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "a.yml",
		Content:     "a: 1",
		Type:        "yaml",
	}); err != nil {
		t.Fatalf("publish a: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "b.yml",
		Content:     "b: 2",
		Type:        "yaml",
	}); err != nil {
		t.Fatalf("publish b: %v", err)
	}
	if got := s.CountByNamespace("public"); got != 2 {
		t.Fatalf("after 2 publishes: got %d, want 2", got)
	}

	// Beta publish for the same (group, dataID) does not add a new entry
	// to the items map — beta variants live in betaItems. ConfigCount
	// reflects distinct regular configs, so the count stays at 2.
	if err := s.PublishBeta(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "a.yml",
		Content:     "a: beta",
		Type:        "yaml",
		BetaIPs:     "10.0.0.1",
	}); err != nil {
		t.Fatalf("publish beta: %v", err)
	}
	if got := s.CountByNamespace("public"); got != 2 {
		t.Fatalf("after beta publish: got %d, want 2 (beta does not add a new item)", got)
	}

	// Publishing an existing key updates in place — count stays the same.
	if err := s.Publish(PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "a.yml",
		Content:     "a: updated",
		Type:        "yaml",
	}); err != nil {
		t.Fatalf("publish update: %v", err)
	}
	if got := s.CountByNamespace("public"); got != 2 {
		t.Fatalf("after update: got %d, want 2", got)
	}

	// Different namespace — independent count.
	if err := s.Publish(PublishRequest{
		NamespaceID: "ns-other",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "x.yml",
		Content:     "x: 1",
		Type:        "yaml",
	}); err != nil {
		t.Fatalf("publish x: %v", err)
	}
	if got := s.CountByNamespace("ns-other"); got != 1 {
		t.Fatalf("ns-other: got %d, want 1", got)
	}
	if got := s.CountByNamespace("public"); got != 2 {
		t.Fatalf("public after ns-other publish: got %d, want 2", got)
	}
	if got := s.CountByNamespace("nonexistent"); got != 0 {
		t.Fatalf("nonexistent namespace: got %d, want 0", got)
	}
}

// TestCountAllByNamespace verifies the batch form returns a count per
// namespace in a single pass, with zero-config namespaces omitted from
// the map.
func TestCountAllByNamespace(t *testing.T) {
	t.Parallel()
	s := NewService()

	if err := s.Publish(PublishRequest{
		NamespaceID: "public", GroupName: "g1", DataID: "a",
		Content: "a", Type: "text",
	}); err != nil {
		t.Fatalf("publish a: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "public", GroupName: "g1", DataID: "b",
		Content: "b", Type: "text",
	}); err != nil {
		t.Fatalf("publish b: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "public", GroupName: "g2", DataID: "c",
		Content: "c", Type: "text",
	}); err != nil {
		t.Fatalf("publish c: %v", err)
	}
	if err := s.Publish(PublishRequest{
		NamespaceID: "ns2", GroupName: "g1", DataID: "x",
		Content: "x", Type: "text",
	}); err != nil {
		t.Fatalf("publish x: %v", err)
	}

	counts := s.CountAllByNamespace()
	if got, want := len(counts), 2; got != want {
		t.Fatalf("len(counts) = %d, want %d", got, want)
	}
	if got := counts["public"]; got != 3 {
		t.Errorf("counts[public] = %d, want 3", got)
	}
	if got := counts["ns2"]; got != 1 {
		t.Errorf("counts[ns2] = %d, want 1", got)
	}
	// Namespaces with zero configs are absent (callers treat missing as 0).
	if _, ok := counts["empty-ns"]; ok {
		t.Errorf("counts[empty-ns] should be absent, got %v", counts["empty-ns"])
	}
}

// TestCountByNamespaceEmptyService verifies that a fresh service returns 0
// for any namespace — no panic, no spurious counts.
func TestCountByNamespaceEmptyService(t *testing.T) {
	t.Parallel()
	s := NewService()
	if got := s.CountByNamespace("public"); got != 0 {
		t.Fatalf("empty service: got %d, want 0", got)
	}
	if got := s.CountAllByNamespace(); len(got) != 0 {
		t.Fatalf("empty service CountAllByNamespace: got %v, want empty", got)
	}
}
