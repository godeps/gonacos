package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/cluster"
	"github.com/godeps/gonacos/pkg/observability"
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
	logger        Logger
	bundle        *app.ServiceBundle
	coord         *store.Coordinator
	persist       *store.RedisPersistence
	push          *app.PushService
	redisSync     *cluster.RedisSync
	redisClient   *redis.Client
	embeddedRedis *store.EmbeddedRedis
	grpcSrv       *grpcsrv.Server
	httpSrv       *http.Server
	httpLn        net.Listener
	grpcLn        net.Listener
	stopPeriodic  func()
	stopRateGC    func()
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
	logger := o.resolveLogger()

	bundle := app.NewServiceBundleWithAuthSecret(o.resolveAuthSecret())
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
	persist.SetBackupCount(o.SnapshotBackupCount)
	if err := persist.Load(context.Background()); err != nil {
		if o.resolveStrictSnapshot() {
			_ = redisClient.Close()
			if embeddedRedis != nil {
				_ = embeddedRedis.Close()
			}
			return nil, fmt.Errorf("load snapshot (strict mode): %w", err)
		}
		logger.Warnf("load snapshot: %v (starting with empty state)", err)
	} else {
		logger.Infof("snapshot loaded")
	}

	push := app.NewPushService(grpcsrv.NewConnectionRegistry(), bundle.Config, bundle.Naming)
	registry := observability.NewRegistry()
	if push != nil {
		push.SetMetricsRegistry(registry)
		push.InstallCallbacks()
	}
	grpcSrv := app.SetupGRPCServerWithPush(bundle, push)
	// Forward gRPC panic recovery logs to the same logger the HTTP side
	// uses, so a single log stream covers both protocols.
	grpcSrv.Logf = func(format string, args ...any) {
		logger.Warnf(format, args...)
	}

	// Readiness checker: ping the Redis client (external or embedded).
	// Returns 503 when Redis is unreachable so load balancers stop sending
	// traffic to a node that can't persist state.
	readiness := app.ReadinessCheckerFunc(func(ctx context.Context) error {
		return redisClient.Ping(ctx).Err()
	})

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

	httpAddr := o.resolveAddr()
	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		_ = redisClient.Close()
		if embeddedRedis != nil {
			_ = embeddedRedis.Close()
		}
		if s := redisSync; s != nil {
			_ = s.Stop()
		}
		return nil, fmt.Errorf("http listen %q: %w", httpAddr, err)
	}
	grpcAddr := o.resolveGRPCAddr()
	grpcLn, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		_ = httpLn.Close()
		_ = redisClient.Close()
		if embeddedRedis != nil {
			_ = embeddedRedis.Close()
		}
		if s := redisSync; s != nil {
			_ = s.Stop()
		}
		return nil, fmt.Errorf("grpc listen %q: %w", grpcAddr, err)
	}

	httpHandler := app.NewHandlerWithServicesAndRegistry(o.resolveRoot(), bundle, coord, registry, readiness, o.buildLoginThrottle())

	// Recovery wraps the innermost handler so panics produce a 500 JSON
	// response with the request ID instead of crashing the connection.
	// Placed inside request ID so the deferred recover() can read the rid
	// from the context.
	httpHandler = newRecoveryMiddleware(logger, httpHandler)

	// Request ID must wrap recovery so the rid is available in the context
	// when recovery's deferred function runs, and so every response —
	// including 500s from panics — carries the X-Request-Id header.
	httpHandler = newRequestIDMiddleware(httpHandler)

	// Per-IP rate limiting. Disabled when rps <= 0. The background cleanup
	// goroutine reaps idle buckets so the map doesn't grow unbounded under
	// spoofed-IP attacks.
	var stopRateGC func()
	if rps := o.resolveHTTPRateRPS(); rps > 0 {
		rl := app.NewRateLimiter(rps, o.resolveHTTPRateBurst())
		stopRateGC = rl.StartCleanup(5*time.Minute, 10*time.Minute)
		httpHandler = app.NewRateLimitMiddleware(rl, httpHandler)
	}

	httpHandler = app.NewMaxBodyMiddleware(o.resolveHTTPMaxBody(), httpHandler)

	httpHandler = newRequestLogMiddleware(logger, o.HTTPVerboseLog, httpHandler)

	// Security headers outermost so every response — including 429/413/500
	// produced by the upper middlewares — carries nosniff, frame-options,
	// referrer-policy, and (under TLS) HSTS. The inner handler can still
	// override any header (e.g., set X-Frame-Options: DENY on a specific
	// route).
	certFile, keyFile := o.resolveTLS()
	httpHandler = app.NewSecurityHeadersMiddleware(certFile != "" && keyFile != "", httpHandler)

	writeTimeout := o.resolveHTTPWriteTimeout()
	idleTimeout := o.resolveHTTPIdleTimeout()
	httpSrv := &http.Server{
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if writeTimeout > 0 {
		httpSrv.WriteTimeout = writeTimeout
	}
	if idleTimeout > 0 {
		httpSrv.IdleTimeout = idleTimeout
	}

	return &Server{
		opts:          o,
		logger:        logger,
		bundle:        bundle,
		coord:         coord,
		persist:       persist,
		push:          push,
		redisSync:     redisSync,
		redisClient:   redisClient,
		embeddedRedis: embeddedRedis,
		grpcSrv:       grpcSrv,
		httpSrv:       httpSrv,
		httpLn:        httpLn,
		grpcLn:        grpcLn,
		stopPeriodic:  stopPeriodic,
		stopRateGC:    stopRateGC,
	}, nil
}

