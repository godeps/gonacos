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

### Resource limits

The in-memory store has no built-in quotas beyond the namespace quota (200
configs per namespace by default). Operators running in production should
monitor memory via the `/v3/admin/ops/metrics` endpoint and restart the
process if heap usage approaches the cgroup limit.

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

The HTTP backup endpoint (`POST /v3/admin/ops/backup`) provides a Redis-agnostic
on-demand JSON snapshot for disaster recovery or migration.

### Health checks

| Endpoint | Purpose |
|----------|---------|
| `GET /v3/console/health/liveness` | Process is alive |
| `GET /v3/console/health/readiness` | Process is ready to serve traffic |
| `GET /v3/admin/core/state/liveness` | Admin liveness |
| `GET /v3/admin/core/state/readiness` | Admin readiness |

Configure load balancer health checks against `/v3/console/health/readiness`.
The readiness probe returns 200 once the HTTP mux is mounted, which happens
synchronously during startup.

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

`GET /v3/admin/ops/metrics` returns Prometheus text exposition format. The
following metrics are always present:

| Metric | Type | Purpose |
|--------|------|---------|
| `process_goroutines` | gauge | Live goroutine count |
| `process_heap_alloc_bytes` | gauge | Go heap allocation |
| `process_gc_count` | gauge | Completed GC cycles |
| `process_start_time_seconds` | gauge | Process start epoch |

Scrape this endpoint from Prometheus:

```yaml
scrape_configs:
  - job_name: gonacos
    static_configs:
      - targets: ["localhost:8848"]
    metrics_path: /v3/admin/ops/metrics
```

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
