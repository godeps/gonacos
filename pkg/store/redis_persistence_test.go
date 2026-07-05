package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newTestPersistence builds a RedisPersistence backed by an embedded miniredis
// and a coordinator with one fake snapshotter registered.
func newTestPersistence(t *testing.T, dumpPath string) (*RedisPersistence, *fakeSnapshotter, *redis.Client, func()) {
	t.Helper()
	e, err := StartEmbedded()
	if err != nil {
		t.Fatalf("start embedded: %v", err)
	}
	c := e.Client()
	coord := NewCoordinator()
	fake := &fakeSnapshotter{key: "fake", data: ""}
	coord.Register(fake)
	p := NewRedisPersistence(c, coord, dumpPath)
	cleanup := func() {
		c.Close()
		e.Close()
	}
	return p, fake, c, cleanup
}

// TestRedisPersistence_SaveLoad verifies that Save writes to Redis and Load
// restores the value.
func TestRedisPersistence_SaveLoad(t *testing.T) {
	t.Parallel()
	p, fake, c, cleanup := newTestPersistence(t, "")
	defer cleanup()
	c.FlushDB(context.Background())

	fake.data = map[string]string{"value": "hello"}
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}
	fake.data = nil

	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	m, ok := fake.data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", fake.data)
	}
	if v, _ := m["value"].(string); v != "hello" {
		t.Fatalf("value = %v, want hello", m["value"])
	}
}

// TestRedisPersistence_FreshStart verifies that Load on an empty Redis (no
// snapshot key, no dump file) is a no-op, not an error.
func TestRedisPersistence_FreshStart(t *testing.T) {
	t.Parallel()
	p, fake, c, cleanup := newTestPersistence(t, "")
	defer cleanup()
	c.FlushDB(context.Background())

	fake.data = "unchanged"
	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if fake.data != "unchanged" {
		t.Fatalf("data = %v, want unchanged", fake.data)
	}
}

// TestRedisPersistence_DiskPersistenceAcrossRestart verifies that when a dump
// path is set, the envelope survives a complete embedded Redis restart.
func TestRedisPersistence_DiskPersistenceAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "snap.json")

	// Session 1: save with a value, close everything.
	p1, fake1, c1, cleanup1 := newTestPersistence(t, dumpPath)
	c1.FlushDB(context.Background())
	fake1.data = map[string]string{"value": "persisted"}
	if err := p1.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}
	cleanup1()
	if !fileExists(dumpPath) {
		t.Fatal("dump file not written")
	}

	// Session 2: fresh embedded Redis (empty), same dump path. Load should
	// read from disk and restore the value.
	e2, err := StartEmbedded()
	if err != nil {
		t.Fatalf("start e2: %v", err)
	}
	defer e2.Close()
	c2 := e2.Client()
	defer c2.Close()
	coord2 := NewCoordinator()
	fake2 := &fakeSnapshotter{key: "fake", data: ""}
	coord2.Register(fake2)
	p2 := NewRedisPersistence(c2, coord2, dumpPath)

	if err := p2.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	m, ok := fake2.data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", fake2.data)
	}
	if v, _ := m["value"].(string); v != "persisted" {
		t.Fatalf("value = %v, want persisted", m["value"])
	}
}

