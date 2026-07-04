package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	aivsvc "github.com/saker-ai/gonacos/internal/ai"
	authsvc "github.com/saker-ai/gonacos/internal/auth"
	clustersvc "github.com/saker-ai/gonacos/internal/cluster"
	configsvc "github.com/saker-ai/gonacos/internal/config"
	"github.com/saker-ai/gonacos/internal/contract"
	"github.com/saker-ai/gonacos/internal/namespace"
	namingsvc "github.com/saker-ai/gonacos/internal/naming"
	"github.com/saker-ai/gonacos/internal/observability"
	"github.com/saker-ai/gonacos/internal/protocol"
	grpcsrv "github.com/saker-ai/gonacos/internal/protocol/grpc"
	"github.com/saker-ai/gonacos/internal/store"
	"github.com/saker-ai/gonacos/internal/web"
	"github.com/redis/go-redis/v9"
)

const Version = "0.1.0-dev"

func Run(ctx context.Context, args []string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start app: %w", err)
	}

	if len(args) > 0 && args[0] == "version" {
		fmt.Println(Version)
		return nil
	}
	if len(args) > 0 && args[0] == "serve" {
		addr := ":8848"
		if len(args) > 1 {
			addr = args[1]
		}
		// Trap SIGINT/SIGTERM so Serve can flush the snapshot on shutdown.
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return Serve(ctx, addr, ".")
	}

	fmt.Println("usage: gonacos version | gonacos serve [addr]")
	return nil
}

func Serve(ctx context.Context, addr, root string) error {
	// Build shared service instances so HTTP and gRPC see the same state.
	redisAddr := redisAddrFromEnv()
	services := newServices()

	// Coordinator is shared between HTTP backup/restore endpoints and the
	// Redis persistence layer so a single Save/Load touches all services.
	coordinator := store.NewCoordinator()
	coordinator.Register(services.namespace)
	coordinator.Register(services.config)
	coordinator.Register(services.naming)
	coordinator.Register(services.auth)
	coordinator.Register(services.ai)
	coordinator.Register(services.cluster)

	// Determine Redis backend: external (GONACOS_REDIS_ADDR) or embedded
	// miniredis (standalone, no external dependency).
	var embeddedRedis *store.EmbeddedRedis
	var redisClient *redis.Client
	dumpPath := "" // disk mirror for embedded Redis persistence
	if redisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	} else {
		embeddedRedis, err := store.StartEmbedded()
		if err != nil {
			return fmt.Errorf("start embedded redis: %w", err)
		}
		redisAddr = embeddedRedis.Addr()
		redisClient = embeddedRedis.Client()
		dumpPath = filepath.Join(dataDirFromEnv(root), "snapshot.json")
		fmt.Printf("embedded redis started at %s (dump: %s)\n", redisAddr, dumpPath)
	}

	// Persistence: load state from Redis (or disk dump for embedded) before
	// starting sync so local state is populated before any remote event.
	persistence := store.NewRedisPersistence(redisClient, coordinator, dumpPath)
	if err := persistence.Load(ctx); err != nil {
		fmt.Printf("warn: load snapshot: %v\n", err)
	} else {
		fmt.Println("snapshot loaded")
	}

	push := NewPushService(grpcsrv.NewConnectionRegistry(), services.config, services.naming)
	push.InstallCallbacks()
	grpcSrv := setupGRPCServerWithPush(services, push)
	grpcAddr := grpcAddrFor(addr)

	// If external Redis is configured, wire up cross-node sync. In embedded
	// (standalone) mode there are no other nodes to sync with.
	var redisSync *clustersvc.RedisSync
	if embeddedRedis == nil {
		host, port := splitHostPort(addr)
		grpcP, _ := strconv.Atoi(port)
		member := clustersvc.Member{
			ID:       clustersvc.DeriveMemberID(host, port),
			IP:       host,
			Port:     grpcP,
			APIPort:  grpcP,
			GRPCPort: grpcP + 1000,
			State:    "UP",
			IsSelf:   true,
		}
		services.cluster.SetMode(clustersvc.ModeRedis)
		rs, err := setupRedisSync(redisClient, member.ID, member, services.config, services.naming)
		if err != nil {
			_ = redisClient.Close()
			if embeddedRedis != nil {
				_ = embeddedRedis.Close()
			}
			return fmt.Errorf("redis cluster: %w", err)
		}
		redisSync = rs
	}

	// Periodic snapshot save (default 30s).
	snapshotInterval := snapshotIntervalFromEnv()
	stopPeriodic := persistence.StartPeriodic(ctx, snapshotInterval)

	go func() {
		if err := grpcSrv.ListenAndServe(grpcAddr); err != nil && err != http.ErrServerClosed {
			fmt.Printf("grpc serve: %v\n", err)
		}
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           NewHandlerWithServicesWithCoordinator(root, services, coordinator),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
			return
		}
		errc <- nil
	}()

	shutdown := func() error {
		stopPeriodic()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := persistence.Save(shutdownCtx); err != nil {
			fmt.Printf("warn: save snapshot on shutdown: %v\n", err)
		}
		if redisSync != nil {
			_ = redisSync.Stop()
		}
		_ = redisClient.Close()
		if embeddedRedis != nil {
			_ = embeddedRedis.Close()
		}
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		if err := grpcSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown grpc server: %w", err)
		}
		return nil
	}

	select {
	case <-ctx.Done():
		if err := shutdown(); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errc:
		if err != nil {
			if cerr := shutdown(); cerr != nil {
				fmt.Printf("warn: shutdown after serve error: %v\n", cerr)
			}
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	}
}