// Start launches the HTTP and gRPC servers and blocks until ctx is cancelled
// or one of the servers fails. On exit it calls [Server.Shutdown] to flush
// the snapshot and close resources. When [WithTLS] is set, both listeners
// serve TLS; otherwise both are plaintext (gRPC uses h2c).
//
// Listeners are pre-bound in [New], so [Server.HTTPAddr] and [Server.GRPCAddr]
// return the actual bound addresses (useful when binding to :0) even before
// Start returns.
func (s *Server) Start(ctx context.Context) error {
	certFile, keyFile := s.opts.resolveTLS()
	errc := make(chan error, 2)
	go func() {
		var err error
		if certFile != "" && keyFile != "" {
			err = s.grpcSrv.ServeTLS(s.grpcLn, certFile, keyFile)
		} else {
			err = s.grpcSrv.Serve(s.grpcLn)
		}
		if err != nil && err != http.ErrServerClosed {
			errc <- fmt.Errorf("grpc serve: %w", err)
			return
		}
		errc <- nil
	}()
	go func() {
		var err error
		if certFile != "" && keyFile != "" {
			err = s.httpSrv.ServeTLS(s.httpLn, certFile, keyFile)
		} else {
			err = s.httpSrv.Serve(s.httpLn)
		}
		if err != nil && err != http.ErrServerClosed {
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
			s.logger.Warnf("shutdown after serve error: %v", cerr)
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
	if s.stopRateGC != nil {
		s.stopRateGC()
	}
	if err := s.persist.Save(ctx); err != nil {
		s.logger.Warnf("save snapshot on shutdown: %v", err)
	}
	if s.redisSync != nil {
		_ = s.redisSync.Stop()
	}
	_ = s.redisClient.Close()
	if s.embeddedRedis != nil {
		_ = s.embeddedRedis.Close()
	}
	// Closing the listeners unblocks any Accept loops that haven't been
	// drained by http.Server.Shutdown / grpc.Server.Shutdown yet.
	if s.httpLn != nil {
		_ = s.httpLn.Close()
	}
	if s.grpcLn != nil {
		_ = s.grpcLn.Close()
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

// HTTPAddr returns the actual bound HTTP address. When the configured address
// uses :0, this returns the kernel-assigned port after [New] returns. Returns
// the configured address as a fallback when the listener is not yet bound.
func (s *Server) HTTPAddr() string {
	if s.httpLn != nil {
		return s.httpLn.Addr().String()
	}
	return s.opts.resolveAddr()
}

// GRPCAddr returns the actual bound gRPC address. When the configured address
// uses :0, this returns the kernel-assigned port after [New] returns. If not
// set explicitly via [WithGRPCAddr], the gRPC port is derived from the HTTP
// port + 1000 (Nacos convention: 8848 -> 9848).
func (s *Server) GRPCAddr() string {
	if s.grpcLn != nil {
		return s.grpcLn.Addr().String()
	}
	return s.opts.resolveGRPCAddr()
}
