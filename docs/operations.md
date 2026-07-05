# Operations Guide

This guide covers configuration, deployment, backup/restore, observability,
and upgrade procedures for GoNacos. It targets operators running the server
in production and development.

## Configuration

GoNacos is configured through command-line flags and environment variables.
There is no external config file; the server is intentionally minimal so the
full configuration surface is visible in `gonacos serve --help`.

### Startup modes

```
gonacos version                # print build version
gonacos serve [addr]           # start HTTP + gRPC servers
```

The default HTTP listen address is `:8848`. The gRPC server follows the
Nacos convention of HTTP port + 1000 (e.g. `8848` → `9848`).

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `GONACOS_ADDR` | `:8848` | HTTP listen address |
| `GONACOS_LOG_LEVEL` | `INFO` | Process log level |
| `GONACOS_ROOT` | `.` | Working directory for contract files |
| `GONACOS_REDIS_ADDR` | (unset) | Redis address for storage and multi-node sync. When set, the process uses the external Redis for both snapshot persistence (key `gonacos:snapshot`) and pub/sub sync. When unset, the process starts an embedded miniredis for in-process persistence with a disk-backed dump, and runs in standalone mode (no cross-node sync). |
| `GONACOS_DATA_DIR` | `<root>/.gonacos/data` | Directory for the embedded Redis disk dump (`snapshot.json`). Only used in standalone (embedded) mode. |
| `GONACOS_SNAPSHOT_INTERVAL` | `30s` | Interval for periodic snapshot saves. Go duration syntax (e.g. `10s`, `2m`). |
| `GONACOS_HTTP_RATE_RPS` | `0` (disabled) | Per-client-IP HTTP rate limit in requests per second. Burst defaults to 2x rps. Recommended production: `100`. |
| `GONACOS_HTTP_MAX_BODY` | `10485760` (10 MiB) | Maximum HTTP request body size in bytes. `-1` disables the cap. |
| `GONACOS_HTTP_WRITE_TIMEOUT` | `30s` | Maximum HTTP response write duration. `-1` disables. |
| `GONACOS_HTTP_IDLE_TIMEOUT` | `120s` | Maximum idle keep-alive duration. `-1` disables. |

### Resource limits

The in-memory store has no built-in quotas beyond the namespace quota (200
configs per namespace by default). HTTP-level limits protect against abuse:

- **Request body cap** (`GONACOS_HTTP_MAX_BODY`, default 10 MiB): oversized
  POST/PUT bodies return 413 instead of OOMing the server.
- **Per-IP rate limit** (`GONACOS_HTTP_RATE_RPS`, default disabled): token
  bucket per client IP, honored with burst = 2x rps. Exceeding the limit
  returns 429 (HTTP) or RESOURCE_EXHAUSTED (gRPC status 8) with a
  `Retry-After` header. The same limiter covers both protocols, so a single
  client IP shares one bucket across HTTP and gRPC — an SDK client cannot
  bypass its HTTP quota by switching protocols.
- **HTTP timeouts**: `ReadHeaderTimeout` 5s, `WriteTimeout`
  (`GONACOS_HTTP_WRITE_TIMEOUT`, default 30s), `IdleTimeout`
  (`GONACOS_HTTP_IDLE_TIMEOUT`, default 120s). The gRPC HTTP/2 server
  also enforces `ReadHeaderTimeout` 5s (slowloris protection) and
  `IdleTimeout` 5m.
- **Shutdown timeout** (`GONACOS_SHUTDOWN_TIMEOUT`, default 30s): the
  maximum time `Shutdown` waits for in-flight HTTP/gRPC handlers to
  complete before forcibly closing connections. Prevents a stuck handler
  from blocking a rolling restart indefinitely. Set to `-1` to wait
  forever (not recommended in production).
- **gRPC frame size cap** (`GONACOS_GRPC_MAX_FRAME_BYTES`, default 4 MiB):
  the maximum payload size of a single gRPC frame the server accepts. A
  peer declaring a larger frame is rejected with `RESOURCE_EXHAUSTED`
  (gRPC status 8) before any body allocation, so a malicious client
  cannot drive the process into OOM by claiming a 4 GiB body. Set to
  `-1` to disable the cap (not recommended in production).

Operators running in production should monitor memory via the `/metrics`
endpoint and restart the process if heap usage approaches the cgroup limit.

## Security hardening

### Security response headers

