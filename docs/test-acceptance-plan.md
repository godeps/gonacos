# GoNacos Test and Acceptance Plan

## Acceptance Principle

GoNacos is accepted when current Nacos v3 clients and the current Web console
can use it without code changes, and when its observable behavior matches the
current Nacos v3 API contract. Tests should be generated from pinned contracts
and enriched with behavior scenarios copied from `../other/nacos/test`.

## Test Pyramid

- Unit tests: domain validation, error mapping, auth decisions, md5/CAS logic,
  lease expiry, import/export parsing, AI resource version state machines.
- Handler tests: `httptest` against every generated `/v3/**` endpoint.
- SDK compatibility tests: run `nacos-sdk-go` against a live GoNacos instance.
- Contract tests: compare GoNacos responses with a reference Nacos container or
  local `other/nacos` process for the same request corpus.
- Browser tests: Playwright against the Web console for every primary workflow.
- Cluster tests: multi-node process tests with network partitions, restarts,
  snapshots, and leader transfer.
- Performance and soak tests: throughput, latency, memory, goroutine leaks, and
  long-lived watch stability.

## Contract Coverage Targets

Use `../other/nacos/test/openapi-test/API_TEST_COVERAGE.md` as the first
coverage ledger.

| Surface | Required Result |
| --- | --- |
| Client OpenAPI | 100% scenario rows passing |
| Console API | 100% scenario rows passing or documented Java-only exclusion |
| Admin API | 100% scenario rows passing or documented Java-only exclusion |
| Auth API | login, token refresh/expiry, user/role/permission CRUD, admin bootstrap |
| gRPC | `Request`, `RequestStream`, `BiRequestStream` with all current SDK request bodies |
| Web console | all `console-ui-next` pages load and complete their primary workflows |

Any exclusion must name the upstream scenario, state why it is not observable or
not applicable to a Go implementation, and include an equivalent GoNacos test.

## API Scenario Matrix

Client API:

- `GET /v3/client/cs/config`
- `POST /v3/client/ns/instance`
- `GET /v3/client/ns/instance/list`
- `DELETE /v3/client/ns/instance`
- `GET /v3/client/ai/prompt`
- `GET /v3/client/ai/skills`
- `GET /v3/client/ai/agentspecs`
- `GET /v3/client/ai/agentspecs/search`

Console API groups:

- Health: liveness and readiness.
- Core: namespace create/query/update/list/exist/delete, cluster nodes.
- Server and plugin: state, announcement, guide, plugin detail/list/
  availability/config/status.
- Config: publish/query/update/delete, list/search detail, listeners, history,
  beta/gray, batch delete, export/import, clone.
- Naming: service CRUD/list/selector/subscribers, cluster metadata, instance
  update/list/delete.
- AI: A2A, MCP, prompt, skill, skill upload, AgentSpec, AgentSpec upload, import
  sources/search/validate/execute, pipeline, copilot.
- Auth and console security: login, register/bootstrap, users, roles,
  permissions, password change, route guards.

Admin API groups:

- Mirror every current row in
  `../other/nacos/test/openapi-test/ADMIN_API_TEST_SCENARIOS.md`.
- Admin endpoints must run under admin-auth enabled and disabled modes.
- Mutating runtime operations must use isolated temporary stores and clusters.

## SDK Compatibility

Create Go integration tests under `tests/sdkcompat` that import
`github.com/nacos-group/nacos-sdk-go/v2` and run against `gonacos`.

Required flows:

- Config: publish, get, delete, search, listen, cancel listen, CAS publish,
  local cache behavior during server restart.
- Naming: register, batch register, list/select, subscribe, unsubscribe,
  deregister, heartbeat expiry.
- Security: username/password token acquisition and request signing headers.
- TLS: HTTP and gRPC clients with custom CA.
- Error behavior: missing required fields, unknown namespace, invalid group,
  invalid weight/port, unauthorized requests.

## Browser Acceptance

Use Playwright with deterministic seed data.

Required specs:

- Login/logout, password change, admin bootstrap.
- Namespace CRUD and namespace switch propagation across pages.
- Config create/edit/publish/history/diff/rollback/import/export/clone.
- Service create/edit/detail, instance update/delete, cluster metadata,
  subscriber list.
- User, role, and permission CRUD with permission enforcement checks.
- Plugin list/detail/config/status.
- AI resource workflows: MCP create/import tool, A2A create/version, prompt
  draft/publish/online/offline/download, skill upload/batch upload/scope/
  download, AgentSpec upload/resource browsing/version workflow.
- Responsive smoke for desktop and mobile widths.

Every UI spec must assert both UI state and backing API state.

## Cluster Acceptance

Run three GoNacos nodes with isolated data directories.

Required checks:

- Initial leader election and member list convergence.
- Config write on one node is readable from all nodes.
- Naming instance heartbeat and expiry are consistent across nodes.
- Auth and permission changes replicate before protected requests succeed.
- AI resource publish/download works after leader failover.
- Kill/restart leader, network partition minority, recover partition, and verify
  no lost committed writes.
- Snapshot restore and backup/restore produce identical API-visible state.

## Performance Gates

Initial non-production targets, to be refined after baseline measurements:

- Config query p95 under 20 ms at 1,000 RPS on local loopback.
- Naming instance list p95 under 30 ms for 10,000 instances.
- 10,000 config listeners with stable memory and no goroutine leak.
- 50,000 ephemeral instances with heartbeat processing under 1 CPU core in
  steady state.
- Web console API p95 under 100 ms for list pages with realistic pagination.

Use Go benchmarks, `go test -race`, pprof CPU/heap profiles, and long-running
soak tests. Packages with long-lived goroutines should use `go.uber.org/goleak`
in tests.

## Definition of Done

- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `golangci-lint`
  pass for the module.
- Pinned OpenAPI/proto specs are committed and contract diff passes.
- Generated conformance tests cover all current v3 endpoints.
- `nacos-sdk-go` compatibility suite passes.
- Playwright Web console suite passes against a fresh GoNacos server.
- Three-node cluster suite passes with restart and partition scenarios.
- Documentation includes config, deployment, backup/restore, and upgrade notes.

## Current Automated Gates

The current scaffold already supports these gates:

- `make contract-generate`: rebuilds `api/openapi/manifest.json` from pinned
  OpenAPI/proto contracts.
- `make contract-verify`: fails if the manifest is stale.
- `GOWORK=off go test ./...`: includes contract parser tests and first HTTP
  route tests for health, route stubs, unknown routes, and namespace console/
  admin lifecycle plus validation behavior. It also covers core config
  admin/console/client publish-query-delete-list-batchDelete-clone-history-
  import-export-listener-beta behavior and required-field validation.
- `GOWORK=off go vet ./...`
- `make build`
