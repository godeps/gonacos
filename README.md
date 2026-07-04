# GoNacos

GoNacos is a new standalone Go implementation plan for the current Nacos v3
server protocol. It is intentionally scoped to the latest v3 HTTP, gRPC, auth,
admin, console, AI registry, and Web console surfaces, without v1/v2 protocol
compatibility.

Reference baseline:

- Local server reference: `../other/nacos`, branch `develop`,
  `3.2.2-55-gd54689a77`.
- Local Go SDK reference: `../other/nacos-sdk-go`, `v2.3.5-10-g0e3d1d0`.
- Official swagger checked on 2026-06-24:
  `https://nacos.io/swagger/client/zh/api.json`,
  `https://nacos.io/swagger/admin/zh/api.json`,
  `https://nacos.io/swagger/console/zh/api.json`, all reporting version
  `3.2.2` and OpenAPI `3.1.0`.

## Project Layout

```text
gonacos/
  cmd/gonacos/          executable entry point
  internal/protocol/    Nacos v3 HTTP and gRPC wire contracts
  internal/config/      configuration service
  internal/naming/      naming and instance health
  internal/auth/        users, roles, permissions, tokens, plugins
  internal/cluster/     membership and raft replication
  internal/store/       storage interfaces and database adapters
  internal/web/         console API and static UI serving
  api/openapi/          pinned/generated OpenAPI contracts
  web/                  Nacos-compatible Web console implementation
  docs/                 design and acceptance documents
```

## Current State

The project now has an independent Go module, pinned upstream v3 OpenAPI/proto
contracts, a generated contract manifest, a contract drift verifier, and a
first HTTP server skeleton. Implemented endpoints currently cover health/state
probes, console/admin namespace CRUD/list/existence APIs, and config
publish/query/delete/list/batch-delete/clone/history/import/export/listener/
beta query plus client query APIs backed by in-memory services. The rest of
the known v3 HTTP surface is registered from the manifest and currently returns
a standard `501` v3 result until each module replaces its stub.

Useful commands:

```sh
make contract-generate
make contract-verify
make test
make build
GOWORK=off go run ./cmd/gonacos serve :8848
```

Start here:

- [Technical Design](docs/technical-design.md)
- [Test and Acceptance Plan](docs/test-acceptance-plan.md)
- [中文技术方案](docs/技术方案.md)
- [中文测试验收方案](docs/测试验收方案.md)
