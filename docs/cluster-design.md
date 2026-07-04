# Cluster Design

This document describes the cluster architecture of gonacos, the current
standalone and Redis-synced modes, and the roadmap for raft-based
replication.

## Current Implementation: Standalone + Redis Modes

gonacos ships with two operating modes selected by the `GONACOS_REDIS_ADDR`
environment variable:

| Mode | Trigger | Behavior |
|------|---------|----------|
| **Standalone** | `GONACOS_REDIS_ADDR` unset | Single process, in-memory state, no replication. |
| **Redis sync** | `GONACOS_REDIS_ADDR=host:port` | Multi-node: each node publishes data changes to Redis pub/sub; member discovery uses a Redis hash with TTL eviction. |

### Standalone Mode

The cluster service (`internal/cluster/service.go`) holds a single member —
the running process itself — and does not attempt any form of distributed
consensus.

#### Properties

- **Single member**: the cluster has exactly one entry marked `IsSelf: true`.
- **No raft groups**: the `opsRaft` endpoint returns
  `ErrNotClusterMode` (HTTP 409) when invoked.
- **No replication**: writes to namespace, config, naming, auth, and AI
  registries live only in the local process memory.
- **No split-brain**: with one node, there is no source of truth to disagree
  with.

### Redis Sync Mode

When `GONACOS_REDIS_ADDR` is set, the process enters Redis sync mode at
startup (`internal/app/redis_cluster.go` wires the sync layer into the
config and naming services). Each node:

1. **Registers itself** in a Redis hash (`gonacos:member:<id>`) with a 10s
   TTL, refreshed by a 3s heartbeat goroutine. The set `gonacos:members`
   tracks all member IDs.
2. **Publishes data changes** to the `gonacos:sync` pub/sub channel as
   `SyncEvent` JSON envelopes. Config and naming services call
   `SetSyncFunc` on every write to fan out the change.
3. **Subscribes** to `gonacos:sync` and applies incoming events via
   idempotent `ApplyRemote*` methods (`ApplyRemotePublish`,
   `ApplyRemoteDelete`, `ApplyRemoteRegister`, `ApplyRemoteDeregister`,
   etc.). Events originated by the local node are skipped to avoid echo.
4. **Discovers members** via `ListMembers()` which reads the member set and
   resolves each ID to its JSON metadata.

#### Properties

- **Multi-node**: any number of processes sharing the same Redis can see
  each other's config and naming writes within pub/sub latency.
- **No raft**: there is no consensus. The Redis broker is the shared state
  bus; each node applies events independently. Conflicting writes (two
  nodes publishing the same dataId) are resolved by last-writer-wins on
  each node.
- **Eventual consistency**: a write is applied locally first, then
  asynchronously published. Other nodes see it after pub/sub delivery.
- **Member lifecycle**: if a node stops heartbeating, its TTL key expires
  and `ListMembers` will drop it. The member set entry is cleaned up lazily
  by the next list call. On graceful shutdown, `Stop()` removes both the
  TTL key and the set entry.
- **No split-brain protection**: a network partition between nodes and
  Redis leaves each node operating standalone. Operators should monitor
  Redis connectivity.

### Why Standalone-Only Was the Original Default

The technical design (`docs/technical-design.md`) initially constrained
gonacos to:

1. **Pure Go standard library** — no external dependencies allowed
   (`go.mod` is empty). A production-grade raft library (etcd-raft,
   hashicorp raft) would require adding dependencies.
2. **In-memory state** — there is no persistence layer; the snapshot/restore
   mechanism (`internal/store/snapshot.go`) is the persistence strategy.
3. **Single-binary deployment** — the goal is a self-contained Nacos v3
   compatible server for development, testing, and small-scale production use.

The constraint was later relaxed to allow `github.com/redis/go-redis/v9` and
`github.com/nacos-group/nacos-sdk-go/v2` for Redis sync and SDK
compatibility testing. Implementing raft from scratch in pure Go remains a
substantial undertaking and is a non-goal for the initial release. The
Redis sync mode covers the common multi-node case without raft's operational
complexity.

## Persistence Strategy: Snapshot / Restore

Instead of replication, gonacos provides durability through the
**backup/restore** layer:

- `GET /v3/admin/ops/backup` — produces a JSON envelope containing the
  serialized state of every registered service (namespace, config, naming,
  auth, ai, cluster).
- `POST /v3/admin/ops/restore` — accepts a previously produced envelope and
  re-applies each service's state via `Snapshotter.Restore`.

Each service implements the `Snapshotter` interface:

```go
type Snapshotter interface {
    SnapshotKey() string
    Snapshot() (any, error)
    Restore(data any) error
}
```

The `store.Coordinator` orchestrates these into a single envelope so callers
don't need to know which services exist.

### Cluster-Specific Endpoints

In addition to the global backup/restore, the cluster service exposes its own
snapshot/restore/status endpoints:

| Method | Path | Purpose |
|--------|------|---------|
| `GET`  | `/v3/admin/core/cluster/snapshot` | Returns just the cluster state (members, plugins, log level). |
| `POST` | `/v3/admin/core/cluster/restore`  | Replaces cluster state from a JSON body. |
| `GET`  | `/v3/admin/core/cluster/status`   | Returns mode, self, member count, snapshot availability, log level. |

These are useful for operators who want to inspect or migrate cluster
membership independently of the other services.

### Restore Semantics for Cluster

When the cluster service restores from a snapshot:

- The **self member is always preserved** from the running process. The
  process identity wins over the backup, so restoring a backup taken on a
  different node does not corrupt the local identity.
- Other members from the backup are merged in. In standalone mode this is
  primarily a metadata operation — there is no live replication to
  establish. In Redis sync mode, the restored members are metadata only;
  live member discovery happens via the Redis hash on the next
  `ListMembers()` call.
- Plugins and log level are replaced wholesale from the backup.

## Roadmap: Raft-Based Cluster Mode

The current Redis sync mode provides multi-node data propagation but not
consensus. A future major version may introduce a true raft-based cluster
mode for operators who need linearizable writes and automatic leader
election. The shape would look like:

1. **Mode flag**: `NewService(ModeRaft, ...)` would initialize the member
   list from a configuration file or seed list instead of seeding only self.
2. **Replication layer**: each service's write path would forward to a
   replication layer before applying locally.
3. **Consensus**: a raft group per service (or a single raft group for all
   state) would order writes. This requires either:
   - Implementing raft in pure Go (significant effort, error-prone), or
   - Adding `hashicorp/raft` or `etcd-io/raft` as a dependency.
4. **Snapshot shipping**: the existing `Snapshot`/`Restore` interface would
   be reused to bring new followers up to date and to recover from
   snapshots stored on disk.
5. **Failover**: with raft, leader election is automatic. The cluster status
   endpoint would gain a `leaderId` field and per-member `state` would
   reflect `LEADER`/`FOLLOWER`/`CANDIDATE`.

### Why Raft Is Not Today

The trade-off is explicit:

- **Redis sync + backup/restore**: minimal dependencies (just go-redis),
  simple to operate, sufficient for multi-node deployments behind a load
  balancer where eventual consistency is acceptable.
- **Raft + raft**: requires an additional dependency or substantial custom
  code, introduces operational complexity (quorum sizing, membership
  changes, log compaction), and is overkill for the current target use case.

Operators who need strong consistency today should run gonacos with a
single Redis-backed node per shard, or wait for the raft mode. Operators
who need high availability with eventual consistency should run multiple
gonacos processes behind a load balancer sharing a single Redis, with
periodic backups.

## Operational Guidance

### Backing Up

```bash
curl -X GET -H "Authorization: Bearer $TOKEN" \
  http://localhost:8848/v3/admin/ops/backup \
  -o gonacos-backup-$(date +%Y%m%d).json
```

The response is a JSON envelope with `Content-Disposition: attachment`. Store
it on durable storage (object store, replicated filesystem).

### Restoring

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @gonacos-backup-20260625.json \
  http://localhost:8848/v3/admin/ops/restore
```

Restore replaces the in-memory state of every service. Run it on a fresh
process or after draining traffic from the existing one.

### Inspecting Cluster State

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8848/v3/admin/core/cluster/status
```

Response (standalone):

```json
{
  "code": 0,
  "data": {
    "mode": "standalone",
    "self": { "id": "127.0.0.1:8848", "ip": "127.0.0.1", "isSelf": true },
    "memberCount": 1,
    "snapshotAvailable": true,
    "snapshotKey": "cluster",
    "logLevel": "INFO"
  }
}
```

Response (Redis sync):

```json
{
  "code": 0,
  "data": {
    "mode": "redis",
    "self": { "id": "10.0.0.1:8848", "ip": "10.0.0.1", "isSelf": true },
    "memberCount": 3,
    "snapshotAvailable": true,
    "snapshotKey": "cluster",
    "logLevel": "INFO"
  }
}
```

### Failover Today

**Standalone mode**: there is no automatic failover. To recover from a
failed node:

1. Provision a new process.
2. Restore from the latest backup before accepting traffic.
3. Update the load balancer to point at the new instance.

The backup envelope is portable across processes — the cluster layer preserves
its own self identity on restore, so restoring a backup from node A onto node B
results in node B reporting its own identity but with the non-self members
from the backup merged in.

**Redis sync mode**: failover is implicit. If a node fails its health
checks, the load balancer routes traffic to the remaining nodes. The failed
node's member entry expires from Redis after the 10s TTL, and
`ListMembers()` will drop it. When a replacement node starts, it joins the
Redis sync group, picks up the current state from subsequent pub/sub
events, and serves traffic. For full state recovery (e.g. existing configs
and services that are not subsequently modified), restore from a backup on
the new node before joining the sync group.
