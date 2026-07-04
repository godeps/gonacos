# GoNacos Technical Design

## Goal

Build a standalone Go implementation of Nacos current v3 behavior, using
`nacos-sdk-go` as a client compatibility oracle and `other/nacos` as the server
behavior reference. GoNacos must support the full Nacos Web console feature set
and the latest v3 HTTP/gRPC protocol surface. It explicitly does not implement
v1/v2 compatibility endpoints, storage migrations for pre-3.0 schemas, or
legacy Java implementation details that are not observable through v3 APIs.

## Reference Baseline

Use these sources as the initial contract snapshot:

- `../other/nacos` branch `develop`, commit `d54689a77`, described as
  `3.2.2-55-gd54689a77`.
- `../other/nacos-sdk-go` branch `master`, commit `0e3d1d0`, described as
  `v2.3.5-10-g0e3d1d0`.
- Official swagger fetched on 2026-06-24:
  - Client: `https://nacos.io/swagger/client/zh/api.json`
  - Admin: `https://nacos.io/swagger/admin/zh/api.json`
  - Console: `https://nacos.io/swagger/console/zh/api.json`
  - All three report OpenAPI `3.1.0` and Nacos version `3.2.2`.
- Local API coverage registry:
  `../other/nacos/test/openapi-test/API_TEST_COVERAGE.md`.

The local coverage registry records 70 public API scenario rows: 8 client
OpenAPI rows, 35 admin API rows, and 27 console API rows. Strict coverage is
90.00%; effective coverage is 95.00%. GoNacos acceptance must meet or exceed
this registry for every row that does not depend on Java-only extension
runtime.

## Protocol Scope

HTTP surfaces:

- `/v3/client/**`: config query, config listen support exposed through current
  client protocol, naming instance register/list/deregister, AI prompt/skill/
  AgentSpec client download and search.
- `/v3/console/**`: Web console APIs for health, namespace, cluster, server
  state, plugin, config, naming, AI resources, users, roles, permissions, and
  copilot settings.
- `/v3/admin/**`: maintainer and operations APIs from the current admin swagger,
  including broader namespace/config/naming/auth/AI/cluster management.
- `/v3/auth/**`: login, token, user, role, and permission APIs defined by the
  default auth plugin spec.

gRPC surface:

- Reuse the current Nacos proto shape from `nacos-sdk-go`:
  `Payload{metadata, body}`, `Request.request`, `RequestStream.requestStream`,
  and `BiRequestStream.requestBiStream`.
- Route by `Metadata.type` and concrete protobuf `Any` body type.
- Implement only current v3 request/response names used by `nacos-sdk-go`;
  reject unknown or legacy request types with Nacos-compatible error payloads.

Web console:

- Implement the full current `console-ui-next` feature set visible under
  `../other/nacos/console-ui-next/src/pages`.
- Required pages include login/register, welcome, namespace, cluster, service
  management/detail/subscribers, configuration management/detail/editor/history/
  rollback/sync/listeners, plugin management, user/role/permission management,
  setting center, and all AI pages: MCP, A2A agent, prompt, skill, AgentSpec,
  upload/import/detail/version workflows.
- The Web UI should be implemented as a static SPA served by GoNacos, backed
  only by `/v3/console/**` and `/v3/auth/**`.

## Architecture

Use a modular service architecture rather than a Java-package translation.
Each module exposes small Go interfaces and owns its persistence model.

```text
cmd/gonacos
  -> internal/app
       -> http server :8848 /nacos
       -> console server :8080 or same listener by config
       -> grpc server :9848
       -> module registry
  -> internal/protocol
       -> generated OpenAPI adapters
       -> grpc payload dispatcher
       -> Nacos response/error envelope
  -> internal/config
  -> internal/naming
  -> internal/auth
  -> internal/ai
  -> internal/cluster
  -> internal/store
  -> internal/web
```

Startup wiring should use manual constructor injection first. Introduce a DI
container only if module initialization becomes unmanageably cyclic. Every
I/O-facing method takes `context.Context` as its first parameter.

## Storage Model