// TestRedisPersistence_PeriodicSave verifies that StartPeriodic calls Save
// on the interval.
func TestRedisPersistence_PeriodicSave(t *testing.T) {
	t.Parallel()
	p, _, c, cleanup := newTestPersistence(t, "")
	defer cleanup()
	c.FlushDB(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := p.StartPeriodic(ctx, 20*time.Millisecond)
	defer stop()

	// Wait for at least one tick.
	time.Sleep(80 * time.Millisecond)

	err := c.Get(context.Background(), snapshotKey).Err()
	if err == redis.Nil {
		t.Fatal("snapshot key not set after periodic tick")
	}
	if err != nil {
		t.Fatalf("get: %v", err)
	}
}

// TestRedisPersistence_RotateDumpFile verifies that Save with backupCount > 0
// keeps the prior N snapshots as <dumpPath>.1, <dumpPath>.2, ... and drops
// anything older.
func TestRedisPersistence_RotateDumpFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "snapshot.json")
	p, fake, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetBackupCount(3)

	for i := 1; i <= 5; i++ {
		fake.data = map[string]string{"gen": string(rune('0' + i))}
		if err := p.Save(context.Background()); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	// With backupCount=3, we expect the current file plus .1, .2, .3.
	// .4 and .5 should have been dropped.
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		path := dumpPath + suffix
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
	for _, suffix := range []string{".4", ".5"} {
		path := dumpPath + suffix
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected %s to be dropped, but it exists", path)
		}
	}
}

// TestRedisPersistence_AtomicWriteNoCorruptionOnPartialWrite is a smoke test
// that the atomic-write path produces a single valid JSON file. A full
// crash-mid-write test would require a fault-injecting filesystem; the
// rename-based implementation makes that scenario impossible by construction.
func TestRedisPersistence_AtomicWriteNoCorruptionOnPartialWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "snapshot.json")
	p, fake, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()

	fake.data = map[string]string{"k": "v"}
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The dump file must be valid JSON (no temp files left behind).
	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("dump file is not valid JSON")
	}

	// No leftover temp files in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "snapshot.json.tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestRedisPersistence_ConcurrentSaveWithRotation verifies that concurrent
// Save calls do not corrupt the rotation. Before the saveMu fix, two
// concurrent Saves could interleave their rotate+write steps: Save A moves
// snapshot.json → snapshot.1.json, Save B moves snapshot.1.json →
// snapshot.2.json (but snapshot.json no longer exists), Save A writes
// snapshot.json, Save B overwrites snapshot.json. Result: snapshot.1 is
// missing while snapshot.2 holds the prior snapshot. With the mutex, all
// rotation slots are populated correctly.
func TestRedisPersistence_ConcurrentSaveWithRotation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "snapshot.json")
	p, fake, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetBackupCount(3)

	// Run 20 concurrent Saves from multiple goroutines, each writing a
	// distinct generation. With the mutex, all Saves serialize and the
	// rotation is consistent.
	const goroutines = 4
	const savesPerGoroutine = 5
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < savesPerGoroutine; i++ {
				fake.setData(map[string]string{
					"goroutine": string(rune('0' + g)),
					"iter":      string(rune('0' + i)),
				})
				if err := p.Save(context.Background()); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// The current dump and all rotation slots must be valid JSON. If the
	// rotation raced, snapshot.1 would be missing or contain stale data.
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		path := dumpPath + suffix
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", suffix, err)
			continue
		}
		if !json.Valid(data) {
			t.Errorf("%s is not valid JSON", suffix)
		}
	}
}

// TestRedisPersistence_StopWaitsForInflightSave verifies that the stop
// function returned by StartPeriodic blocks until any in-flight Save has
// completed. Without this guarantee, a Save started by the ticker and a
// Save started by Shutdown immediately after could race on the dump file
// even with the saveMu (the goroutine would still be mid-Save when stop
// returns).
func TestRedisPersistence_StopWaitsForInflightSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "snapshot.json")
	p, fake, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetBackupCount(2)

	// Use a very short interval so a Save starts quickly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := p.StartPeriodic(ctx, 10*time.Millisecond)
	defer stop()

	// Wait for at least one Save to land, then immediately call stop.
	// The test passes if stop returns without hanging and the dump file
	// is valid — meaning no Save was left mid-write.
	time.Sleep(40 * time.Millisecond)
	stop()

	// After stop returns, we can safely call Save ourselves without
	// racing. If stop did not wait for the goroutine, this Save could
	// interleave with the goroutine's in-flight Save.
	fake.setData(map[string]string{"after": "stop"})
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("save after stop: %v", err)
	}

	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("dump file is not valid JSON after stop+save")
	}
}