// dataDirFromEnv returns the data directory for persistent files. Defaults to
// <root>/.gonacos/data. Overridden by GONACOS_DATA_DIR env var.
func dataDirFromEnv(root string) string {
	if v := os.Getenv("GONACOS_DATA_DIR"); v != "" {
		return v
	}
	return filepath.Join(root, ".gonacos", "data")
}

// snapshotIntervalFromEnv returns the periodic snapshot interval. Defaults to
// 30s. Overridden by GONACOS_SNAPSHOT_INTERVAL env var (Go duration syntax).
func snapshotIntervalFromEnv() time.Duration {
	if v := os.Getenv("GONACOS_SNAPSHOT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// splitHostPort splits an address into host and port strings. Returns
// "127.0.0.1" and "8848" for ":8848".
func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", "8848"
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port
}

// grpcAddrFor returns the gRPC listen address. By convention Nacos uses the
// HTTP port + 1000 for gRPC (e.g. 8848 -> 9848).
func grpcAddrFor(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ":9848"
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return ":9848"
	}
	if host == "" {
		return fmt.Sprintf(":%d", p+1000)
	}
	return fmt.Sprintf("%s:%d", host, p+1000)
}

// serviceBundle groups the shared service instances used by both the HTTP
// handler and the gRPC server so they see the same in-memory state.
type serviceBundle struct {
	namespace *namespace.Service
	config    *configsvc.Service
	naming    *namingsvc.Service
	auth      *authsvc.Service
	ai        *aivsvc.Service
	cluster   *clustersvc.Service
}

func newServices() *serviceBundle {
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	authSvc := authsvc.NewService()
	aiSvc := aivsvc.NewService(nil)
	clusterSvc := clustersvc.NewService(clustersvc.ModeStandalone, "", 0, 0, 0)
	return &serviceBundle{
		namespace: namespace.NewService(),
		config:    configSvc,
		naming:    namingSvc,
		auth:      authSvc,
		ai:        aiSvc,
		cluster:   clusterSvc,
	}
}

// setupGRPCServer builds the gRPC server with the standard dispatchers.
func setupGRPCServer(services *serviceBundle) *grpcsrv.Server {
	return setupGRPCServerWithPush(services, nil)
}

// setupGRPCServerWithPush builds the gRPC server with push support. When
// push is non-nil, the BiRequestStream handler registers connections so
// the server can push ConfigChangeNotify and NotifySubscriber frames to
// subscribed SDK clients.
func setupGRPCServerWithPush(services *serviceBundle, push *PushService) *grpcsrv.Server {
	var registry *grpcsrv.ConnectionRegistry
	if push != nil {
		registry = push.ConnectionRegistry()
	}
	return grpcsrv.SetupDefaultServerWithRegistry(
		namingGRPCAdapter{service: services.naming, push: push},
		configGRPCAdapter{service: services.config, push: push},
		aiGRPCAdapter{service: services.ai},
		registry,
	)
}

// NewHandler builds the HTTP handler with fresh service instances. Kept for
// tests and standalone runs that don't need a shared gRPC/HTTP bundle.
func NewHandler(root string) http.Handler {
	return NewHandlerWithServices(root, nil)
}

