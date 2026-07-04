# GoNacos

[English](README.md) | [中文](README.zh-CN.md)

GoNacos is a Nacos v3-compatible server implemented in Go. It speaks the
Nacos v3 HTTP and gRPC wire protocols, so the official `nacos-group/nacos-sdk-go`
client and other v3 SDKs work against it unmodified. Run it as a binary, or
embed it as a library inside another Go program.

## Features

- **v3 wire compatible**: HTTP (`/v3/admin`, `/v3/console`, `/v3/client`, `/v3/auth`)
  and gRPC (`Request`, `RequestStream`, `BiRequestStream`) surfaces matching
  Nacos v3.2.2.
- **Configuration service**: publish/query/delete/list, batch listen, history,
  clone, import/export, beta/gray releases.
- **Naming service**: instance register/deregister, service list/discover,
  subscriber push, health checks, ephemeral leases.
- **Auth**: users, roles, permissions, HMAC token login, RBAC authorization.
- **Namespace**: CRUD with the default `public` namespace seeded.
- **Cluster**: standalone (embedded miniredis) or Redis-backed multi-node sync
  via pub/sub.
- **AI registry**: prompts, skills, agent specs, MCP servers, A2A agents
  (Nacos AI extension).
- **Persistence**: snapshot/restore all services to a single envelope; periodic
  save to Redis or disk.
- **Embeddable**: import `github.com/godeps/gonacos/pkg/server` and run a
  Nacos-compatible service inside your own process.

## Install

As a library:

```sh
go get github.com/godeps/gonacos@latest
```

As a binary:

```sh
git clone https://github.com/godeps/gonacos
cd gonacos
make build
```

## Quick start (server binary)

```sh
make build
./gonacos serve :8848
```

Health check:

```sh
curl http://localhost:8848/v3/console/health/liveness
# {"code":0,"message":"success","data":"ok"}
```

Publish and query a config (curl, or use the upstream Go SDK):

```sh
curl 'http://localhost:8848/v3/admin/cs/config' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"dataId":"app.yml","groupName":"DEFAULT_GROUP","content":"key: value","type":"yaml"}'
curl 'http://localhost:8848/v3/client/cs/config?dataId=app.yml&groupName=DEFAULT_GROUP'
```

## Embed in your program

Import `github.com/godeps/gonacos/pkg/server` and construct a `*server.Server`:

```go
package main

import (
	"context"
	"log"

	"github.com/godeps/gonacos/pkg/config"
	"github.com/godeps/gonacos/pkg/server"
)

func main() {
	srv, err := server.New(
		server.WithAddr(":8848"),
		server.WithRoot("."), // dir containing api/openapi/upstream/ for 501 stubs
	)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := srv.Start(context.Background()); err != nil {
			log.Printf("serve: %v", err)
		}
	}()

	// Three usage modes:

	// 1. HTTP/gRPC in-process: any Nacos v3 SDK can reach
	//    http://localhost:8848 and gRPC at localhost:9848.

	// 2. Direct service call (no network hop):
	bundle := srv.Services()
	_ = bundle.Config.Publish(config.PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "app.yml",
		Content:     "key: value",
		Type:        "yaml",
	})
	item, _ := bundle.Config.Get("public", "DEFAULT_GROUP", "app.yml")
	log.Printf("config: %s", item.Content)

	// 3. Snapshot/restore for backup:
	env, _ := srv.Snapshot()
	_ = env // marshal to JSON, write to disk, etc.

	// Graceful shutdown flushes the snapshot and closes resources:
	// _ = srv.Shutdown(ctx)
}
```

## Configuration

Options (`server.With*`):

| Option | Default | Description |
|---|---|---|
| `WithAddr(addr)` | `:8848` | HTTP listen address. |
| `WithGRPCAddr(addr)` | derived (`HTTP+1000`) | gRPC listen address. |
| `WithRedisAddr(addr)` | `""` (embedded) | Redis address. Empty = embedded miniredis (standalone). Non-empty = external Redis + cluster sync. |
| `WithDataDir(dir)` | `<root>/.gonacos/data` | Directory for the embedded Redis disk dump. Ignored when `WithRedisAddr` is set. |
| `WithSnapshotInterval(d)` | `30s` | Periodic snapshot save interval. |
| `WithRoot(root)` | `.` | Project root for OpenAPI contract enumeration (501 stubs for unimplemented endpoints). |

Environment variable fallbacks (used when the corresponding option is not set):

| Env var | Maps to |
|---|---|
| `GONACOS_REDIS_ADDR` | `WithRedisAddr` |
| `GONACOS_DATA_DIR` | `WithDataDir` |
| `GONACOS_SNAPSHOT_INTERVAL` | `WithSnapshotInterval` |

## Project layout

```text
gonacos/
  cmd/gonacos/          server binary entry point
  cmd/gonacos-contract/ contract manifest generator/verifier
  pkg/server/           embeddable server (New, Start, Shutdown, Services)
  pkg/app/              HTTP handler and gRPC adapter assembly
  pkg/config/           configuration service
  pkg/naming/           service discovery and instance health
  pkg/namespace/        namespace service
  pkg/auth/             users, roles, permissions, tokens
  pkg/cluster/          membership and Redis pub/sub sync
  pkg/store/            snapshot coordinator, Redis persistence, embedded Redis
  pkg/ai/               AI registry (prompts, skills, agentspecs, MCP, A2A)
  pkg/protocol/         v3 HTTP result envelope
  pkg/protocol/grpc/    v3 gRPC codec, server, dispatcher, push
  pkg/contract/         OpenAPI/proto contract manifest tooling
  pkg/observability/    metrics registry
  pkg/web/              console UI static assets
  api/openapi/          pinned upstream OpenAPI specs + generated manifest
  api/proto/            pinned upstream gRPC service proto
  docs/                 design and acceptance documents
  test/                 cluster, sdkcompat, and playwright integration tests
```

## Module

- Module path: `github.com/godeps/gonacos`
- Go version: 1.26+

## Documentation

- [Technical Design](docs/technical-design.md)
- [Test and Acceptance Plan](docs/test-acceptance-plan.md)
- [Operations Guide](docs/operations.md)
- [Cluster Design](docs/cluster-design.md)
- [中文技术方案](docs/技术方案.md)
- [中文测试验收方案](docs/测试验收方案.md)

## Compatibility

- Pinned to Nacos v3.2.2 OpenAPI (`api/openapi/upstream/*.zh.json`) and gRPC
  proto (`api/proto/nacos_grpc_service.proto`).
- The upstream Go SDK `github.com/nacos-group/nacos-sdk-go/v2` works as a
  client. See `test/sdkcompat` for the compatibility suite.

## License

MIT (placeholder — confirm before publishing).