Every HTTP response carries standard security headers that protect the
embedded React console and the JSON API from common client-side attacks:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Blocks MIME sniffing |
| `X-Frame-Options` | `SAMEORIGIN` | Clickjacking protection |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limits referrer leakage |
| `X-XSS-Protection` | `0` | Disables buggy legacy XSS auditor |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | HSTS (only under TLS) |

Headers are set by the outermost middleware so they appear on every
response — including 429s from rate limiting, 413s from body caps, 500s
from panic recovery, and 404s from the catch-all. Inner handlers can
override any header per-route.

### Login brute-force protection

The `/v3/auth/user/login` endpoint is wrapped with a per-(client-IP,
username) lockout policy. After `maxFailures` consecutive failed logins
within `failWindow`, the pair is locked for `lockoutDuration`; a successful
login resets the counter. Recommended production: 5 failures, 5m window,
15m lockout.

Configure via the `WithLoginThrottle(maxFailures, failWindow, lockoutDuration)`
option when embedding, or run `gonacos serve` with the appropriate flags.
Disabled by default — set `maxFailures > 0` to enable.

A locked pair receives `429 Too Many Requests` with a `Retry-After` header
without calling the login handler, so an attacker cannot probe passwords
even if the underlying auth service is slow.

### Request tracing

Every response carries an `X-Request-Id` header (e.g.
`gonacos-1m0abc23-000042`). The ID is generated from an atomic counter +
process start time (no crypto dependency on the hot path) and is logged
in the access log line, so an operator receiving a report can correlate
a specific response to its log entry by the request ID.

IDs are scoped to the process and reset on restart — they are not
globally unique, but they are unique within a single process lifetime
and stable enough for log correlation.

### Panic recovery

Both HTTP and gRPC handlers are wrapped with a deferred `recover()`. A
panicking handler produces a structured 500 response (HTTP) or gRPC
INTERNAL status (code 13) carrying the request ID, plus a single log
line with the stack trace. The server stays up — the panic does not
crash the process or tear down the connection.

Without recovery, Go's `net/http` recovers the panic but writes no
response, so the client sees a connection reset and the operator has no
log line tying the failure to a request.

## Deployment

### Single process (standalone)

The default deployment is a single process with in-memory state. This is the
mode implemented by `cluster.Mode = "standalone"`. It is appropriate for
development, CI, and small production workloads that can tolerate a restart
on failure.

```
gonacos serve :8848
```

### Multi-node (Redis sync)

For workloads that need horizontal scaling or cross-node data propagation,
run multiple gonacos processes sharing a single Redis. Each node publishes
config and naming writes to Redis pub/sub and applies remote events via
idempotent `ApplyRemote*` methods.

```
GONACOS_REDIS_ADDR=redis.internal:6379 gonacos serve :8848
```

Run one process per host behind a load balancer. All nodes serve both read
and write traffic — there is no leader/follower distinction. Writes are
applied locally first, then asynchronously fanned out via pub/sub, so
other nodes see them within pub/sub delivery latency.

Member discovery is automatic: each node registers itself in Redis with a
10s TTL refreshed by a 3s heartbeat. `GET /v3/admin/core/cluster/node/list`
returns all live members. A node that stops heartbeating is dropped from
the list after the TTL expires.

This mode provides **eventual consistency**, not linearizability. Two
nodes writing to the same config key simultaneously will both apply locally
and publish; the last pub/sub event to arrive on each node wins. For
strong consistency, run a single gonacos process per shard.

### Persistence

State is persisted to Redis on a 30s ticker and on graceful shutdown. The
snapshot envelope (the same JSON shape used by the HTTP backup endpoint) is
stored under the `gonacos:snapshot` Redis key.

- **Standalone mode** (no `GONACOS_REDIS_ADDR`): an embedded miniredis runs
  in-process. The envelope is also mirrored to
  `<GONACOS_DATA_DIR>/snapshot.json` so state survives process restarts.
  Killing the process ungracefully may lose up to 30s of writes (the periodic
  tick window). Send `SIGINT` or `SIGTERM` for a clean shutdown that flushes
  the final snapshot.
- **Redis sync mode** (`GONACOS_REDIS_ADDR` set): the envelope is stored in
  the shared external Redis. Durability depends on the Redis instance's own
  persistence configuration (RDB/AOF). All nodes see the same restored state
  on restart.

