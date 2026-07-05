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
| `WithAddr(addr)` | `:8848` | HTTP listen address. Use `:0` to let the kernel pick a free port; `HTTPAddr()` reports the bound port. |
| `WithGRPCAddr(addr)` | derived (`HTTP+1000`) | gRPC listen address. Use `:0` to let the kernel pick a free port; `GRPCAddr()` reports the bound port. |
| `WithRedisAddr(addr)` | `""` (embedded) | Redis address. Empty = embedded miniredis (standalone). Non-empty = external Redis + cluster sync. |
| `WithDataDir(dir)` | `<root>/.gonacos/data` | Directory for the embedded Redis disk dump. Ignored when `WithRedisAddr` is set. |
| `WithSnapshotInterval(d)` | `30s` | Periodic snapshot save interval. |
| `WithRoot(root)` | `.` | Project root for OpenAPI contract enumeration (501 stubs for unimplemented endpoints). |
| `WithAuthSecret(secret)` | random per-process | HMAC-SHA256 token signing secret. **Set this** when running multiple nodes that must verify each other's tokens. |
| `WithTLS(certFile, keyFile)` | `""` (plaintext) | PEM-encoded cert + key for TLS on both HTTP and gRPC. gRPC negotiates HTTP/2 via ALPN. |
| `WithLogger(l)` | stderr via `log` | Plug in a structured logger (zap, zerolog, slog) by wrapping it to match the `Logger` interface. |
| `WithStrictSnapshot(bool)` | `false` | When `true`, `New` returns an error if the snapshot fails to load instead of starting with empty state. |
| `WithHTTPRateLimit(rps, burst)` | `0` (disabled) | Per-client-IP token bucket rate limit on HTTP. Honors `X-Forwarded-For` for layer-7 proxy deployments. Recommended production: `100, 200`. |
| `WithHTTPMaxBody(bytes)` | `10485760` (10 MiB) | Maximum HTTP request body size. Oversized bodies return 413. Pass `-1` to disable (not recommended). |
| `WithHTTPWriteTimeout(d)` | `30s` | Maximum HTTP response write duration. Pass `-1` to disable. |
| `WithHTTPIdleTimeout(d)` | `120s` | Maximum idle (keep-alive) duration for HTTP connections. Pass `-1` to disable. |
| `WithHTTPVerboseLog(bool)` | `false` | When `true`, log every HTTP request including health/metrics probes. When `false`, noisy paths are excluded. |
| `WithLoginThrottle(maxFailures, failWindow, lockoutDuration)` | `0` (disabled) | Per-(client-IP, username) brute-force lockout on `/v3/auth/user/login`. Recommended production: `5, 5m, 15m`. |
| `WithSnapshotBackupCount(n)` | `0` (disabled) | Retain the prior N disk-dump snapshots as `snapshot.1.json`, `snapshot.2.json`, ... so a corrupted latest snapshot can be recovered. Recommended: `5`. |

Environment variable fallbacks (used when the corresponding option is not set):

| Env var | Maps to |
|---|---|
| `GONACOS_REDIS_ADDR` | `WithRedisAddr` |
| `GONACOS_DATA_DIR` | `WithDataDir` |
| `GONACOS_SNAPSHOT_INTERVAL` | `WithSnapshotInterval` |
| `GONACOS_AUTH_SECRET` | `WithAuthSecret` |
| `GONACOS_TLS_CERT_FILE` + `GONACOS_TLS_KEY_FILE` | `WithTLS` |
| `GONACOS_STRICT_SNAPSHOT` | `WithStrictSnapshot` (`1`/`true`/`yes` to enable) |
| `GONACOS_HTTP_RATE_RPS` | `WithHTTPRateLimit` (burst defaults to 2x rps) |
| `GONACOS_HTTP_MAX_BODY` | `WithHTTPMaxBody` |
| `GONACOS_HTTP_WRITE_TIMEOUT` | `WithHTTPWriteTimeout` |
| `GONACOS_HTTP_IDLE_TIMEOUT` | `WithHTTPIdleTimeout` |

## Production hardening

gonacos ships with built-in protection for internet-facing deployments. None
of these are on by default in a way that would break existing embeddings —
configure them via options or env vars when running in production.

- **Per-IP rate limiting** (`WithHTTPRateLimit`): token-bucket limiter using
  `golang.org/x/time/rate`. Honors `X-Forwarded-For` so deployments behind a
  layer-7 proxy get per-client buckets. Idle buckets are reaped every 5
  minutes so a spoofed-IP attack can't grow the bucket map unbounded.
  Legitimate SDK traffic is low-volume per client, so a `100 rps / 200 burst`
  cap is generous.
- **Request body cap** (`WithHTTPMaxBody`, default 10 MiB): wraps the request
  body in `http.MaxBytesReader` so an oversized POST returns 413 instead of
  OOMing the server.
- **HTTP timeouts** (`WithHTTPWriteTimeout` 30s, `WithHTTPIdleTimeout` 120s):
  prevent slowloris-style attacks and reclaim idle keep-alive connections.
  `ReadHeaderTimeout` is hardcoded to 5s.
- **Readiness probe** (`GET /v3/console/health/readiness`,
  `GET /v3/admin/core/state/readiness`): pings the Redis client (external or
  embedded) and returns 503 when Redis is unreachable. Load balancers should
  gate traffic on this endpoint — a node that can't persist state should not
  receive writes. Liveness (`/liveness`) is unchanged: it returns 200 as
  long as the process is alive, regardless of dependency state.
- **Per-request access log**: one line per request with method, path,
  status, bytes, duration, and remote address. Health and metrics probes are
  excluded by default to keep the signal-to-noise ratio high;
  `WithHTTPVerboseLog(true)` opts into full logging.
- **Prometheus metrics** at `GET /metrics`: standard text exposition format
  suitable for the default `prometheus.yml` `metrics_path: /metrics`. Exposes
  Go runtime metrics (`process_*`), push-path counters
  (`gonacos_push_total{type=config|service}`), and subscription gauges
  (`gonacos_config_subscriptions`, `gonacos_service_subscriptions`). An
  admin-only mirror is also available at `GET /v3/admin/ops/metrics`.
- **Security response headers**: every response carries `nosniff`,
  `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy`, and `X-XSS-Protection: 0`.
  Under TLS, `Strict-Transport-Security` is added. Inner handlers can
  override any header per-route.
- **Request tracing** (`X-Request-Id`): every response carries a
  process-unique correlation ID that is also logged in the access log, so an
  operator can correlate a specific response to its log entry.
- **Login brute-force protection** (`WithLoginThrottle`): per-(client-IP,
  username) lockout after N consecutive failures within a window. Locked
  pairs receive 429 with `Retry-After` without calling the login handler.
- **Panic recovery** (HTTP + gRPC): a panicking handler produces a structured
  500 (HTTP) or gRPC INTERNAL status (code 13) carrying the request ID, plus
  a log line with the stack trace. The server stays up.
- **Atomic snapshot writes + backup rotation** (`WithSnapshotBackupCount`):
  disk dumps are written via temp-file-then-rename so a crash mid-write
  cannot corrupt the dump file. When `n > 0`, the prior N snapshots are
  retained as `snapshot.1.json`, `snapshot.2.json`, ... so a corrupted
  latest snapshot can be recovered from the previous one.

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
