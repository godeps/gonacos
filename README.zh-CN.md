# GoNacos

[English](README.md) | [中文](README.zh-CN.md)

GoNacos 是一个用 Go 实现的 Nacos v3 兼容服务端。它实现 Nacos v3 的 HTTP 和 gRPC 协议,因此官方的 `nacos-group/nacos-sdk-go` 客户端以及其他 v3 SDK 无需修改即可对接。你可以把它作为独立二进制运行,也可以作为库嵌入到其他 Go 程序中。

## 特性

- **v3 协议兼容**:HTTP(`/v3/admin`、`/v3/console`、`/v3/client`、`/v3/auth`)和 gRPC(`Request`、`RequestStream`、`BiRequestStream`)对齐 Nacos v3.2.2。
- **配置服务**:发布/查询/删除/列表、批量监听、历史、克隆、导入/导出、Beta/Gray 发布。
- **命名服务**:实例注册/注销、服务列表/发现、订阅推送、健康检查、临时实例租约。
- **鉴权**:用户、角色、权限,HMAC token 登录,RBAC 授权。
- **命名空间**:CRUD,默认内置 `public` 命名空间。
- **集群**:standalone(内嵌 miniredis)或基于 Redis pub/sub 的多节点同步。
- **AI 注册中心**:prompts、skills、agent specs、MCP servers、A2A agents(Nacos AI 扩展)。
- **持久化**:全服务快照/恢复到单一 envelope;周期性保存到 Redis 或磁盘。
- **可嵌入**:`import "github.com/godeps/gonacos/pkg/server"` 即可在自己进程内跑一个 Nacos 兼容服务。

## 安装

作为库:

```sh
go get github.com/godeps/gonacos@latest
```

作为二进制:

```sh
git clone https://github.com/godeps/gonacos
cd gonacos
make build
```

## 快速开始(二进制)

```sh
make build
./gonacos serve :8848
```

健康检查:

```sh
curl http://localhost:8848/v3/console/health/liveness
# {"code":0,"message":"success","data":"ok"}
```

通过 curl 或上游 Go SDK 发布/查询配置:

```sh
curl 'http://localhost:8848/v3/admin/cs/config' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"dataId":"app.yml","groupName":"DEFAULT_GROUP","content":"key: value","type":"yaml"}'
curl 'http://localhost:8848/v3/client/cs/config?dataId=app.yml&groupName=DEFAULT_GROUP'
```

## 嵌入到你的程序