**Atomic writes**: the disk dump is written via a temp-file-then-rename
pattern, so a crash mid-write cannot leave a half-written dump file that
would fail to load on next startup. The dump file is either the previous
snapshot or the new one — never a partial write.

**Backup rotation**: when `WithSnapshotBackupCount(n)` is set (n > 0), each
save shifts the existing dump file to `snapshot.1.json`, `snapshot.1.json`
to `snapshot.2.json`, ..., dropping the oldest when the count is exceeded.
This protects against a corrupted latest snapshot — the operator can
manually promote `snapshot.1.json` to `snapshot.json` and restart. Recommended
production value: 5.

The HTTP backup endpoint (`POST /v3/admin/ops/backup`) provides a Redis-agnostic
on-demand JSON snapshot for disaster recovery or migration.

### Health checks

| Endpoint | Purpose |
|----------|---------|
| `GET /v3/console/health/liveness` | Process is alive (always 200 once started) |
| `GET /v3/console/health/readiness` | Process is ready to serve traffic (pings Redis) |
| `GET /v3/admin/core/state/liveness` | Admin liveness |
| `GET /v3/admin/core/state/readiness` | Admin readiness (pings Redis) |

Configure load balancer health checks against `/v3/console/health/readiness`.
The readiness probe pings the Redis client (external or embedded) with a 2s
timeout and returns:

- `200 OK` when Redis is reachable — the node can persist state and accept writes.
- `503 Service Unavailable` when Redis is unreachable — load balancers should
  stop sending traffic to a node that can't persist state.

Liveness (`/liveness`) returns 200 as long as the process is alive,
regardless of dependency state. Use liveness for kubelet's "restart the pod"
probe and readiness for the load balancer's "route traffic here" probe.

## Backup and restore

Backups capture all in-memory state as a single JSON envelope. Each service
(namespace, config, naming, auth, ai, cluster) implements its own
snapshot/restore; the coordinator stitches them together.

### Backup

```
curl -f http://localhost:8848/v3/admin/ops/backup -o gonacos-$(date +%Y%m%d).json
```

The response is a JSON document with this top-level shape:

```json
{
  "version": "gonacos/v1",
  "created_at": "2026-06-25T12:00:00Z",
  "services": {
    "namespace": [...],
    "config": {...},
    "naming": [...],
    "auth": {...},
    "ai": {...},
    "cluster": {...}
  }
}
```

### Restore

```
curl -X POST -H "Content-Type: application/json" \
  --data-binary @gonacos-20260625.json \
  http://localhost:8848/v3/admin/ops/restore
```

Restore replaces the current in-memory state wholesale. Any state present
before the restore that is not in the backup envelope is discarded.

### Scheduled backup

A daily cron is the simplest pattern:

```
0 2 * * * curl -fsS http://localhost:8848/v3/admin/ops/backup -o /var/backups/gonacos/$(date +\%Y\%m\%d).json && find /var/backups/gonacos -mtime +30 -delete
```

### Restore semantics

- **Auth tokens**: tokens issued by the current process remain valid after
  restore when the password hash matches. The HMAC signing secret is
  process-local, so a cross-process restore (e.g. loading a backup taken on
  node A onto node B) invalidates outstanding tokens via signature
  mismatch, not revocation. Users from the backup replace the current user
  list wholesale.
- **Naming ephemeral instances**: the last heartbeat timestamp is preserved.
  The lease tracker re-evaluates expiry against the current clock, so
  instances that have been silent for longer than the TTL are marked
  unhealthy immediately after restore.
- **Cluster self member**: the running process's self identity is always
  preserved over the backup's self entry. Other members from the backup are
  merged in. In Redis sync mode, live member discovery happens via the
  Redis hash on the next `ListMembers()` call, so backup members are
  metadata only.

## Observability

### Metrics

