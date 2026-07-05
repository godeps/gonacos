package server

import (
	"context"
	"crypto/tls"
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
	"github.com/godeps/gonacos/pkg/version"
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
	tlsConfig     *tls.Config // non-nil when TLS is enabled; carries GetCertificate for hot reload
	stopPeriodic  func()
	stopRateGC    func()
	stopResource  func()
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
	// Wire the server logger as the audit sink so security-relevant
	// events (login, user/namespace/config CRUD, backup/restore) land in
	// the same log stream as access logs. When an audit log file is
	// configured, events also go to that file as JSON-lines for
	// compliance archival and SIEM forwarding. A nil logger disables
	// auditing (matching noopAuditLogger behavior).
	bundle.AuditLogger = buildAuditLogger(logger, o.resolveAuditLogFile())
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
		redisClient = redis.NewClient(&redis.Options{
			Addr:            redisAddr,
			PoolSize:        o.resolveRedisPoolSize(),
			MinIdleConns:    o.resolveRedisMinIdleConns(),
			DialTimeout:     o.resolveRedisDialTimeout(),
			ReadTimeout:     o.resolveRedisReadTimeout(),
			WriteTimeout:    o.resolveRedisWriteTimeout(),
			PoolTimeout:     o.resolveRedisPoolTimeout(),
			ConnMaxLifetime: o.resolveRedisMaxConnAge(),
		})
	} else {
		er, err := store.StartEmbedded()
		if err != nil {
			return nil, fmt.Errorf("start embedded redis: %w", err)
		}
		embeddedRedis = er
		dumpPath = filepath.Join(o.resolveDataDir(), "snapshot.json")
		redisClient = embeddedRedis.Client()
	}

	// Wire the metrics hook before any Redis call. The hook is added to
	// both external and embedded clients — embedded mode still benefits
	// from per-command latency visibility (a slow command against the
	// in-memory store signals a hot path or a regression). The registry
	// is constructed below, so we add the hook after the registry is
	// available.
	registry := observability.NewRegistry()
	// Wire the audit-event counter before constructing the audit
	// logger so WrapWithMetrics can find the registry. Audit events
	// increment gonacos_audit_events_total{action,result} — the
	// alerting signal for "audit rate spiked" (brute-force login,
	// permission scan). Set before buildAuditLogger below so the
	// wrapper picks it up.
	app.SetAuditMetricsRegistry(registry)
	redisClient.AddHook(newRedisMetricsHook(registry))

	persist := store.NewRedisPersistence(redisClient, coord, dumpPath)
	persist.SetBackupCount(o.resolveSnapshotBackupCount())
	// Wire the snapshot HMAC key so the disk dump is authenticated. A
	// tampered dump (e.g., an attacker replacing the file to inject a
	// malicious admin account) is rejected at Load with
	// ErrSnapshotTampered. Empty key skips verification — backward
	// compatible with pre-HMAC dumps. Defaults to the auth secret so a
	// single secret secures both tokens and snapshots.
	persist.SetHMACKey([]byte(o.resolveSnapshotHMACKey()))
	// Wire snapshot metrics into the persistence layer. The metrics are
	// the data-loss signal: alert on gonacos_snapshot_saves_total{result=
	// "failure"} rate > 0, and on time() - gonacos_last_snapshot_save_
	// timestamp_seconds > 2*interval to catch a stuck periodic loop.
	persist.SetMetricsRegistry(registry)
	if err := persist.Load(context.Background()); err != nil {
		if o.resolveStrictSnapshot() {
			_ = redisClient.Close()
			if embeddedRedis != nil {
				_ = embeddedRedis.Close()
			}
			return nil, fmt.Errorf("load snapshot (strict mode): %w", err)
		}
		logger.Errorf("load snapshot: %v (starting with empty state)", err)
	} else {
		logger.Infof("snapshot loaded")
	}

	push := app.NewPushService(grpcsrv.NewConnectionRegistry(), bundle.Config, bundle.Naming)
	// Publish the binary's build identity as a Prometheus gauge so operators
	// can query `gonacos_build_info` to see which version/commit is deployed
	// across a fleet, alert on version drift, and verify rollouts landed.
	registry.RegisterBuildInfo(version.Version, version.Commit, version.BuildDate)
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
	// Cap the per-frame payload size so a malicious peer cannot drive the
	// process into OOM by claiming a 4 GiB body. Negative means unlimited
	// (operator opted out — not recommended in production).
	grpcSrv.MaxFrameBytes = o.resolveGRPCMaxFrameBytes()
	// HTTP/2 keepalive PINGs detect half-open connections (client crashed
	// without sending FIN) so the server doesn't hold a goroutine + fd
	// alive indefinitely. Disabled when ReadIdleTimeout <= 0.
	grpcSrv.KeepAlive = o.resolveGRPCKeepAlive()
	// Wire the same metrics registry into the gRPC server so
	// gonacos_grpc_requests_total is exposed under /metrics alongside the
	// HTTP and process metrics. A single scrape captures everything.
	grpcSrv.MetricsRegistry = &grpcMetricsAdapter{r: registry}

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
		// stopPeriodic launched above; without this call the snapshot
		// goroutine survives [New] failure and outlives the test or
		// embedder that called [New].
		stopPeriodic()
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
		stopPeriodic()
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

	// Cap concurrent connections so a connection-flood attack cannot
	// exhaust the process's file descriptor limit. The cap is shared
	// across HTTP and gRPC because both listeners feed into the same
	// process — a flood on either protocol can take down both.
	maxConns := o.resolveMaxConns()
	if maxConns > 0 {
		httpLn = newMaxConnsListener(httpLn, maxConns)
		grpcLn = newMaxConnsListener(grpcLn, maxConns)
	}

	httpHandler := app.NewHandlerWithServicesAndRegistry(o.resolveRoot(), bundle, coord, registry, readiness, o.buildLoginThrottle(), o.resolveMetricsToken())

	// Recovery wraps the innermost handler so panics produce a 500 JSON
	// response with the request ID instead of crashing the connection.
	// Wired with the metrics registry so a recovered panic increments
	// gonacos_http_panics_total — the alerting signal that a handler is
	// crashing. A non-zero rate pages on-call (deployed bug or malformed
	// request the handler can't process).
	httpHandler = newRecoveryMiddlewareWithRegistry(logger, httpHandler, registry)

	// Per-IP rate limiting. Disabled when rps <= 0. The background cleanup
	// goroutine reaps idle buckets so the map doesn't grow unbounded under
	// spoofed-IP attacks. The same limiter is wired into the gRPC server so
	// a single client IP shares one token bucket across both HTTP and gRPC
	// — an SDK client cannot bypass its HTTP quota by switching protocols.
	var stopRateGC func()
	if rps := o.resolveHTTPRateRPS(); rps > 0 {
		rl := app.NewRateLimiter(rps, o.resolveHTTPRateBurst())
		stopRateGC = rl.StartCleanup(5*time.Minute, 10*time.Minute)
		httpHandler = app.NewRateLimitMiddleware(rl, httpHandler)
		grpcSrv.RateLimiter = rl
	}

	httpHandler = app.NewMaxBodyMiddleware(o.resolveHTTPMaxBody(), httpHandler)

	httpHandler = newRequestLogMiddleware(logger, o.resolveHTTPVerboseLog(), registry, httpHandler)

	// Request ID must wrap recovery, request log, max-body, and rate-limit
	// so the rid is available in the context when any of them runs —
	// including the recovery deferred function (for 500s), the request log
	// (so the log line carries rid=<id> instead of rid=""), and the 413/429
	// rejection paths (so every response, including rejections, carries the
	// X-Request-Id header for correlation).
	httpHandler = newRequestIDMiddleware(httpHandler)

	// Security headers outermost so every response — including 429/413/500
	// produced by the upper middlewares — carries nosniff, frame-options,
	// referrer-policy, and (under TLS) HSTS. The inner handler can still
	// override any header (e.g., set X-Frame-Options: DENY on a specific
	// route).
	certFile, keyFile := o.resolveTLS()
	httpHandler = app.NewSecurityHeadersMiddleware(certFile != "" && keyFile != "", httpHandler)

	// CORS sits just inside security headers so preflight OPTIONS requests
	// get the standard security headers (nosniff, frame-options) on the way
	// out, and the CORS response headers are set before any auth/rate-limit
	// check runs. The middleware is a no-op when CORS is disabled (default,
	// same-origin deployments). Preflight requests are short-circuited to
	// 204 without delegating to the inner handler.
	if corsCfg := o.resolveCORS(); corsCfg.Enabled {
		httpHandler = app.NewCORSMiddleware(corsCfg, httpHandler)
	}

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

	// Build the TLS config once for both HTTP and gRPC. The CertReloader's
	// GetCertificate callback re-reads the cert from disk when the file
	// mtime changes, so operators can rotate certs by replacing the file
	// — no restart, no dropped connections. NextProtos advertises h2 so
	// the gRPC server (which shares this config) negotiates HTTP/2 via
	// ALPN.
	var tlsCfg *tls.Config
	if certFile != "" && keyFile != "" {
		reloader, err := NewCertReloader(certFile, keyFile)
		if err != nil {
			// Cleanup everything wired before this point so a TLS
			// misconfiguration doesn't leak ports, Redis connections,
			// or background goroutines. Without this, a retry after a
			// bad cert hits EADDRINUSE on the listeners and the
			// embedded-Redis goroutine survives process exit.
			stopPeriodic()
			if s := redisSync; s != nil {
				_ = s.Stop()
			}
			_ = grpcLn.Close()
			_ = httpLn.Close()
			_ = redisClient.Close()
			if embeddedRedis != nil {
				_ = embeddedRedis.Close()
			}
			return nil, fmt.Errorf("load TLS cert: %w", err)
		}
		tlsCfg = &tls.Config{
			GetCertificate: reloader.GetCertificate,
			NextProtos:     []string{"h2", "http/1.1"},
			MinVersion:     tls.VersionTLS12,
		}
		httpSrv.TLSConfig = tlsCfg
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
		tlsConfig:     tlsCfg,
		stopPeriodic:  stopPeriodic,
		stopRateGC:    stopRateGC,
		stopResource:  startResourceCollector(registry, bundle, push, httpLn, grpcLn, redisClient, 30*time.Second),
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
	errc := make(chan error, 2)
	go func() {
		var err error
		if s.tlsConfig != nil {
			err = s.grpcSrv.ServeTLSConfig(s.grpcLn, s.tlsConfig)
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
		if s.tlsConfig != nil {
			// httpSrv.TLSConfig is set in New; ServeTLS with empty cert/key
			// paths uses the configured TLSConfig (including GetCertificate).
			err = s.httpSrv.ServeTLS(s.httpLn, "", "")
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
		return s.shutdownWithTimeout()
	case err := <-errc:
		if cerr := s.shutdownWithTimeout(); cerr != nil {
			s.logger.Errorf("shutdown after serve error: %v", cerr)
		}
		return err
	}
}

// shutdownWithTimeout calls Shutdown with a context bounded by the
// configured shutdown timeout. A stuck handler cannot block the shutdown
// past the timeout — after that, connections are forcibly closed.
func (s *Server) shutdownWithTimeout() error {
	timeout := s.opts.resolveShutdownTimeout()
	if timeout < 0 {
		return s.Shutdown(context.Background())
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Shutdown(ctx)
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
	if s.stopResource != nil {
		s.stopResource()
	}
	if err := s.persist.Save(ctx); err != nil {
		s.logger.Errorf("save snapshot on shutdown: %v", err)
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

// buildAuditLogger assembles the AuditLogger for the server. When path is
// empty, events go to the application logger only. When path is set, events
// fan out to both the application logger and a JSON-lines file at path. If
// the file cannot be opened, the server logs a warning and continues with
// logger-only audit so events are not lost.
//
// The assembled logger is wrapped with [app.WrapWithMetrics] so every
// event increments gonacos_audit_events_total{action,result} — the
// alerting signal for "audit event rate spiked" (brute-force login,
// permission scan). The wrapper is a no-op when AuditMetricsRegistry
// is nil (registry not yet wired at first call, or embedder that
// opted out of observability).
func buildAuditLogger(logger Logger, path string) app.AuditLogger {
	loggerAudit := app.NewLoggerAuditLogger(logger)
	if path == "" {
		return app.WrapWithMetrics(loggerAudit)
	}
	fileAudit, err := app.NewFileAuditLogger(path)
	if err != nil {
		if logger != nil {
			logger.Infof("audit: file logger disabled for path %q: %v", path, err)
		}
		return app.WrapWithMetrics(loggerAudit)
	}
	return app.WrapWithMetrics(app.NewMultiAuditLogger(loggerAudit, fileAudit))
}

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

// ReopenAuditLog closes and reopens the audit log file, if one is configured.
// Returns nil when no audit file is in use (loggerAuditLogger and noop
// implementations don't hold a file descriptor).
//
// The canonical caller is the SIGHUP handler installed in cmd/gonacos:
// logrotate renames the audit file and sends SIGHUP; this method swaps the
// file descriptor so subsequent events land in the new file rather than the
// renamed inode. Returns the first error from the underlying Reopen, but
// all configured loggers get a Reopen call so a single broken file doesn't
// block rotation of the others.
//
// Safe to call concurrently with Log: the fileAuditLogger's Reopen takes
// the same mutex as Log, so an in-flight write completes before the fd is
// swapped.
func (s *Server) ReopenAuditLog() error {
	if s.bundle == nil || s.bundle.AuditLogger == nil {
		return nil
	}
	if r, ok := s.bundle.AuditLogger.(app.AuditLogReopener); ok {
		return r.Reopen()
	}
	return nil
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
