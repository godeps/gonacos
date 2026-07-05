package server

import (
	"net"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/redis/go-redis/v9"
)

// poolStatsProvider abstracts the subset of *redis.Client used by the
// resource collector to sample connection-pool stats. Defined as an
// interface so the collector can be tested with a stub instead of a
// real Redis client — and so pkg/server doesn't grow new compile-time
// coupling to redis beyond what [New] already has.
type poolStatsProvider interface {
	PoolStats() *redis.PoolStats
}

// startResourceCollector registers resource-count gauges and launches a
// background goroutine that refreshes them on the given interval. Returns a
// stop function safe to call multiple times.
//
// Gauges exposed:
//
//	gonacos_namespaces_total          — number of namespaces
//	gonacos_configs_total             — total config items across all namespaces
//	gonacos_services_total            — total registered services across all namespaces
//	gonacos_users_total               — number of registered users
//	gonacos_instances_total           — total service instances across all services
//	gonacos_grpc_connections          — active long-lived gRPC push connections
//	gonacos_active_connections{proto} — active TCP connections per protocol
//	                                    (proto="http" or "grpc"); only populated
//	                                    when [WithMaxConns] is set, since the
//	                                    connection counter lives on the cap
//	                                    wrapper. Without the cap, the gauge
//	                                    stays at zero — operators who want
//	                                    the metric should set a high cap
//	                                    (e.g., 100000) to track without
//	                                    effectively limiting.
//	gonacos_connection_rejections_total{proto} — cumulative connections
//	                                    refused because the cap was hit.
//	                                    A sustained non-zero rate signals a
//	                                    connection-flood attack or capacity
//	                                    shortfall; pair with
//	                                    gonacos_active_connections to tell
//	                                    "saturated but not rejecting" from
//	                                    "actively refusing new conns".
//	gonacos_redis_pool_connections{state} — Redis connection pool state
//	                                    (state="total|idle|stale"); sampled
//	                                    from redis.Client.PoolStats so the
//	                                    values reflect the live pool, not a
//	                                    snapshot at startup.
//	gonacos_redis_pool_hits_total     — counter, pool hits since process start
//	gonacos_redis_pool_misses_total   — counter, pool misses (pool empty)
//	gonacos_redis_pool_timeouts_total — counter, pool wait timeouts (pool
//	                                    saturated and PoolTimeout exceeded)
//
// The collector is a no-op when registry or bundle is nil. Sampling is O(n)
// on the in-memory store (bounded by the namespace quota), so at a 30s
// default interval the overhead is negligible against the request hot path.
func startResourceCollector(registry *observability.Registry, bundle *app.ServiceBundle, push *app.PushService, httpLn, grpcLn net.Listener, redisClient poolStatsProvider, interval time.Duration) func() {
	if registry == nil || bundle == nil {
		return func() {}
	}
	gauges := struct {
		namespaces  *observability.Gauge
		configs     *observability.Gauge
		services    *observability.Gauge
		users       *observability.Gauge
		instances   *observability.Gauge
		connections *observability.Gauge
		httpConns   *observability.Gauge
		grpcConns   *observability.Gauge
		httpReject  *observability.Gauge
		grpcReject  *observability.Gauge
		poolTotal   *observability.Gauge
		poolIdle    *observability.Gauge
		poolStale   *observability.Gauge
	}{
		namespaces:  registry.Gauge("gonacos_namespaces_total", nil),
		configs:     registry.Gauge("gonacos_configs_total", nil),
		services:    registry.Gauge("gonacos_services_total", nil),
		users:       registry.Gauge("gonacos_users_total", nil),
		instances:   registry.Gauge("gonacos_instances_total", nil),
		connections: registry.Gauge("gonacos_grpc_connections", nil),
		httpConns:   registry.Gauge("gonacos_active_connections", map[string]string{"proto": "http"}),
		grpcConns:   registry.Gauge("gonacos_active_connections", map[string]string{"proto": "grpc"}),
		httpReject:  registry.Gauge("gonacos_connection_rejections_total", map[string]string{"proto": "http"}),
		grpcReject:  registry.Gauge("gonacos_connection_rejections_total", map[string]string{"proto": "grpc"}),
		poolTotal:   registry.Gauge("gonacos_redis_pool_connections", map[string]string{"state": "total"}),
		poolIdle:    registry.Gauge("gonacos_redis_pool_connections", map[string]string{"state": "idle"}),
		poolStale:   registry.Gauge("gonacos_redis_pool_connections", map[string]string{"state": "stale"}),
	}
	// Pool hit/miss/timeout counters are cumulative since process start
	// (PoolStats returns uint32 totals, not deltas). Modeled as gauges
	// with the _total suffix so Prometheus scrapers see the cumulative
	// value as-is — Counter.Set would require extending the public API
	// or tracking per-period deltas, and the absolute value is what
	// PoolStats already gives us. The _total convention in the name is
	// a hint to scrapers that the value is monotonic.
	poolHits := registry.Gauge("gonacos_redis_pool_hits_total", nil)
	poolMisses := registry.Gauge("gonacos_redis_pool_misses_total", nil)
	poolTimeouts := registry.Gauge("gonacos_redis_pool_timeouts_total", nil)

	refresh := func() {
		namespaces := bundle.Namespace.List()
		gauges.namespaces.Set(int64(len(namespaces)))

		// Configs: sum TotalCount across all namespaces. Each namespace's
		// List is O(configs-in-namespace); the namespace quota bounds this.
		var configs int64
		for _, ns := range namespaces {
			page, err := bundle.Config.List(ns.Namespace, "", "", "", 1, 1)
			if err != nil {
				continue
			}
			configs += int64(page.TotalCount)
		}
		gauges.configs.Set(configs)

		// Services and instances: O(services) on the naming store, no
		// per-namespace sweep needed.
		gauges.services.Set(int64(bundle.Naming.CountAllServices()))
		gauges.instances.Set(int64(bundle.Naming.CountAllInstances()))

		// Users: TotalCount from a size-1 page query.
		up, err := bundle.Auth.ListUsers(1, 1, "", "")
		if err == nil {
			gauges.users.Set(int64(up.TotalCount))
		}

		// Active gRPC push connections (long-lived BiRequestStream streams
		// registered with the push service's connection registry). Nil when
		// push is disabled — leave the gauge at 0 in that case.
		if push != nil {
			if reg := push.ConnectionRegistry(); reg != nil {
				gauges.connections.Set(int64(reg.Count()))
			}
		}

		// Active TCP connections per protocol. Only meaningful when
		// [WithMaxConns] is set — the count lives on the maxConnsListener
		// wrapper. Without the cap, the listeners are raw net.Listeners
		// and the gauges stay at 0 (their initial value).
		//
		// The rejection counter is sampled here too: it is a cumulative
		// int32 on the listener, so we Set (not Inc) the gauge to its
		// absolute value. This mirrors the redis pool hits/misses
		// pattern — Prometheus sees a monotonic counter that scrapers
		// can rate via increase()/rate().
		if ml, ok := httpLn.(*maxConnsListener); ok {
			gauges.httpConns.Set(int64(ml.CurrentConns()))
			gauges.httpReject.Set(int64(ml.RejectedConns()))
		}
		if ml, ok := grpcLn.(*maxConnsListener); ok {
			gauges.grpcConns.Set(int64(ml.CurrentConns()))
			gauges.grpcReject.Set(int64(ml.RejectedConns()))
		}

		// Redis connection pool state. PoolStats returns cumulative
		// counters since process start, so .Set on the counter is the
		// right call (not .Inc — the value is absolute, not a delta).
		// Nil redisClient (tests, embedders that don't wire Redis) skips
		// the sample; the gauges stay at 0.
		if redisClient != nil {
			stats := redisClient.PoolStats()
			if stats != nil {
				gauges.poolTotal.Set(int64(stats.TotalConns))
				gauges.poolIdle.Set(int64(stats.IdleConns))
				gauges.poolStale.Set(int64(stats.StaleConns))
				poolHits.Set(int64(stats.Hits))
				poolMisses.Set(int64(stats.Misses))
				poolTimeouts.Set(int64(stats.Timeouts))
			}
		}
	}

	refresh()
	if interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				refresh()
			case <-stop:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
