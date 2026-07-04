package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/cluster"
	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
	"github.com/godeps/gonacos/pkg/store"
	"github.com/redis/go-redis/v9"
)

// Server is an embeddable gonacos instance. Construct with [New] and run
// with [Server.Start]. External programs embed gonacos to get a Nacos v3
// compatible service (HTTP + gRPC) inside their own process.
//
// Three usage modes are supported:
//
//  1. HTTP/gRPC in-process: call [Server.Start] and talk to it over localhost.
//  2. Direct service call: use [Server.Services] to call config/naming/auth
//     methods without a network hop.
//  3. Storage/snapshot access: use [Server.Coordinator], [Server.Snapshot],
//     [Server.RedisClient] to integrate with backup/restore pipelines.
type Server struct {
	opts          options
	bundle        *app.ServiceBundle
	coord         *store.Coordinator
	persist       *store.RedisPersistence
	push          *app.PushService
	redisSync     *cluster.RedisSync
	redisClient   *redis.Client
	embeddedRedis *store.EmbeddedRedis
	grpcSrv       *grpcsrv.Server
	httpSrv       *http.Server
	stopPeriodic  func()
}

// New builds a Server with the given options. It constructs the service
// bundle, wires Redis (external or embedded), loads the snapshot, builds
// the gRPC server, and (in external Redis mode) starts cluster sync. It
// does not start listening; call [Server.Start] to serve.
//
// Options left zero fall back to env vars (GONACOS_REDIS_ADDR,
// GONACOS_DATA_DIR, GONACOS_SNAPSHOT_INTERVAL) and then to defaults.
func New(opts ...Option) (*Server, error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	bundle := app.NewServiceBundle()
	coord := store.NewCoordinator()
	coord.Register(bundle.Namespace)
	coord.Register(bundle.Config)
	coord.Register(bundle.Naming)
	coord.Register(bundle.Auth)
	coord.Register(bundle.AI)
	coord.Register(bundle.Cluster)

	redisAddr := o.resolveRedisAddr()
	var embeddedRedis *store.EmbeddedRedis
	var redisClient *redis.Client
	dumpPath := ""
	if redisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	} else {
		er, err := store.StartEmbedded()
		if err != nil {
			return nil, fmt.Errorf("start embedded redis: %w", err)
		}
		embeddedRedis = er
		dumpPath = filepath.Join(o.resolveDataDir(), "snapshot.json")
		redisClient = embeddedRedis.Client()
	}

	persist := store.NewRedisPersistence(redisClient, coord, dumpPath)
	if err := persist.Load(context.Background()); err != nil {
		// Non-fatal: start with empty state.
		fmt.Printf("warn: load snapshot: %v\n", err)
	} else {
		fmt.Println("snapshot loaded")
	}

	push := app.NewPushService(grpcsrv.NewConnectionRegistry(), bundle.Config, bundle.Naming)
	if push != nil {
		push.InstallCallbacks()
	}
	grpcSrv := app.SetupGRPCServerWithPush(bundle, push)

	var redisSync *cluster.RedisSync
	if embeddedRedis == nil {
		host, port := splitHostPort(o.resolveAddr())
		grpcP, _ := strconv.Atoi(port)
		member := cluster.Member{
			ID:       cluster.DeriveMemberID(host, port),
			IP:       host,
			Port:     grpcP,
			APIPort:  grpcP,
			GRPCPort: grpcP + 1000,
			State:    "UP",
			IsSelf:   true,
		}
		bundle.Cluster.SetMode(cluster.ModeRedis)
		rs, err := app.SetupRedisSync(redisClient, member.ID, member, bundle.Config, bundle.Naming)
		if err != nil {
			_ = redisClient.Close()
			if embeddedRedis != nil {
				_ = embeddedRedis.Close()
			}
			return nil, fmt.Errorf("redis cluster: %w", err)
		}
		redisSync = rs
	}

	stopPeriodic := persist.StartPeriodic(context.Background(), o.resolveSnapshotInterval())

	httpSrv := &http.Server{
		Addr:              o.resolveAddr(),
		Handler:           app.NewHandlerWithServicesWithCoordinator(o.resolveRoot(), bundle, coord),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &Server{
		opts:          o,
		bundle:        bundle,
		coord:         coord,
		persist:       persist,
		push:          push,
		redisSync:     redisSync,
		redisClient:   redisClient,
		embeddedRedis: embeddedRedis,
		grpcSrv:       grpcSrv,
		httpSrv:       httpSrv,
		stopPeriodic:  stopPeriodic,
	}, nil
}

// Start launches the HTTP and gRPC servers and blocks until ctx is cancelled
// or one of the servers fails. On exit it calls [Server.Shutdown] to flush
// the snapshot and close resources.
func (s *Server) Start(ctx context.Context) error {
	errc := make(chan error, 2)
	go func() {
		if err := s.grpcSrv.ListenAndServe(s.opts.resolveGRPCAddr()); err != nil && err != http.ErrServerClosed {
			errc <- fmt.Errorf("grpc serve: %w", err)
			return
		}
		errc <- nil
	}()
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- fmt.Errorf("http serve: %w", err)
			return
		}
		errc <- nil
	}()

	select {
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	case err := <-errc:
		if cerr := s.Shutdown(context.Background()); cerr != nil {
			fmt.Printf("warn: shutdown after serve error: %v\n", cerr)
		}
		return err
	}
}

// Shutdown flushes the snapshot, stops cluster sync, and closes the HTTP
// and gRPC servers. Safe to call once; subsequent calls are no-ops on the
// HTTP/gRPC layers (their Shutdown handles double-call gracefully).
func (s *Server) Shutdown(ctx context.Context) error {
	if s.stopPeriodic != nil {
		s.stopPeriodic()
	}
	if err := s.persist.Save(ctx); err != nil {
		fmt.Printf("warn: save snapshot on shutdown: %v\n", err)
	}
	if s.redisSync != nil {
		_ = s.redisSync.Stop()
	}
	_ = s.redisClient.Close()
	if s.embeddedRedis != nil {
		_ = s.embeddedRedis.Close()
	}
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	if err := s.grpcSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown grpc server: %w", err)
	}
	return nil
}

// Services returns the service bundle backing the server. Use it to call
// config/naming/auth/namespace/ai/cluster methods directly without a network
// hop.
func (s *Server) Services() *app.ServiceBundle { return s.bundle }

// Coordinator returns the shared snapshot coordinator. Register additional
// Snapshotters here to include them in Save/Load.
func (s *Server) Coordinator() *store.Coordinator { return s.coord }

// RedisClient returns the Redis client in use (external or embedded). Nil
// before [New] returns; always non-nil after.
func (s *Server) RedisClient() *redis.Client { return s.redisClient }

// Snapshot captures a backup envelope of all registered snapshotters.
func (s *Server) Snapshot() (*store.Envelope, error) {
	return s.coord.Snapshot()
}

// Restore replaces in-memory state from the given envelope.
func (s *Server) Restore(env *store.Envelope) error {
	return s.coord.Restore(env)
}

// HTTPAddr returns the configured HTTP listen address.
func (s *Server) HTTPAddr() string { return s.opts.resolveAddr() }

// GRPCAddr returns the configured gRPC listen address. If not set explicitly
// via [WithGRPCAddr], it is derived from the HTTP port + 1000.
func (s *Server) GRPCAddr() string { return s.opts.resolveGRPCAddr() }
