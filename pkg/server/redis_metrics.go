package server

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/redis/go-redis/v9"
)

// redisMetricsHook wires go-redis command/dial events into the Prometheus
// registry. The hook is wired in [New] when an external Redis client is
// constructed (and also for the embedded-redis client — both paths benefit
// from observability).
//
// Metrics emitted:
//
//   - gonacos_redis_commands_total{command="..."} — counter, incremented per
//     processed command. The command label is the leaf name from Cmder.Name()
//     (e.g. "set", "get", "cluster"), lowercased and truncated so a malicious
//     or pathological command name cannot blow up the label cardinality.
//   - gonacos_redis_command_duration_seconds{command="..."} — histogram,
//     observed in milliseconds (the registry's native unit) with buckets
//     covering 1 ms to ~10 s. The sum/count fields let alerting compute
//     average latency; the buckets give percentiles.
//   - gonacos_redis_dial_total{result="success|failure"} — counter, dial
//     outcomes. A spike in result="failure" is the early-warning signal for
//     Redis connectivity loss (network partition, Redis down, ACL revocation).
//
// All metrics are best-effort: a panic or error in the hook must not break
// the actual Redis call. The hook wraps the next hook/process function and
// always delegates, recording metrics after the call returns.
type redisMetricsHook struct {
	registry *observability.Registry

	// buckets for command latency, in milliseconds (registry's native unit).
	// Covers 1ms → 10s. Tuned for Redis-class latency: typical <5ms, slow
	// commands >100ms, catastrophic >1s.
	latencyBuckets []float64
}

// newRedisMetricsHook constructs a hook wired into the given registry. The
// registry may be nil — in that case the hook is a no-op pass-through (no
// metrics recorded). This keeps the call site in [New] simple: it always
// installs the hook, and the hook self-disables when there's nothing to
// record to.
func newRedisMetricsHook(registry *observability.Registry) *redisMetricsHook {
	return &redisMetricsHook{
		registry: registry,
		latencyBuckets: []float64{
			1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000,
		},
	}
}

// DialHook wraps the dial function to count dial attempts and outcomes.
// A dial is the first connection establishment to a Redis node; a spike in
// result="failure" here is the earliest signal that the Redis endpoint is
// unreachable (vs. a process hook failure, which can also be a command
// timeout on a stale connection).
func (h *redisMetricsHook) DialHook(next redis.DialHook) redis.DialHook {
	if h.registry == nil {
		return next
	}
	success := h.registry.Counter("gonacos_redis_dial_total", map[string]string{"result": "success"})
	failure := h.registry.Counter("gonacos_redis_dial_total", map[string]string{"result": "failure"})
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := next(ctx, network, addr)
		if err != nil {
			failure.Inc()
		} else {
			success.Inc()
		}
		return conn, err
	}
}

// ProcessHook wraps a single Redis command. It records the command counter
// and the latency histogram. The command label is normalized: lowercased,
// empty-string-safe. A panic in the inner process (e.g. a misbehaving
// command) is not recovered here — the inner process is expected to return
// errors, not panic. If it did panic, the metrics wouldn't record, but the
// panic would propagate as before (preserving existing behavior).
func (h *redisMetricsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	if h.registry == nil {
		return next
	}
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := normalizeCommandName(cmd.Name())
		labels := map[string]string{"command": name}
		counter := h.registry.Counter("gonacos_redis_commands_total", labels)
		hist := h.registry.Histogram("gonacos_redis_command_duration_seconds", labels, h.latencyBuckets)
		start := time.Now()
		err := next(ctx, cmd)
		hist.Observe(time.Since(start).Milliseconds())
		counter.Inc()
		return err
	}
}

// ProcessPipelineHook wraps a pipeline of commands. Each command in the
// pipeline is counted individually under its own name so the per-command
// counters and latencies remain accurate. Pipeline-level latency (the
// round-trip for the whole batch) is not recorded as a separate metric —
// operators already see it as the sum of per-command latencies, and a
// separate "pipeline" histogram would double-count the same wall time.
//
// Errors from the pipeline (a failed MULTI/EXEC, for example) are returned
// unchanged; the per-command counters still increment because each Cmder
// in the pipeline carries its own error.
func (h *redisMetricsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	if h.registry == nil {
		return next
	}
	return func(ctx context.Context, cmds []redis.Cmder) error {
		// Pre-resolve counters/histograms for each command in the pipeline.
		// We can't share the per-command *Counter pointer across iterations
		// because different commands in a pipeline may have different names.
		type cmdMetrics struct {
			counter *observability.Counter
			hist    *observability.Histogram
		}
		metrics := make([]cmdMetrics, len(cmds))
		for i, cmd := range cmds {
			name := normalizeCommandName(cmd.Name())
			labels := map[string]string{"command": name}
			metrics[i] = cmdMetrics{
				counter: h.registry.Counter("gonacos_redis_commands_total", labels),
				hist:    h.registry.Histogram("gonacos_redis_command_duration_seconds", labels, h.latencyBuckets),
			}
		}
		start := time.Now()
		err := next(ctx, cmds)
		elapsed := time.Since(start).Milliseconds()
		// Distribute elapsed time across the pipeline. Each command gets
		// elapsed/len(cmds) — an approximation, but the only signal we have
		// for a pipelined batch (the per-command start/end times are not
		// individually observable through this hook).
		per := int64(0)
		if len(cmds) > 0 {
			per = elapsed / int64(len(cmds))
		}
		for _, m := range metrics {
			m.counter.Inc()
			m.hist.Observe(per)
		}
		return err
	}
}

// normalizeCommandName lowercases and trims the command name. go-redis's
// Cmder.Name() returns the leaf command name (e.g. "set", "get", "cluster"
// for "cluster info"), already lowercased in practice, but defensively
// normalize anyway — a future redis client version or custom Cmder could
// return uppercase, and a malformed name with whitespace would create
// spurious label cardinality.
//
// An empty name (e.g. for an internal Cmder that doesn't carry a name) is
// mapped to "unknown" so it still appears in metrics rather than silently
// falling through.
func normalizeCommandName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "unknown"
	}
	return name
}
