package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// snapshotKey is the Redis key under which the full backup envelope is stored.
// Storing the envelope as a single JSON blob is simpler than per-service keys
// and reuses the existing Coordinator serialization.
const snapshotKey = "gonacos:snapshot"

// RedisPersistence saves and loads the backup envelope to/from Redis. When
// dumpPath is set (standalone mode with embedded Redis), the envelope is also
// mirrored to a disk file so state survives process restarts even though
// miniredis itself is in-memory.
type RedisPersistence struct {
	client   *redis.Client
	coord    *Coordinator
	dumpPath string
}

// NewRedisPersistence constructs a persistence layer. dumpPath may be empty
// to disable disk mirroring (cluster mode with external Redis that has its
// own persistence).
func NewRedisPersistence(client *redis.Client, coord *Coordinator, dumpPath string) *RedisPersistence {
	return &RedisPersistence{
		client:   client,
		coord:    coord,
		dumpPath: dumpPath,
	}
}

// Save snapshots all registered services and writes the envelope to the Redis
// key. When dumpPath is set, the envelope is also written to disk so the
// embedded Redis can be repopulated on next startup.
func (p *RedisPersistence) Save(ctx context.Context) error {
	env, err := p.coord.Snapshot()
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := p.client.Set(ctx, snapshotKey, data, 0).Err(); err != nil {
		return fmt.Errorf("redis set snapshot: %w", err)
	}
	if p.dumpPath != "" {
		if err := writeDumpFile(p.dumpPath, data); err != nil {
			return fmt.Errorf("write dump: %w", err)
		}
	}
	return nil
}

// Load reads the envelope from the Redis key and restores all registered
// services. If the Redis key is missing and dumpPath is set, the envelope is
// loaded from disk and pushed into Redis so subsequent reads find it. A
// missing snapshot (fresh start) is not an error.
func (p *RedisPersistence) Load(ctx context.Context) error {
	data, err := p.client.Get(ctx, snapshotKey).Bytes()
	if err == redis.Nil {
		if p.dumpPath == "" {
			return nil // fresh start, nothing to restore
		}
		data, err = os.ReadFile(p.dumpPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // fresh start, no dump file
			}
			return fmt.Errorf("read dump: %w", err)
		}
		// Repopulate Redis so future loads hit the cache.
		if setErr := p.client.Set(ctx, snapshotKey, data, 0).Err(); setErr != nil {
			return fmt.Errorf("redis set snapshot from dump: %w", setErr)
		}
	} else if err != nil {
		return fmt.Errorf("redis get snapshot: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if err := p.coord.Restore(&env); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// StartPeriodic launches a goroutine that calls Save on the given interval
// until the returned stop function is called. The goroutine exits cleanly
// when ctx is canceled. Stop is idempotent and safe to call multiple times.
func (p *RedisPersistence) StartPeriodic(ctx context.Context, interval time.Duration) (stop func()) {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				_ = p.Save(context.Background())
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
	}
}

// writeDumpFile writes data to path, creating parent directories as needed.
func writeDumpFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