The technical design below describes the target storage model for a future
production-grade release. The **current implementation** uses Redis as the
storage layer with an embedded miniredis for standalone mode (see
`internal/store/redis_persistence.go` and `internal/store/embedded_redis.go`):

- All service state (namespace, config, naming, auth, ai, cluster) lives in
  process memory for fast reads.
- Cross-restart persistence: the `store.Coordinator` envelope is written to
  Redis key `gonacos:snapshot` on a 30s ticker and on graceful shutdown.
  Standalone mode runs an embedded miniredis (no external dependency) and
  mirrors the envelope to a disk file at `<dataDir>/snapshot.json` so state
  survives process restarts even though miniredis itself is in-memory.
  External Redis mode persists to the shared Redis, so all nodes see the
  same restored state on restart.
- Cross-node replication uses Redis pub/sub for config and naming data
  changes, with Redis-based member discovery and TTL eviction. This avoids
  the raft dependency while supporting multi-node deployments behind a load
  balancer.

The full SQL + raft model remains the long-term target:

- Embedded mode: SQLite or Pebble-compatible local store for development and
  tests.
- Production mode: PostgreSQL/MySQL-compatible SQL store for durable metadata.
- Cluster consensus: per-domain raft groups for naming ephemeral state,
  persistent config changes, auth metadata, AI metadata, and cluster members.

Core tables or buckets:

- `namespace`
- `config_item`, `config_history`, `config_gray`
- `service`, `service_cluster`, `instance`, `subscriber`
- `user`, `role`, `permission`, `token_state`
- `plugin`, `plugin_config`, `plugin_status`
- `ai_resource`, `ai_resource_version`, `ai_resource_label`,
  `ai_resource_blob`
- `cluster_member`, `raft_snapshot`, `raft_log`

Ephemeral naming instances should be memory-first with heartbeat/lease expiry,
then replicated through raft events. Persistent instances and service metadata
must survive restart.

## Module Responsibilities

### Config

- Publish/query/delete config with `dataId`, `groupName`, `namespaceId`, type,
  content, md5, tags, description, and gray/beta fields.
- History list/detail/previous/config snapshots.
- Batch delete, import/export zip, clone, listener status, and IP-scoped
  listener status.
- Long polling or gRPC notification for config changes.
- CAS publish based on md5 where current SDK/API requires it.

### Naming

- Service create/update/delete/query/list and selector types.
- Cluster metadata update and health checker metadata.
- Instance register/update/list/delete with namespace/group/cluster defaults,
  weight, enabled, healthy, metadata, ephemeral flag, and heartbeat lease.
- Subscriber list and service-level watch notifications.

### Auth

- Default username/password login and JWT-like access token issuance.
- Users, roles, permissions, admin bootstrap, password changes.
- Permission resources follow the current spec:
  `{namespaceId}:{group}:{signType}/{resourceName}` for config/naming and
  `console/users`, `console/roles`, `console/permissions` for console auth.
- Plugin shape must allow later RAM/OIDC equivalents, but the first milestone
  implements default `nacos` auth completely.

### AI Registry

- MCP server lifecycle, tool import validation/execution.
- A2A agent lifecycle and version list.
- Prompt lifecycle: draft, submit, publish, force publish, redraft, online,
  offline, labels, description, biz tags, Markdown download.
- Skill lifecycle: draft/fork/upload/batch upload, submit, publish, labels,
  biz tags, scope, online/offline, ZIP download.
- AgentSpec lifecycle: draft/upload, resource tree, versions, labels, scope,
  online/offline.
- Import source list/search/validate/execute.
- Pipeline and copilot endpoints with compatible success/error shapes; external
  LLM calls must be pluggable and disabled by default in tests.

### Cluster and Plugins

- Standalone mode is a first-class mode.
- Cluster mode exposes member list, liveness/readiness, server state, plugin
  list/detail/availability/config/status, and admin operations.
- Plugin interfaces should be Go-native and typed. Java SPI compatibility is
  not required, but observable plugin APIs and configuration state are required.

## OpenAPI and Code Generation

