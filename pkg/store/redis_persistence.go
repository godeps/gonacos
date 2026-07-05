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
//
// Disk writes are atomic (temp file + rename) so a crash mid-write cannot
// corrupt the dump file. When backupCount > 0, the previous N snapshots are
// retained as snapshot.1.json, snapshot.2.json, ... so a corrupted or
// accidentally-erased latest snapshot can be recovered from the prior one.
type RedisPersistence struct {
	client      *redis.Client
	coord       *Coordinator
	dumpPath    string
	backupCount int
}

// NewRedisPersistence constructs a persistence layer. dumpPath may be empty
// to disable disk mirroring (cluster mode with external Redis that has its
// own persistence). backupCount defaults to 0 (no rotation); use
// [RedisPersistence.SetBackupCount] to enable.
func NewRedisPersistence(client *redis.Client, coord *Coordinator, dumpPath string) *RedisPersistence {
	return &RedisPersistence{
		client:   client,
		coord:    coord,
		dumpPath: dumpPath,
	}
}

// SetBackupCount configures how many prior snapshots to retain on disk.
// When n > 0, each Save shifts the existing dump file to <dumpPath>.1,
// <dumpPath>.2, ..., <dumpPath>.n (dropping the oldest) before writing the
// new snapshot. When n <= 0 (default), no rotation occurs and the dump file
// is overwritten in place.
func (p *RedisPersistence) SetBackupCount(n int) {
	if n < 0 {
		n = 0
	}
	p.backupCount = n
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
		if p.backupCount > 0 {
			rotateDumpFile(p.dumpPath, p.backupCount)
		}
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

// writeDumpFile writes data to path atomically: it writes to a sibling temp
// file then renames it into place. On POSIX systems rename is atomic, so a
// crash mid-write cannot leave a half-written dump file that would fail to
// load on next startup.
func writeDumpFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// rotateDumpFile shifts the existing dump file at path to path.1, path.1 to
// path.2, ..., dropping the oldest (path.N) when count is exceeded. The base
// path itself is renamed (not copied) so the rotation is atomic and there is
// no window where both path and path.1 contain the same data. Missing files
// are silently skipped.
func rotateDumpFile(path string, count int) {
	if count <= 0 {
		return
	}
	// Drop the oldest; rename from highest to lowest so we don't clobber.
	oldest := fmt.Sprintf("%s.%d", path, count)
	if _, err := os.Stat(oldest); err == nil {
		_ = os.Remove(oldest)
	}
	for i := count; i > 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i-1)
		dst := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	// Move the current dump to .1. If the current dump doesn't exist yet
	// (first save), there's nothing to rotate.
	if _, err := os.Stat(path); err == nil {
		_ = os.Rename(path, fmt.Sprintf("%s.1", path))
	}
}