`import "github.com/godeps/gonacos/pkg/server"`,构造 `*server.Server`:

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
		server.WithRoot("."), // 包含 api/openapi/upstream/ 的目录,用于 501 stub
	)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := srv.Start(context.Background()); err != nil {
			log.Printf("serve: %v", err)
		}
	}()

	// 三种使用模式:

	// 1. 进程内启 HTTP/gRPC:任何 Nacos v3 SDK 都能访问
	//    http://localhost:8848 和 gRPC localhost:9848。

	// 2. 直接调用 Service 方法(不走网络):
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

	// 3. 快照/恢复(备份):
	env, _ := srv.Snapshot()
	_ = env // 序列化为 JSON,写入磁盘等

	// 优雅关闭会刷盘快照并关闭资源:
	// _ = srv.Shutdown(ctx)
}
```

## 配置

选项(`server.With*`):

| 选项 | 默认值 | 说明 |
|---|---|---|
| `WithAddr(addr)` | `:8848` | HTTP 监听地址。用 `:0` 让内核选空闲端口,`HTTPAddr()` 返回实际端口。 |
| `WithGRPCAddr(addr)` | 派生(`HTTP+1000`) | gRPC 监听地址。用 `:0` 让内核选空闲端口,`GRPCAddr()` 返回实际端口。 |
| `WithRedisAddr(addr)` | `""`(内嵌) | Redis 地址。空 = 内嵌 miniredis(standalone)。非空 = 外部 Redis + 集群同步。 |
| `WithDataDir(dir)` | `<root>/.gonacos/data` | 内嵌 Redis 磁盘 dump 目录。`WithRedisAddr` 设置时忽略。 |
| `WithSnapshotInterval(d)` | `30s` | 周期性快照保存间隔。 |
| `WithRoot(root)` | `.` | 项目根目录,用于 OpenAPI 契约枚举(为未实现端点注册 501 stub)。 |
| `WithAuthSecret(secret)` | 每进程随机 | HMAC-SHA256 token 签名密钥。多节点集群必须设置相同密钥,才能互相校验 token。 |
| `WithTLS(certFile, keyFile)` | `""`(明文) | PEM 编码的证书 + 私钥,HTTP 和 gRPC 同时启用 TLS。gRPC 通过 ALPN 协商 HTTP/2。 |
| `WithLogger(l)` | stderr 经 `log` 输出 | 注入结构化日志(zap、zerolog、slog),包装成 `Logger` 接口即可。 |
| `WithStrictSnapshot(bool)` | `false` | 为 `true` 时,快照加载失败会让 `New` 返回错误,而不是以空状态启动。 |

环境变量 fallback(未设置对应选项时使用):

| 环境变量 | 对应选项 |
|---|---|
| `GONACOS_REDIS_ADDR` | `WithRedisAddr` |
| `GONACOS_DATA_DIR` | `WithDataDir` |
| `GONACOS_SNAPSHOT_INTERVAL` | `WithSnapshotInterval` |
| `GONACOS_AUTH_SECRET` | `WithAuthSecret` |
| `GONACOS_TLS_CERT_FILE` + `GONACOS_TLS_KEY_FILE` | `WithTLS` |
| `GONACOS_STRICT_SNAPSHOT` | `WithStrictSnapshot`(`1`/`true`/`yes` 启用) |

## 项目布局

```text
gonacos/
  cmd/gonacos/          服务端二进制入口
  cmd/gonacos-contract/ 契约 manifest 生成/校验工具
  pkg/server/           可嵌入服务(New、Start、Shutdown、Services)
  pkg/app/              HTTP handler 与 gRPC adapter 组装
  pkg/config/           配置服务
  pkg/naming/           服务发现与实例健康
  pkg/namespace/        命名空间服务
  pkg/auth/             用户、角色、权限、token
  pkg/cluster/          成员管理与 Redis pub/sub 同步
  pkg/store/            快照协调器、Redis 持久化、内嵌 Redis
  pkg/ai/               AI 注册中心(prompts、skills、agentspecs、MCP、A2A)
  pkg/protocol/         v3 HTTP result envelope
  pkg/protocol/grpc/    v3 gRPC codec、server、dispatcher、push
  pkg/contract/         OpenAPI/proto 契约 manifest 工具
  pkg/observability/    metrics 注册表
  pkg/web/              控制台 UI 静态资源
  api/openapi/          锁定的上游 OpenAPI spec + 生成的 manifest
  api/proto/            锁定的上游 gRPC service proto
  docs/                 设计与验收文档
  test/                 cluster、sdkcompat、playwright 集成测试
```

## Module

- Module 路径:`github.com/godeps/gonacos`
- Go 版本:1.26+

## 文档

- [Technical Design](docs/technical-design.md)
- [Test and Acceptance Plan](docs/test-acceptance-plan.md)
- [Operations Guide](docs/operations.md)
- [Cluster Design](docs/cluster-design.md)
- [中文技术方案](docs/技术方案.md)
- [中文测试验收方案](docs/测试验收方案.md)

## 兼容性

- 锁定 Nacos v3.2.2 OpenAPI(`api/openapi/upstream/*.zh.json`)和 gRPC proto(`api/proto/nacos_grpc_service.proto`)。
- 上游 Go SDK `github.com/nacos-group/nacos-sdk-go/v2` 可作为客户端。兼容性套件见 `test/sdkcompat`。

## License

MIT(占位 — 发布前确认)。
