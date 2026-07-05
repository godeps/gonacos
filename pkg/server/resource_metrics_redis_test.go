package server

import (
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/redis/go-redis/v9"
)

// stubPoolStatsProvider is a poolStatsProvider stub for testing the
// Redis-pool metrics sampling without spinning up a real Redis. The
// returned *redis.PoolStats is heap-allocated so the test can mutate
// it between samples to verify the collector picks up new values.
type stubPoolStatsProvider struct {
	stats redis.PoolStats
}

func (s *stubPoolStatsProvider) PoolStats() *redis.PoolStats {
	return &s.stats
}

// TestResourceCollectorExposesRedisPoolMetrics verifies that the
// resource collector samples the Redis connection pool stats and
// exposes them as gauges under gonacos_redis_pool_*.
//
// Without these metrics, an operator can't tell from /metrics whether
// the pool is saturated (misses > hits), leaking (TotalConns grows),
// or churning (StaleConns > 0 indicates Redis restarts or network
// problems invalidating pooled connections).
func TestResourceCollectorExposesRedisPoolMetrics(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()
	stub := &stubPoolStatsProvider{}
	stub.stats = redis.PoolStats{
		Hits: 100, Misses: 5, Timeouts: 1,
		TotalConns: 10, IdleConns: 8, StaleConns: 2,
	}

	stop := startResourceCollector(registry, bundle, nil, nil, nil, stub, 0)
	defer stop()

	var buf strings.Builder
	registry.WritePrometheus(&buf)
	out := buf.String()

	for _, want := range []string{
		"gonacos_redis_pool_connections",
		"gonacos_redis_pool_hits_total",
		"gonacos_redis_pool_misses_total",
		"gonacos_redis_pool_timeouts_total",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metric %q missing from /metrics output: %s", want, out)
		}
	}

	// Verify the state= labels carry the right values.
	totalGauge := registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "total"}).Value()
	if totalGauge != 10 {
		t.Errorf("pool total = %d, want 10", totalGauge)
	}
	idleGauge := registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "idle"}).Value()
	if idleGauge != 8 {
		t.Errorf("pool idle = %d, want 8", idleGauge)
	}
	staleGauge := registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "stale"}).Value()
	if staleGauge != 2 {
		t.Errorf("pool stale = %d, want 2", staleGauge)
	}
	hitsGauge := registry.Gauge("gonacos_redis_pool_hits_total", nil).Value()
	if hitsGauge != 100 {
		t.Errorf("pool hits = %d, want 100", hitsGauge)
	}
	missesGauge := registry.Gauge("gonacos_redis_pool_misses_total", nil).Value()
	if missesGauge != 5 {
		t.Errorf("pool misses = %d, want 5", missesGauge)
	}
	timeoutsGauge := registry.Gauge("gonacos_redis_pool_timeouts_total", nil).Value()
	if timeoutsGauge != 1 {
		t.Errorf("pool timeouts = %d, want 1", timeoutsGauge)
	}
}

// TestResourceCollectorUpdatesRedisPoolMetricsOnRefresh verifies that
// a second refresh picks up the new PoolStats values — the collector
// must not snapshot at startup and stop sampling.
func TestResourceCollectorUpdatesRedisPoolMetricsOnRefresh(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()
	stub := &stubPoolStatsProvider{}
	stub.stats = redis.PoolStats{
		Hits: 100, TotalConns: 10,
	}

	stop := startResourceCollector(registry, bundle, nil, nil, nil, stub, 0)
	defer stop()

	totalGauge := registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "total"}).Value()
	if totalGauge != 10 {
		t.Fatalf("initial pool total = %d, want 10", totalGauge)
	}

	// Mutate the pool stats and refresh again.
	stub.stats = redis.PoolStats{
		Hits: 200, TotalConns: 20,
	}
	// Trigger another refresh by calling startResourceCollector's
	// refresh again — the public API only exposes the periodic ticker,
	// so we re-call the constructor with interval=0 to run one
	// immediate refresh on the new values.
	stop2 := startResourceCollector(registry, bundle, nil, nil, nil, stub, 0)
	defer stop2()

	totalGauge = registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "total"}).Value()
	if totalGauge != 20 {
		t.Errorf("updated pool total = %d, want 20 (refresh did not pick up new value)", totalGauge)
	}
	hitsGauge := registry.Gauge("gonacos_redis_pool_hits_total", nil).Value()
	if hitsGauge != 200 {
		t.Errorf("updated pool hits = %d, want 200", hitsGauge)
	}
}

// TestResourceCollectorNilRedisClientSkipsPoolMetrics verifies that
// when redisClient is nil (no PoolStats provider wired), the pool
// metrics are still registered (at 0) but never sampled — so the
// /metrics output lists them but the values stay at 0. This is the
// path for tests and embedders that don't wire Redis.
func TestResourceCollectorNilRedisClientSkipsPoolMetrics(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()

	stop := startResourceCollector(registry, bundle, nil, nil, nil, nil, 0)
	defer stop()

	totalGauge := registry.Gauge("gonacos_redis_pool_connections",
		map[string]string{"state": "total"}).Value()
	if totalGauge != 0 {
		t.Errorf("pool total with nil client = %d, want 0", totalGauge)
	}
	hitsGauge := registry.Gauge("gonacos_redis_pool_hits_total", nil).Value()
	if hitsGauge != 0 {
		t.Errorf("pool hits with nil client = %d, want 0", hitsGauge)
	}
}