`GET /metrics` returns Prometheus text exposition format on the standard
scrape path (no `/nacos` prefix), so the default `prometheus.yml`
`metrics_path: /metrics` works without configuration. An admin-only mirror
is also available at `GET /v3/admin/ops/metrics` (gated by the auth
middleware's anonymous-permissive policy).

The following metrics are always present:

| Metric | Type | Purpose |
|--------|------|---------|
| `process_goroutines` | gauge | Live goroutine count |
| `process_heap_alloc_bytes` | gauge | Go heap allocation |
| `process_gc_count` | gauge | Completed GC cycles |
| `process_start_time_seconds` | gauge | Process start epoch |
| `gonacos_http_requests_total{method,status}` | counter | HTTP requests served, by method and status code |
| `gonacos_http_request_duration_seconds{method}` | histogram | HTTP request latency in milliseconds (buckets: 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000) |
| `gonacos_grpc_requests_total{method,status}` | counter | gRPC requests served, by gRPC path and status code |
| `gonacos_grpc_request_duration_seconds{method}` | histogram | gRPC request latency in milliseconds (same buckets as HTTP) |
| `gonacos_namespaces_total` | gauge | Number of namespaces |
| `gonacos_configs_total` | gauge | Total config items across all namespaces |
| `gonacos_services_total` | gauge | Total registered services across all namespaces |
| `gonacos_instances_total` | gauge | Total service instances across all services (currently 0 — placeholder for future instance-count tracking) |
| `gonacos_users_total` | gauge | Number of registered users |
| `gonacos_push_total{type="config"}` | counter | Config change notifications pushed to subscribers |
| `gonacos_push_total{type="service"}` | counter | Service change notifications pushed to subscribers |
| `gonacos_config_subscriptions` | gauge | Active config subscriptions (client × dataId) |
| `gonacos_service_subscriptions` | gauge | Active service subscriptions (client × serviceName) |

Scrape this endpoint from Prometheus:

```yaml
scrape_configs:
  - job_name: gonacos
    static_configs:
      - targets: ["localhost:8848"]
    # metrics_path defaults to /metrics — no override needed.
```

### Access log

Each HTTP request is logged at INFO level with method, path, status, bytes,
duration, remote address, and request ID. Health and metrics probes are
excluded by default to keep the signal-to-noise ratio high. Set
`GONACOS_HTTP_VERBOSE_LOG=1` or pass `WithHTTPVerboseLog(true)` to log
every request including probes.

Each gRPC request is logged similarly: `grpc <method> <path> status=<code>
duration=<dur> remote=<addr>`. Unary RPCs log when the response is sent;
streaming RPCs log when the stream closes (one line per connection, not
per frame). The gRPC access log uses the same logger as the HTTP access
log, so a single log stream covers both protocols.

### Process info

`GET /v3/admin/ops/info` returns a JSON snapshot of the process: version,
goroutine count, heap stats, GC count, and current time.

### Profiling

Go pprof endpoints are mounted under `/v3/admin/ops/pprof/`:

| Path | Purpose |
|------|---------|
| `/v3/admin/ops/pprof/` | Index of available profiles |
| `/v3/admin/ops/pprof/heap` | Heap profile |
| `/v3/admin/ops/pprof/goroutine` | Goroutine profile |
| `/v3/admin/ops/pprof/profile` | CPU profile (30s default) |
| `/v3/admin/ops/pprof/trace` | Execution trace |

Capture a CPU profile:

```
go tool pprof http://localhost:8848/v3/admin/ops/pprof/profile?seconds=60
```

## Upgrade

### In-place upgrade with backup

1. Back up the current state:
   ```
   curl -f http://localhost:8848/v3/admin/ops/backup -o pre-upgrade.json
   ```
2. Stop the old process.
3. Replace the binary.
4. Start the new process.
5. Restore state:
   ```
   curl -X POST -H "Content-Type: application/json" \
     --data-binary @pre-upgrade.json \
     http://localhost:8848/v3/admin/ops/restore
   ```

### Rolling upgrade (Redis sync mode)

In Redis sync mode, rolling upgrades are supported because each node's
in-memory state is continuously refreshed by pub/sub events from peers.
To upgrade:

1. Drain traffic from one node (e.g. mark it unhealthy in the load
   balancer).
2. Stop the node.
3. Replace the binary.
4. Start the new node — it rejoins the Redis sync group, picks up
   subsequent pub/sub events, and serves traffic.
5. Repeat for each node.

For full state recovery (e.g. existing configs that are not subsequently
modified), restore from a backup on the new node before joining the sync
group. There is no leader election, so any node can be upgraded first.

In standalone mode, state is persisted to the embedded Redis disk dump on
shutdown and restored on startup, so a simple stop-binary-start cycle
preserves state. The in-place backup procedure above is still recommended
for extra safety before upgrades.

### Version compatibility

The backup envelope carries a `version` field (`gonacos/v1`). Future
releases that change the schema will bump this version. Restore rejects
envelopes with a missing version; operators should inspect the field before
restoring backups from unknown sources.