// NewHandlerWithServices builds the HTTP handler using the provided service
// bundle. If services is nil, fresh instances are created (and the config
// bridge is wired so the gRPC adapter can reach them).
func NewHandlerWithServices(root string, services *serviceBundle) http.Handler {
	return NewHandlerWithServicesWithCoordinator(root, services, nil)
}

// NewHandlerWithServicesWithCoordinator is like NewHandlerWithServices but
// accepts a shared *store.Coordinator. When coord is nil, a fresh coordinator
// is built and the services are registered into it. When coord is non-nil
// (passed from Serve, which also uses it for startup restore and periodic
// save), the services are (re)registered into it so the HTTP backup/restore
// endpoints and the persistence layer share the same coordinator.
func NewHandlerWithServicesWithCoordinator(root string, services *serviceBundle, coord *store.Coordinator) http.Handler {
	if services == nil {
		services = newServices()
	}
	if coord == nil {
		coord = store.NewCoordinator()
	}

	mux := http.NewServeMux()
	routes := map[string]struct{}{}

	register := func(method, path string, handler http.HandlerFunc) {
		for _, routePath := range []string{path, "/nacos" + path} {
			pattern := method + " " + routePath
			if _, ok := routes[pattern]; ok {
				continue
			}
			routes[pattern] = struct{}{}
			mux.HandleFunc(pattern, handler)
		}
	}

	register("GET", "/v3/console/health/liveness", okHandler("ok"))
	register("GET", "/v3/console/health/readiness", okHandler("ok"))
	register("GET", "/v3/admin/core/state/liveness", okHandler("ok"))
	register("GET", "/v3/admin/core/state/readiness", okHandler("ok"))
	register("GET", "/v3/admin/core/state", stateHandler)
	register("GET", "/v3/console/server/state", stateHandler)
	register("GET", "/v3/console/server/announcement", okHandler(""))
	register("GET", "/v3/console/server/guide", okHandler(""))

	namespaceSvc := services.namespace
	configSvc := services.config
	namingSvc := services.naming
	authSvc := services.auth
	aiSvc := services.ai
	clusterSvc := services.cluster

	coord.Register(namespaceSvc)
	coord.Register(configSvc)
	coord.Register(namingSvc)
	coord.Register(authSvc)
	coord.Register(aiSvc)
	coord.Register(clusterSvc)

	registry := observability.NewRegistry()

	registerNamespaceRoutes(register, namespaceSvc)
	registerConfigRoutes(register, configSvc)
	registerNamingRoutes(register, namingSvc)
	registerAuthRoutes(register, authSvc)
	registerAIRoutes(register, aiSvc)
	registerClusterRoutes(register, clusterSvc)
	registerOpsRoutes(register, coord, registry)
	registerStubRoutes(register, configSvc, namingSvc, aiSvc, clusterSvc)

	mux.HandleFunc("/v3/console/ui", web.ConsoleHandler())
	mux.HandleFunc("/v3/console/ui/", web.ConsoleHandler())

	manifest, err := contract.Build(root)
	if err == nil {
		for _, surface := range manifest.OpenAPI {
			for _, operation := range surface.Operations {
				op := operation
				register(op.Method, op.Path, func(w http.ResponseWriter, r *http.Request) {
					protocol.WriteError(w, http.StatusNotImplemented, protocol.Error{
						Code:    protocol.CodeNotImplemented,
						Message: "operation not implemented",
						Data: map[string]string{
							"method":      op.Method,
							"path":        op.Path,
							"operationId": op.OperationID,
						},
					})
				})
			}
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteError(w, http.StatusNotFound, protocol.Error{
			Code:    protocol.CodeNotFound,
			Message: "resource not found",
		})
	})

	return newAuthMiddleware(authSvc, mux)
}

func okHandler(data string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteResult(w, http.StatusOK, data)
	}
}

func stateHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UnixMilli()
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"standalone_mode":       "true",
		"function_mode":         "All",
		"version":               Version,
		"startup_mode":          "standalone",
		"server_port":           requestPort(r),
		"last_refresh_time":     strconv.FormatInt(now, 10),
		"last_refresh_time_str": time.UnixMilli(now).Format(time.RFC3339),
	})
}

func requestPort(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err == nil && port != "" {
		return port
	}
	return "8848"
}
