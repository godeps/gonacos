package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
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

	// metrics is optional — when nil, metrics calls are no-ops. Wired in
	// by [server.New] via SetMetricsRegistry. Tracked here rather than at
	// the call site so the periodic ticker can record outcomes without
	// the caller wiring each call.
	metrics *observability.Registry

	// saveMu serializes Save calls so the periodic ticker and a shutdown
	// Save cannot trip over each other's rotation/write. Without this,
	// stopPeriodic closes the done channel but does not wait for an
	// in-flight Save to finish — the goroutine only observes the channel
	// after Save returns. A Save started by the ticker and a Save started
	// by Shutdown could then race on rotateDumpFile, leaving snapshot.1
	// missing while snapshot.2 holds the prior snapshot.
	saveMu sync.Mutex
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

// SetMetricsRegistry wires a Prometheus metrics registry. When set, Save
// and Load record:
//
//   - gonacos_snapshot_saves_total{result="success|failure"} — counter
//   - gonacos_snapshot_loads_total{result="success|failure"} — counter
//   - gonacos_snapshot_save_duration_seconds — histogram (1ms-10s buckets)
//   - gonacos_last_snapshot_save_timestamp_seconds — gauge, unix seconds
//
// The gauge is the alerting signal: alert on `time() - last_save > 2*interval`
// to catch a stuck periodic loop. The result="failure" counter is the data-
// loss signal: a sustained failure rate means state won't survive restart.
//
// Nil registry is allowed — metrics calls become no-ops, preserving backward
// compatibility for embedders that don't wire observability.
func (p *RedisPersistence) SetMetricsRegistry(r *observability.Registry) {
	p.metrics = r
}

// recordSaveResult increments the save counter and observes the duration.
// Best-effort: a nil registry or a malformed result string is silently
// dropped — metrics must not break the actual save call.
func (p *RedisPersistence) recordSaveResult(result string, duration time.Duration) {
	if p.metrics == nil {
		return
	}
	p.metrics.Counter("gonacos_snapshot_saves_total", map[string]string{"result": result}).Inc()
	p.metrics.Histogram("gonacos_snapshot_save_duration_seconds", nil,
		[]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
	).Observe(duration.Milliseconds())
	if result == "success" {
		p.metrics.Gauge("gonacos_last_snapshot_save_timestamp_seconds", nil).Set(time.Now().Unix())
	}
}

// recordLoadResult increments the load counter. Duration is not tracked
// separately because load only happens once at startup — the histogram
// would have a single observation per process lifetime, which is not useful.
func (p *RedisPersistence) recordLoadResult(result string) {
	if p.metrics == nil {
		return
	}
	p.metrics.Counter("gonacos_snapshot_loads_total", map[string]string{"result": result}).Inc()
}

// Save snapshots all registered services and writes the envelope to the Redis
// key. When dumpPath is set, the envelope is also written to disk so the
// embedded Redis can be repopulated on next startup. Concurrent Save calls
// are serialized by saveMu so the periodic ticker and a shutdown Save cannot
// interleave their rotation/write steps.
func (p *RedisPersistence) Save(ctx context.Context) error {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	start := time.Now()
	env, err := p.coord.Snapshot()
	if err != nil {
		p.recordSaveResult("failure", time.Since(start))
		return fmt.Errorf("snapshot: %w", err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		p.recordSaveResult("failure", time.Since(start))
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := p.client.Set(ctx, snapshotKey, data, 0).Err(); err != nil {
		p.recordSaveResult("failure", time.Since(start))
		return fmt.Errorf("redis set snapshot: %w", err)
	}
	if p.dumpPath != "" {
		if p.backupCount > 0 {
			rotateDumpFile(p.dumpPath, p.backupCount)
		}
		if err := writeDumpFile(p.dumpPath, data); err != nil {
			p.recordSaveResult("failure", time.Since(start))
			return fmt.Errorf("write dump: %w", err)
		}
	}
	p.recordSaveResult("success", time.Since(start))
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
			// Fresh start, nothing to restore. Not a failure.
			p.recordLoadResult("success")
			return nil
		}
		data, err = os.ReadFile(p.dumpPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Fresh start, no dump file. Not a failure.
				p.recordLoadResult("success")
				return nil
			}
			p.recordLoadResult("failure")
			return fmt.Errorf("read dump: %w", err)
		}
		// Repopulate Redis so future loads hit the cache.
		if setErr := p.client.Set(ctx, snapshotKey, data, 0).Err(); setErr != nil {
			p.recordLoadResult("failure")
			return fmt.Errorf("redis set snapshot from dump: %w", setErr)
		}
	} else if err != nil {
		p.recordLoadResult("failure")
		return fmt.Errorf("redis get snapshot: %w", err)
	}
	if len(data) == 0 {
		p.recordLoadResult("success")
		return nil
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		p.recordLoadResult("failure")
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if err := p.coord.Restore(&env); err != nil {
		p.recordLoadResult("failure")
		return fmt.Errorf("restore: %w", err)
	}
	p.recordLoadResult("success")
	return nil
}

// StartPeriodic launches a goroutine that calls Save on the given interval
// until the returned stop function is called. The goroutine exits cleanly
// when ctx is canceled. Stop is idempotent and safe to call multiple times.
// Stop blocks until any in-flight Save has completed, so callers can safely
// call Save immediately after stop returns without racing on the dump file.
//
// Save errors from the periodic loop are logged via the standard log package
// (not the server logger — pkg/store is below the logger layer in the
// dependency graph). The metrics registry, if wired, records each failure
// under gonacos_snapshot_saves_total{result="failure"}. Operators alert on
// a non-zero failure rate.
func (p *RedisPersistence) StartPeriodic(ctx context.Context, interval time.Duration) (stop func()) {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(finished)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := p.Save(context.Background()); err != nil {
					log.Printf("snapshot: periodic save failed: %v", err)
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
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