1. Pin fetched swagger JSON files under `api/openapi/upstream/`.
2. Generate Go server interfaces and request/response schemas into
   `internal/protocol/openapi`.
3. Add a contract diff tool that compares pinned specs against local
   `other/nacos` or official swagger and fails when paths, methods, parameters,
   status codes, or response schemas drift.
4. Do not hand-code request parsing for generated endpoints unless the generator
   cannot represent a multipart or streaming edge case.

Current implementation status:

- `api/openapi/upstream/*.json` pins official client/admin/console Swagger.
- `api/proto/nacos_grpc_service.proto` pins the SDK gRPC service definition.
- `cmd/gonacos-contract` generates and verifies
  `api/openapi/manifest.json`.
- `internal/app.NewHandler` registers every manifest HTTP operation. Every
  operation is backed by a real handler — the previous 501 contract stubs
  have all been implemented (see `internal/app/stub_handlers.go` for the
  remaining admin/console operations).
- `internal/namespace` implements console/admin namespace create, detail,
  update, delete, list, and existence checks with `public` seeded by default.
- `internal/config` implements publish, detail, delete, list, batch delete,
  clone, history list/detail/previous/config snapshots, namespace config
  listing, import/export zip, listener status lookup, beta publish/query/
  stop, gray publish/query/delete/list, admin metadata update, content
  search, capacity query/update, client metrics, cluster client metrics,
  local cache refresh, and client config query with md5 and lastModified
  fields. gRPC listener push is implemented: when a config changes, the
  server pushes a `ConfigChangeNotifyRequest` frame to all SDK clients
  that have issued a `ConfigBatchListenRequest` for that config. The SDK
  then re-queries the config to fetch the new content.
- `internal/naming` implements service create/update/delete/query/list,
  cluster metadata, instance register/update/list/delete with heartbeat
  lease, subscriber tracking, client tracking (list/detail/published/
  subscribed/publisher/subscriber), health update, naming metrics,
  distribution status, and service-level watch push (the server pushes a
  `NotifySubscriberRequest` frame to subscribed SDK clients when instances
  change).
- `internal/auth` implements default username/password login, JWT-like
  HMAC-SHA256 access token issuance with generation-based revocation, users,
  roles, permissions, admin bootstrap, password changes, and permission
  enforcement middleware (admin-only route gating, three-source token
  extraction for SDK compatibility).
- `internal/ai` implements MCP/A2A/prompt/skill/AgentSpec lifecycle
  (draft, submit, publish, force publish, redraft, online/offline, labels,
  biz tags), import source list/search/validate/execute, version metadata,
  and ZIP download/upload.
- `internal/cluster` implements standalone mode, Redis-based cross-node
  sync (pub/sub for config and naming data changes, member discovery via
  Redis hash with TTL eviction), cluster snapshot/restore/status endpoints,
  plugin config/status management, and member list/lookup.
- `internal/store` implements the `Coordinator` orchestrator and
  `Snapshotter` interface for backup/restore across all services.
- `internal/observability` implements the metrics registry (Prometheus
  text exposition), process info, and pprof endpoints.
- `internal/web` serves the console SPA under `/v3/console/ui`.

## Implementation Phases

1. Contract pinning and compatibility harness.
2. HTTP envelope, error model, auth middleware, and health endpoints.
3. Namespace, plugin, server state, and cluster standalone APIs.
4. Config core, history, import/export, clone, listener, and SDK compatibility.
5. Naming core, heartbeat/lease, subscriptions, and SDK compatibility.
6. Full auth management and permission enforcement.
7. AI registry resources and import/upload/download workflows.
8. Web console SPA backed by `/v3/console/**`.
9. Cluster replication, snapshots, failover, and rolling upgrade behavior.
10. Performance, observability, backup/restore, and hardening.

## Non-Goals

- No `/v1/**` or `/v2/**` compatibility.
- No pre-3.0 storage migration behavior.
- No Java SPI binary compatibility.
- No exact Java internal package or class layout replication.
- No implementation of external LLM, MSE, cloud RAM, or OIDC providers beyond
  pluggable contracts and compatible disabled/default behavior.
