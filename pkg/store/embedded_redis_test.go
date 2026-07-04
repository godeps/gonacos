package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestEmbeddedRedis_StartAndClient verifies that the embedded server starts
// and a redis.Client can interact with it.
func TestEmbeddedRedis_StartAndClient(t *testing.T) {
	t.Parallel()
	e, err := StartEmbedded()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Close()
	if e.Addr() == "" {
		t.Fatal("addr is empty")
	}
	c := e.Client()
	defer c.Close()
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.Set(context.Background(), "k", "v", 0).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := c.Get(context.Background(), "k").Result()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "v" {
		t.Fatalf("got %q, want v", got)
	}
}

// TestEmbeddedRedis_IndependentSessions verifies that two embedded servers
// have independent state (miniredis is in-memory; no built-in persistence).
func TestEmbeddedRedis_IndependentSessions(t *testing.T) {
	t.Parallel()
	e1, err := StartEmbedded()
	if err != nil {
		t.Fatalf("start e1: %v", err)
	}
	defer e1.Close()
	c1 := e1.Client()
	defer c1.Close()
	if err := c1.Set(context.Background(), "k", "v1", 0).Err(); err != nil {
		t.Fatalf("set e1: %v", err)
	}

	e2, err := StartEmbedded()
	if err != nil {
		t.Fatalf("start e2: %v", err)
	}
	defer e2.Close()
	c2 := e2.Client()
	defer c2.Close()
	_, err = c2.Get(context.Background(), "k").Result()
	if err != redis.Nil {
		t.Fatalf("expected redis.Nil on second server, got %v", err)
	}
}

// TestFileExists verifies the fileExists helper.
func TestFileExists(t *testing.T) {
	t.Parallel()
	if fileExists("") {
		t.Fatal("empty path should not exist")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if fileExists(p) {
		t.Fatal("nonexistent file should not exist")
	}
}
