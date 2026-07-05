package server

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

type options struct {
	Addr             string
	GRPCAddr         string
	RedisAddr        string
	DataDir          string
	SnapshotInterval time.Duration
	Root             string
	AuthSecret       string
	SnapshotHMACKey  string
	TLSCertFile      string
	TLSKeyFile       string
	// TLSMinVersion sets the minimum TLS version accepted by the HTTP
	// and gRPC listeners when TLS is enabled. Accepted values: "1.2"
	// (the default, matching Go's crypto/tls recommendation) and "1.3"
	// (stricter — disables TLS 1.2 and its cipher suites, useful when
	// compliance or policy requires forward secrecy by default and the
	// client fleet supports TLS 1.3). Falls back to the
	// GONACOS_TLS_MIN_VERSION env var. An invalid value falls back to
	// "1.2" rather than failing startup — a typo should not lock
	// operators out of the server.
	TLSMinVersion  string
	Logger         Logger
	StrictSnapshot bool

	// Redis connection pool. Zero values fall back to safe production
	// defaults resolved in [options.resolveRedisPool*]. The defaults are
	// tuned for a gonacos process serving hundreds of concurrent SDK
	// clients — PoolSize 50, MinIdleConns 5, DialTimeout 5s, ReadTimeout
	// 3s, WriteTimeout 3s, PoolTimeout 4s, MaxConnAge 30m. Override via
	// the WithRedis* options or GONACOS_REDIS_* env vars.
	RedisPoolSize     int
	RedisMinIdleConns int
	RedisDialTimeout  time.Duration
	RedisReadTimeout  time.Duration
	RedisWriteTimeout time.Duration
	RedisPoolTimeout  time.Duration
	RedisMaxConnAge   time.Duration

	// HTTP production hardening. Zero values fall back to safe defaults
	// resolved in [options.resolveHTTP*].
	HTTPRateRPS      float64
	HTTPRateBurst    int
	HTTPMaxBodyBytes int64
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	// HTTPReadTimeout caps the total time for reading an entire
	// request, including headers and body. Distinct from
	// ReadHeaderTimeout (which only covers the headers and is
	// hardcoded to 5s in [New]): without a body-level cap a client
	// can send a request body very slowly (1 byte/second) and hold
	// a goroutine indefinitely, even with maxBodyMiddleware limiting
	// the total size — that middleware caps bytes, not read rate.
	// The slowloris-on-body attack exhausts the server's goroutine
	// and fd budget without sending much data. Default 30s (see
	// resolveHTTPReadTimeout) gives a 10 MiB upload a minimum
	// sustained rate of ~333 KB/s, which is fine for LAN and most
	// WAN deployments; operators with very slow clients can raise it
	// via [WithHTTPReadTimeout] or GONACOS_HTTP_READ_TIMEOUT.
	HTTPReadTimeout time.Duration

	// Request logging. When true, every HTTP request is logged (including
	// health/metrics probes). When false (default), noisy paths are
	// excluded from the log but everything else is logged.
	HTTPVerboseLog bool

	// Login brute-force protection. Zero values disable throttling;
	// non-zero values configure the (ip, username) lockout policy.
	LoginMaxFailures     int
	LoginFailWindow      time.Duration
	LoginLockoutDuration time.Duration

	// Snapshot backup rotation. When > 0, the periodic snapshot save keeps
	// the prior N dump files as <dumpPath>.1, <dumpPath>.2, ... so a
	// corrupted or accidentally-erased latest snapshot can be recovered
	// from the previous one. Zero (default) disables rotation.
	SnapshotBackupCount int

	// ShutdownTimeout is the maximum time Shutdown will wait for in-flight
	// HTTP/gRPC handlers to complete before forcibly closing connections.
	// Zero falls back to resolveShutdownTimeout (30s default). A negative
	// value disables the timeout (wait forever — not recommended in
	// production, as a stuck handler would block shutdown indefinitely).
	ShutdownTimeout time.Duration

	// GRPCMaxFrameBytes caps the payload size of a single gRPC frame the
	// server accepts from a peer. Zero falls back to the gRPC default
	// (4 MiB). A negative value disables the cap (not recommended — a
	// malicious peer can claim a 4 GiB body and drive the process into
	// OOM before the handler runs).
	GRPCMaxFrameBytes int

	// GRPCKeepAlive configures HTTP/2 PING-based liveness detection on the
	// gRPC server. When ReadIdleTimeout > 0, the server sends a PING after
	// that duration of connection silence; if no ack arrives within
	// PingTimeout, the connection is closed. Catches half-open connections
	// (client crashed without FIN) that would otherwise hold a goroutine +
	// fd indefinitely. Zero values disable PINGs (legacy behavior). Falls
	// back to GONACOS_GRPC_KEEPALIVE_READ_IDLE and
	// GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT env vars.
	GRPCKeepAlive GRPCKeepAliveConfig

	// GRPCReadFrameTimeout caps the time spent reading a single gRPC
	// frame (header + body) from a peer. When the deadline elapses,
	// the read is aborted and the stream is closed with
	// DEADLINE_EXCEEDED. This closes the slowloris-on-body window on
	// the gRPC path: without a per-frame deadline, a peer can send a
	// frame body 1 byte at a time and hold the server's goroutine for
	// up to GRPCMaxFrameBytes seconds (4 MiB at 1 byte/sec ≈ 48 days),
	// even with MaxConns capping the total connection count — each
	// held connection still holds a goroutine and a fd. Zero falls
	// back to 30s; negative disables (not recommended). Falls back to
	// GONACOS_GRPC_READ_FRAME_TIMEOUT env var.
	GRPCReadFrameTimeout time.Duration

	// GRPCMaxConcurrentStreams caps the number of concurrent HTTP/2
	// streams accepted on a single gRPC client connection. Zero falls
	// back to 100 (matching Go's http2.Server default and the gRPC
	// client's advertised limit); negative disables the cap (http2.Server
	// then applies its own 100 default). This is the per-connection
	// defense complementary to MaxConns (which caps total connections):
	// a single connection that opens 100 streams each holding a server
	// goroutine + ~4 MiB of frame-buffer headroom can still burn
	// goroutines and memory. Lowering to e.g. 32 tightens the
	// per-connection blast radius; legitimate SDK clients rarely need
	// more than a handful of concurrent streams. Falls back to
	// GONACOS_GRPC_MAX_CONCURRENT_STREAMS env var.
	GRPCMaxConcurrentStreams int

	// GRPCWriteByteTimeout is the HTTP/2 server-side write timeout:
	// when data is buffered to write but cannot be flushed within this
	// duration, the connection is closed. This is the write-side
	// counterpart to GRPCReadFrameTimeout — ReadFrameTimeout caps the
	// time spent reading a request frame (closing the slowloris-on-body
	// window on the request path), while WriteByteTimeout caps the time
	// spent writing a response frame (closing the symmetric window
	// where a slow client cannot drain the server's response buffer,
	// holding a server goroutine + the buffered response bytes
	// indefinitely). Zero disables (the legacy behavior — relies on
	// IdleTimeout and TCP write deadlines to eventually fail). Falls
	// back to GONACOS_GRPC_WRITE_BYTE_TIMEOUT env var.
	GRPCWriteByteTimeout time.Duration

	// MaxConns caps the total number of concurrent TCP connections the
	// HTTP and gRPC servers accept. Zero falls back to resolveMaxConns
	// (10000 default). A negative value disables the cap. When the cap is
	// reached, new connections are immediately closed (the peer sees a
	// reset) rather than queued — queuing would still hold the file
	// descriptor, defeating the cap. Pair with the per-IP rate limiter
	// for request-level protection.
	MaxConns int

	// LogLevel controls which messages the default stdLogger emits. A nil
	// pointer falls back to resolveLogLevel (GONACOS_LOG_LEVEL env var,
	// default INFO). Only affects the default logger; a custom [Logger]
	// passed via [WithLogger] is responsible for its own filtering.
	LogLevel *LogLevel

	// LogFormat selects the output format of the default logger. A nil
	// pointer falls back to resolveLogFormat (GONACOS_LOG_FORMAT env var,
	// default text). Only affects the default logger; a custom [Logger]
	// passed via [WithLogger] is responsible for its own format.
	LogFormat *LogFormat

	// MetricsToken, when non-empty, requires scrapers to present it as a
	// Bearer token (Authorization: Bearer <token>) on the /metrics endpoint.
	// When empty (default), /metrics is publicly accessible — appropriate
	// for development or when the network layer already restricts access.
	// Production deployments should set a token to avoid leaking process
	// and business metrics to unauthenticated callers.
	MetricsToken string

	// AuditLogFile, when non-empty, writes JSON-lines audit events to the
	// named file in addition to the application logger. Events are appended
	// one per line; the parent directory is created if missing. When
	// AuditLogMaxBytes > 0, the file is auto-rotated when it reaches that
	// size, keeping AuditLogMaxBackups copies. When AuditLogMaxBytes is 0,
	// rotation is the operator's responsibility (logrotate(8) with
	// copytruncate, or SIGHUP-based). When empty (default), audit events
	// go only to the application logger.
	AuditLogFile string

	// AuditLogMaxBytes is the file size in bytes that triggers automatic
	// rotation of the audit log. When the current file reaches this size,
	// it is closed, renamed to .1 (shifting existing backups down), and a
	// fresh file is opened. This is the safety net for deployments where
	// logrotate(8) is not configured — without it, a high-volume audit
	// stream can fill the disk in hours. Zero (default) disables
	// size-based rotation; SIGHUP + logrotate(8) is still honored.
	AuditLogMaxBytes int64

	// AuditLogMaxBackups is the number of rotated backup files to keep
	// when AuditLogMaxBytes > 0. The chain is audit.log (current) →
	// audit.log.1 (most recent) → ... → audit.log.<N> (oldest). When
	// the chain is full, the oldest is deleted before the next
	// rotation. Defaults to 5 when zero and AuditLogMaxBytes > 0.
	AuditLogMaxBackups int

	// CORS configures cross-origin resource sharing for the HTTP API. The
	// middleware is a no-op when CORSConfig.Enabled is false (the default).
	// Enable when the React console is served from a different origin than
	// the API. Falls back to environment variables via resolveCORS.
	CORS app.CORSConfig

	// TrustedProxies is the list of CIDR ranges (e.g. "10.0.0.0/8",
	// "192.168.1.5/32") from which the X-Forwarded-For and X-Real-IP
	// headers are honored. When a request's RemoteAddr matches one of
	// these ranges, the proxy-set headers are trusted to carry the
	// real client IP; otherwise the headers are ignored and RemoteAddr
	// is used directly.
	//
	// Empty (default) means no proxy is trusted — X-Forwarded-For is
	// ignored entirely. This is the secure default: a direct deployment
	// (no proxy in front) is not vulnerable to IP spoofing, and a
	// proxied deployment must explicitly opt in by listing the proxy
	// CIDRs. Without this gate, any client could forge the
	// X-Forwarded-For header to bypass per-IP rate limits, evade login
	// throttling, and pollute the audit trail with a spoofed IP.
	// Falls back to the GONACOS_TRUSTED_PROXIES env var (comma-separated).
	TrustedProxies []string
}

// GRPCKeepAliveConfig mirrors the gRPC server's keepalive config without
// pulling the grpc package into options.go. Converted in [Server.New] via
// [options.resolveGRPCKeepAlive].
type GRPCKeepAliveConfig struct {
	ReadIdleTimeout time.Duration
	PingTimeout     time.Duration
}

// Option configures a Server at construction time. Pass to [New].
type Option func(*options)

// WithAddr sets the HTTP listen address (default ":8848").
func WithAddr(addr string) Option {
	return func(o *options) { o.Addr = addr }
}

// WithGRPCAddr sets the gRPC listen address. If empty, the gRPC port is
// derived from the HTTP port + 1000 (Nacos convention: 8848 -> 9848).
func WithGRPCAddr(addr string) Option {
	return func(o *options) { o.GRPCAddr = addr }
}

// WithRedisAddr sets the Redis address. If empty, gonacos starts an embedded
// miniredis in-process (standalone mode). If non-empty, gonacos connects to
// the external Redis and enables cross-node sync.
func WithRedisAddr(addr string) Option {
	return func(o *options) { o.RedisAddr = addr }
}

// WithRedisPoolSize sets the maximum number of socket connections to Redis.
// Default 50 — enough for a gonacos process serving hundreds of concurrent
// SDK clients. Set higher if the metrics show connection-pool exhaustion
// (gonacos_redis_pool_* — future). Set lower for resource-constrained
// embedded deployments.
func WithRedisPoolSize(n int) Option {
	return func(o *options) { o.RedisPoolSize = n }
}

// WithRedisMinIdleConns sets the minimum number of idle connections the
// pool keeps warm. Default 5 — eliminates cold-start latency on the first
// few requests after a pause. Set to 0 to disable (let the pool start
// empty and grow on demand).
func WithRedisMinIdleConns(n int) Option {
	return func(o *options) { o.RedisMinIdleConns = n }
}

// WithRedisDialTimeout sets the timeout for establishing a new Redis
// connection. Default 5s — long enough to survive a transient network
// blip, short enough that a down Redis doesn't hang the startup path.
func WithRedisDialTimeout(d time.Duration) Option {
	return func(o *options) { o.RedisDialTimeout = d }
}

// WithRedisReadTimeout sets the timeout for a single Redis command's
// response. Default 3s — Redis commands should be sub-millisecond; 3s is
// a generous ceiling that catches a stuck Redis without false-positiving
// on a slow GC pause.
func WithRedisReadTimeout(d time.Duration) Option {
	return func(o *options) { o.RedisReadTimeout = d }
}

// WithRedisWriteTimeout sets the timeout for sending a command to Redis.
// Default 3s. Set higher for high-latency links to a remote Redis.
func WithRedisWriteTimeout(d time.Duration) Option {
	return func(o *options) { o.RedisWriteTimeout = d }
}

// WithRedisPoolTimeout sets the maximum time to wait for a connection from
// the pool when all connections are in use. Default 4s — slightly longer
// than ReadTimeout so a pool-exhaustion event surfaces as a ReadTimeout
// rather than a misleading PoolTimeout.
func WithRedisPoolTimeout(d time.Duration) Option {
	return func(o *options) { o.RedisPoolTimeout = d }
}

// WithRedisMaxConnAge sets the maximum age of a connection before it's
// recycled. Default 30m — catches connections that have gone stale due to
// an intermediate proxy (e.g., HAProxy's default 1h idle timeout) without
// churning the pool. Set to 0 to disable (connections live forever).
func WithRedisMaxConnAge(d time.Duration) Option {
	return func(o *options) { o.RedisMaxConnAge = d }
}

// WithDataDir sets the directory for the embedded Redis disk dump
// (snapshot.json). Only used in embedded mode. If empty, defaults to
// <root>/.gonacos/data. Ignored when WithRedisAddr is set.
func WithDataDir(dir string) Option {
	return func(o *options) { o.DataDir = dir }
}

// WithSnapshotInterval sets the periodic snapshot save interval. If zero,
// defaults to 30s.
func WithSnapshotInterval(d time.Duration) Option {
	return func(o *options) { o.SnapshotInterval = d }
}

// WithRoot sets the project root path used by the contract builder to
// enumerate OpenAPI operations for 501 stub registration. If the directory
// does not contain api/openapi/upstream/*.json, stub registration is skipped
// silently and implemented routes work as normal. Defaults to ".".
func WithRoot(root string) Option {
	return func(o *options) { o.Root = root }
}

// WithAuthSecret sets the HMAC-SHA256 secret used to sign auth tokens. All
// nodes in a cluster must share the same secret so a token issued by one
// node verifies on every other node. If empty, a random per-process secret
// is generated (standalone-only; tokens won't survive a restart or verify
// across nodes). Falls back to the GONACOS_AUTH_SECRET env var.
func WithAuthSecret(secret string) Option {
	return func(o *options) { o.AuthSecret = secret }
}

// WithSnapshotHMACKey sets the HMAC-SHA256 key used to authenticate the
// disk dump file. When set, Save computes the HMAC of the dump bytes and
// writes it to a sibling .hmac file; Load verifies the HMAC before
// unmarshalling, rejecting a tampered dump (e.g., an attacker who replaced
// the file to inject a malicious admin account). Falls back to the
// GONACOS_SNAPSHOT_HMAC_KEY env var, then to the auth secret (so a single
// secret secures both tokens and snapshots in a default deployment). When
// no key is resolvable, verification is skipped — old dumps still load.
func WithSnapshotHMACKey(key string) Option {
	return func(o *options) { o.SnapshotHMACKey = key }
}

// WithTLS enables TLS on both the HTTP and gRPC listeners. certFile and
// keyFile must be PEM-encoded. When set, gRPC negotiates HTTP/2 via ALPN
// (standard TLS-enabled gRPC clients must use the "xds://" / "grpcs://" or
// "tls://" scheme); HTTP serves HTTPS. When unset (default), both listeners
// use plaintext (h2c on gRPC, HTTP/1.1 on HTTP). Falls back to the
// GONACOS_TLS_CERT_FILE and GONACOS_TLS_KEY_FILE env vars.
func WithTLS(certFile, keyFile string) Option {
	return func(o *options) {
		o.TLSCertFile = certFile
		o.TLSKeyFile = keyFile
	}
}

// WithTLSMinVersion sets the minimum TLS version accepted by the HTTP
// and gRPC listeners when TLS is enabled. Accepted values: "1.2" (the
// default, matching Go's crypto/tls recommendation) and "1.3"
// (stricter — disables TLS 1.2 and its cipher suites, useful when
// compliance or policy requires forward secrecy by default and the
// client fleet supports TLS 1.3). Falls back to the
// GONACOS_TLS_MIN_VERSION env var. An invalid value falls back to
// "1.2" rather than failing startup — a typo should not lock operators
// out of the server.
func WithTLSMinVersion(v string) Option {
	return func(o *options) { o.TLSMinVersion = v }
}

// WithLogger sets the logger used by the Server for startup and shutdown
// diagnostics. Pass a structured logger (zap, zerolog, slog) wrapped to
// match the [Logger] interface. When unset, gonacos writes to stderr via
// the standard log package.
func WithLogger(l Logger) Option {
	return func(o *options) { o.Logger = l }
}

// WithStrictSnapshot makes [New] return an error when the snapshot fails to
// load. By default snapshot load errors are logged and the server starts
// with empty state, which is appropriate for first-time boot. Set this in
// production where starting without persisted state would be worse than
// failing fast. Falls back to the GONACOS_STRICT_SNAPSHOT env var ("1",
// "true", "yes" — case-insensitive — to enable).
func WithStrictSnapshot(strict bool) Option {
	return func(o *options) { o.StrictSnapshot = strict }
}

// WithHTTPRateLimit sets a per-client-IP token bucket rate limit on the HTTP
// handler. rps is the steady-state requests-per-second; burst is the maximum
// burst size before throttling kicks in. A burst of zero is clamped to rps
// rounded up. When rps <= 0, rate limiting is disabled (default). Recommended
// production value: 100 rps with burst 200 — generous enough for legitimate
// SDK traffic (which is low-volume per client) while protecting against
// abusive clients.
func WithHTTPRateLimit(rps float64, burst int) Option {
	return func(o *options) {
		o.HTTPRateRPS = rps
		o.HTTPRateBurst = burst
	}
}

// WithHTTPMaxBody sets the maximum allowed size of an HTTP request body. A
// request exceeding the limit returns 413 Request Entity Too Large. When
// zero (default), a 10 MiB cap is enforced by [New]; pass a negative value
// to disable the cap (not recommended in production).
func WithHTTPMaxBody(bytes int64) Option {
	return func(o *options) { o.HTTPMaxBodyBytes = bytes }
}

// WithHTTPWriteTimeout sets the maximum duration of an HTTP write (response).
// A zero value defaults to 30s; pass a negative value to disable (not
// recommended in production). Long-running streaming endpoints should be
// exempt; the standard Nacos API is request-response, so 30s is generous.
func WithHTTPWriteTimeout(d time.Duration) Option {
	return func(o *options) { o.HTTPWriteTimeout = d }
}

// WithHTTPIdleTimeout sets the maximum amount of time an idle (keep-alive)
// HTTP connection is held open. A zero value defaults to 120s; pass a
// negative value to disable (matches Go http.Server default of no timeout,
// not recommended in production).
func WithHTTPIdleTimeout(d time.Duration) Option {
	return func(o *options) { o.HTTPIdleTimeout = d }
}

// WithHTTPReadTimeout sets the maximum duration for reading an entire
// HTTP request, including headers and body. A zero value defaults to
// 30s; pass a negative value to disable (not recommended — exposes
// the server to slowloris-on-body attacks where a client sends a
// request body very slowly to hold a goroutine indefinitely).
//
// Distinct from ReadHeaderTimeout (5s, hardcoded): ReadHeaderTimeout
// only covers the request line and headers, so without a body-level
// cap a client can send a 10 MiB body at 1 byte/second and hold a
// goroutine for ~121 days. maxBodyMiddleware caps total bytes but
// not read rate; this timeout caps read time.
//
// gonacos has no streaming-upload endpoints, so 30s is safe for the
// standard Nacos API (10 MiB at 333 KB/s). Operators with very slow
// clients can raise it; the value is exposed as
// GONACOS_HTTP_READ_TIMEOUT.
func WithHTTPReadTimeout(d time.Duration) Option {
	return func(o *options) { o.HTTPReadTimeout = d }
}

// WithHTTPVerboseLog enables per-request logging including health and
// metrics probes. Default is false: noisy paths are excluded from the log
// but everything else (config/naming/auth/admin) is logged at one line per
// request with method, path, status, duration, and remote address.
func WithHTTPVerboseLog(verbose bool) Option {
	return func(o *options) { o.HTTPVerboseLog = verbose }
}

// WithLoginThrottle enables per-(client-IP, username) brute-force protection
// on the /v3/auth/user/login endpoint. After maxFailures consecutive failed
// logins within failWindow, the pair is locked for lockoutDuration. A
// successful login resets the counter. Recommended production: 5 failures,
// 5m window, 15m lockout. Pass maxFailures=0 to disable (default).
func WithLoginThrottle(maxFailures int, failWindow, lockoutDuration time.Duration) Option {
	return func(o *options) {
		o.LoginMaxFailures = maxFailures
		o.LoginFailWindow = failWindow
		o.LoginLockoutDuration = lockoutDuration
	}
}

// WithSnapshotBackupCount configures the snapshot dump file to retain the
// prior N snapshots as <dumpPath>.1, <dumpPath>.2, ... so a corrupted latest
// snapshot can be recovered from the previous one. Only meaningful in
// standalone mode (embedded Redis with disk dump). Recommended production
// value: 5. Zero (default) disables rotation.
func WithSnapshotBackupCount(n int) Option {
	return func(o *options) { o.SnapshotBackupCount = n }
}

// WithShutdownTimeout sets the maximum time Shutdown will wait for in-flight
// HTTP/gRPC handlers to complete before forcibly closing connections. The
// default is 30s — long enough for legitimate long-poll responses to drain,
// short enough that a stuck handler doesn't block a rolling restart.
// Pass a negative value to disable (wait forever — not recommended in
// production).
func WithShutdownTimeout(d time.Duration) Option {
	return func(o *options) { o.ShutdownTimeout = d }
}

// WithGRPCMaxFrameBytes caps the payload size of a single gRPC frame the
// server accepts from a peer. The default is 4 MiB (matching the standard
// gRPC client default). A request declaring a larger frame is rejected with
// RESOURCE_EXHAUSTED before the body is read, so a malicious peer cannot
// drive the process into OOM by claiming a 4 GiB body. Pass a negative value
// to disable the cap (not recommended in production).
func WithGRPCMaxFrameBytes(bytes int) Option {
	return func(o *options) { o.GRPCMaxFrameBytes = bytes }
}

// WithMaxConns caps the total number of concurrent TCP connections the
// HTTP and gRPC servers accept. The default is 10000 — generous enough for
// production traffic, low enough to prevent a connection-flood attack from
// exhausting the process's file descriptor limit. When the cap is reached,
// new connections are immediately closed. Pass a negative value to disable
// the cap (not recommended in production). Pair with the per-IP rate
// limiter for request-level protection.
func WithMaxConns(max int) Option {
	return func(o *options) { o.MaxConns = max }
}

// WithLogLevel sets the minimum log level emitted by the default logger.
// DEBUG: all messages (currently equivalent to INFO since the default
// logger has no Debugf method). INFO: Infof and Warnf (default). WARN:
// Warnf only. ERROR: nothing (both Infof and Warnf suppressed). Falls back
// to the GONACOS_LOG_LEVEL env var (case-insensitive: DEBUG, INFO, WARN,
// ERROR; unknown values default to INFO). Only affects the default
// stdLogger; a custom Logger passed via [WithLogger] is responsible for
// its own filtering.
func WithLogLevel(level LogLevel) Option {
	l := level
	return func(o *options) { o.LogLevel = &l }
}

// WithLogFormat selects the output format of the default logger. TextFormat
// (default) writes "LEVEL  message" lines for humans; JSONFormat writes
// one JSON object per line for log collectors (ELK, Loki, Datadog). Falls
// back to the GONACOS_LOG_FORMAT env var (case-insensitive: text, json;
// unknown values default to text). Only affects the default logger; a
// custom Logger passed via [WithLogger] is responsible for its own format.
func WithLogFormat(format LogFormat) Option {
	f := format
	return func(o *options) { o.LogFormat = &f }
}

// WithMetricsToken requires scrapers to present the given Bearer token on
// the /metrics endpoint. When unset (default), /metrics is publicly
// accessible. Production deployments should set a token to prevent
// unauthenticated callers from scraping process and business metrics.
// The token is compared in constant time, so timing attacks cannot
// recover it. Scrapers must send `Authorization: Bearer <token>`.
// Falls back to the GONACOS_METRICS_TOKEN env var.
func WithMetricsToken(token string) Option {
	return func(o *options) { o.MetricsToken = token }
}

// WithAuditLogFile writes JSON-lines audit events to the named file in
// addition to the application logger. Events are appended one per line;
// the parent directory is created if missing. When unset (default), audit
// events go only to the application logger. Falls back to the
// GONACOS_AUDIT_LOG_FILE env var.
func WithAuditLogFile(path string) Option {
	return func(o *options) { o.AuditLogFile = path }
}

// WithAuditLogMaxBytes sets the file size that triggers automatic
// rotation of the audit log. When the current file reaches this many
// bytes, it is rotated (renamed to .1, shifting existing backups down)
// and a fresh file is opened. Zero (default) disables size-based
// rotation; the operator must rely on SIGHUP + logrotate(8). Falls
// back to the GONACOS_AUDIT_LOG_MAX_BYTES env var.
func WithAuditLogMaxBytes(maxBytes int64) Option {
	return func(o *options) { o.AuditLogMaxBytes = maxBytes }
}

// WithAuditLogMaxBackups sets the number of rotated backup files to
// keep when AuditLogMaxBytes > 0. The chain is path (current) →
// path.1 → ... → path.<N>. When the chain is full, the oldest is
// deleted before the next rotation. Defaults to 5 when zero and
// AuditLogMaxBytes > 0. Falls back to the
// GONACOS_AUDIT_LOG_MAX_BACKUPS env var.
func WithAuditLogMaxBackups(n int) Option {
	return func(o *options) { o.AuditLogMaxBackups = n }
}

// WithCORS enables cross-origin resource sharing for the HTTP API. Pass a
// populated CORSConfig with Enabled=true to activate; the middleware is a
// no-op when Enabled is false. Falls back to environment variables
// (GONACOS_CORS_ENABLED, GONACOS_CORS_ALLOW_ORIGINS, etc.) via resolveCORS.
// Enable when the React console is served from a different origin than the
// API; same-origin deployments can leave this disabled.
func WithCORS(cfg app.CORSConfig) Option {
	return func(o *options) { o.CORS = cfg }
}

// WithTrustedProxies configures the CIDR ranges (e.g. "10.0.0.0/8",
// "192.168.1.5/32") from which X-Forwarded-For and X-Real-IP headers are
// honored. When a request's RemoteAddr matches one of these ranges, the
// proxy-set headers are trusted to carry the real client IP; otherwise
// they are ignored and RemoteAddr is used directly.
//
// Empty (default) means no proxy is trusted — X-Forwarded-For is ignored
// entirely, which is the secure default for a direct deployment. A proxied
// deployment must explicitly opt in by listing the proxy CIDRs, otherwise
// any client could forge X-Forwarded-For to bypass rate limits and pollute
// the audit trail. Falls back to the GONACOS_TRUSTED_PROXIES env var
// (comma-separated).
func WithTrustedProxies(cidrs []string) Option {
	return func(o *options) { o.TrustedProxies = cidrs }
}

// WithGRPCKeepAlive enables HTTP/2 PING-based liveness detection on the gRPC
// server. After readIdle seconds of connection silence, the server sends a
// PING; if no ack arrives within pingTimeout, the connection is closed.
// Catches half-open connections (client crashed without FIN) that would
// otherwise hold a goroutine + fd indefinitely. Recommended production:
// readIdle=15s, pingTimeout=15s. Pass readIdle=0 to disable (default).
// Falls back to the GONACOS_GRPC_KEEPALIVE_READ_IDLE and
// GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT env vars.
func WithGRPCKeepAlive(readIdle, pingTimeout time.Duration) Option {
	return func(o *options) {
		o.GRPCKeepAlive = GRPCKeepAliveConfig{
			ReadIdleTimeout: readIdle,
			PingTimeout:     pingTimeout,
		}
	}
}

// WithGRPCReadFrameTimeout caps the time spent reading a single gRPC
// frame (header + body) from a peer. When the deadline elapses, the
// read is aborted and the stream is closed with DEADLINE_EXCEEDED.
// This closes the slowloris-on-body window on the gRPC path: without
// a per-frame deadline, a peer can send a frame body 1 byte at a time
// and hold the server's goroutine for up to GRPCMaxFrameBytes seconds
// (4 MiB at 1 byte/sec ≈ 48 days). A zero value defaults to 30s; a
// negative value disables (not recommended). The timeout applies
// per-frame on both unary and streaming RPCs; a streaming peer that
// sends a frame every <30s is unaffected. Falls back to the
// GONACOS_GRPC_READ_FRAME_TIMEOUT env var.
func WithGRPCReadFrameTimeout(d time.Duration) Option {
	return func(o *options) { o.GRPCReadFrameTimeout = d }
}

// WithGRPCMaxConcurrentStreams caps the number of concurrent HTTP/2
// streams accepted on a single gRPC client connection. Zero falls back
// to 100 (matching Go's http2.Server default); negative disables the
// cap (http2.Server applies its own 100 default). This is the
// per-connection defense complementary to [WithMaxConns]: a single
// connection that opens many in-flight streams each holding a server
// goroutine + frame-buffer headroom can still burn goroutines and
// memory. Lowering to e.g. 32 tightens the per-connection blast
// radius; legitimate SDK clients rarely need more than a handful of
// concurrent streams. Falls back to the
// GONACOS_GRPC_MAX_CONCURRENT_STREAMS env var.
func WithGRPCMaxConcurrentStreams(n int) Option {
	return func(o *options) { o.GRPCMaxConcurrentStreams = n }
}

// WithGRPCWriteByteTimeout sets the HTTP/2 server-side write timeout:
// when data is buffered to write but cannot be flushed within this
// duration, the connection is closed. This is the write-side
// counterpart to [WithGRPCReadFrameTimeout] — ReadFrameTimeout caps
// the time spent reading a request frame, while WriteByteTimeout caps
// the time spent writing a response frame, closing the symmetric
// window where a slow client cannot drain the server's response
// buffer. Zero disables (the legacy behavior). The timeout is
// per-write-byte, not per-RPC: a streaming RPC that continuously
// writes is unaffected; only a connection that stalls mid-write is
// closed. Falls back to the GONACOS_GRPC_WRITE_BYTE_TIMEOUT env var.
func WithGRPCWriteByteTimeout(d time.Duration) Option {
	return func(o *options) { o.GRPCWriteByteTimeout = d }
}

func (o *options) resolveAddr() string {
	if o.Addr != "" {
		return o.Addr
	}
	return ":8848"
}

func (o *options) resolveGRPCAddr() string {
	if o.GRPCAddr != "" {
		return o.GRPCAddr
	}
	return grpcAddrFor(o.resolveAddr())
}

func (o *options) resolveRedisAddr() string {
	if o.RedisAddr != "" {
		return o.RedisAddr
	}
	return os.Getenv("GONACOS_REDIS_ADDR")
}

// resolveRedisPoolSize returns the configured pool size or the default 50.
// GONACOS_REDIS_POOL_SIZE env var overrides when the option is unset.
func (o *options) resolveRedisPoolSize() int {
	if o.RedisPoolSize > 0 {
		return o.RedisPoolSize
	}
	if v := os.Getenv("GONACOS_REDIS_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50
}

// resolveRedisMinIdleConns returns the configured min idle conns or the
// default 5.
func (o *options) resolveRedisMinIdleConns() int {
	if o.RedisMinIdleConns > 0 {
		return o.RedisMinIdleConns
	}
	if v := os.Getenv("GONACOS_REDIS_MIN_IDLE_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 5
}

// resolveRedisDialTimeout returns the configured dial timeout or the
// default 5s.
func (o *options) resolveRedisDialTimeout() time.Duration {
	if o.RedisDialTimeout > 0 {
		return o.RedisDialTimeout
	}
	if v := os.Getenv("GONACOS_REDIS_DIAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Second
}

// resolveRedisReadTimeout returns the configured read timeout or the
// default 3s.
func (o *options) resolveRedisReadTimeout() time.Duration {
	if o.RedisReadTimeout > 0 {
		return o.RedisReadTimeout
	}
	if v := os.Getenv("GONACOS_REDIS_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 3 * time.Second
}

// resolveRedisWriteTimeout returns the configured write timeout or the
// default 3s.
func (o *options) resolveRedisWriteTimeout() time.Duration {
	if o.RedisWriteTimeout > 0 {
		return o.RedisWriteTimeout
	}
	if v := os.Getenv("GONACOS_REDIS_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 3 * time.Second
}

// resolveRedisPoolTimeout returns the configured pool timeout or the
// default 4s.
func (o *options) resolveRedisPoolTimeout() time.Duration {
	if o.RedisPoolTimeout > 0 {
		return o.RedisPoolTimeout
	}
	if v := os.Getenv("GONACOS_REDIS_POOL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 4 * time.Second
}

// resolveRedisMaxConnAge returns the configured max conn age or the
// default 30m. Zero or negative disables (connections live forever).
func (o *options) resolveRedisMaxConnAge() time.Duration {
	if o.RedisMaxConnAge != 0 {
		return o.RedisMaxConnAge
	}
	if v := os.Getenv("GONACOS_REDIS_MAX_CONN_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 30 * time.Minute
}

func (o *options) resolveDataDir() string {
	if o.DataDir != "" {
		return o.DataDir
	}
	if v := os.Getenv("GONACOS_DATA_DIR"); v != "" {
		return v
	}
	root := o.Root
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".gonacos", "data")
}

func (o *options) resolveSnapshotInterval() time.Duration {
	if o.SnapshotInterval > 0 {
		return o.SnapshotInterval
	}
	if v := os.Getenv("GONACOS_SNAPSHOT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (o *options) resolveRoot() string {
	if o.Root != "" {
		return o.Root
	}
	return "."
}

func (o *options) resolveAuthSecret() string {
	if o.AuthSecret != "" {
		return o.AuthSecret
	}
	return os.Getenv("GONACOS_AUTH_SECRET")
}

// resolveSnapshotHMACKey returns the HMAC key to authenticate the disk
// dump file, or empty string to skip verification. Resolution order:
//  1. WithSnapshotHMACKey (explicit)
//  2. GONACOS_SNAPSHOT_HMAC_KEY env var
//  3. The auth secret (single-secret default: one secret secures both
//     tokens and snapshots). When the auth secret is also empty — e.g.,
//     a standalone dev instance with no configured secret — the empty
//     return skips verification, preserving the pre-HMAC load behavior
//     so old dumps still load.
func (o *options) resolveSnapshotHMACKey() string {
	if o.SnapshotHMACKey != "" {
		return o.SnapshotHMACKey
	}
	if v := os.Getenv("GONACOS_SNAPSHOT_HMAC_KEY"); v != "" {
		return v
	}
	return o.resolveAuthSecret()
}

func (o *options) resolveTLS() (cert, key string) {
	if o.TLSCertFile != "" || o.TLSKeyFile != "" {
		return o.TLSCertFile, o.TLSKeyFile
	}
	return os.Getenv("GONACOS_TLS_CERT_FILE"), os.Getenv("GONACOS_TLS_KEY_FILE")
}

// resolveTLSMinVersion returns the minimum TLS version for the HTTP and
// gRPC listeners. Defaults to TLS 1.2 (matching Go's crypto/tls
// recommendation). "1.3" disables TLS 1.2 and its cipher suites —
// useful when compliance or policy requires forward secrecy by default
// and the client fleet supports TLS 1.3. An invalid value falls back
// to 1.2 rather than failing startup — a typo should not lock
// operators out of the server.
func (o *options) resolveTLSMinVersion() uint16 {
	v := o.TLSMinVersion
	if v == "" {
		v = os.Getenv("GONACOS_TLS_MIN_VERSION")
	}
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "1.3":
		return tls.VersionTLS13
	case "1.2", "":
		return tls.VersionTLS12
	default:
		return tls.VersionTLS12
	}
}

func (o *options) resolveLogger() Logger {
	if o.Logger != nil {
		return o.Logger
	}
	if o.resolveLogFormat() == JSONFormat {
		return newJSONLogger(o.resolveLogLevel())
	}
	return newStdLogger(o.resolveLogLevel())
}

// resolveLogFormat returns the configured log format. Explicit option wins;
// otherwise the GONACOS_LOG_FORMAT env var is consulted (case-insensitive
// "text" or "json"); unknown values fall back to TextFormat.
func (o *options) resolveLogFormat() LogFormat {
	if o.LogFormat != nil {
		return *o.LogFormat
	}
	return ParseLogFormat(os.Getenv("GONACOS_LOG_FORMAT"))
}

func (o *options) resolveStrictSnapshot() bool {
	if o.StrictSnapshot {
		return true
	}
	switch strings.ToLower(os.Getenv("GONACOS_STRICT_SNAPSHOT")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// buildLoginThrottle constructs the per-(ip, username) brute-force limiter
// from the server options. Returns nil when throttling is disabled
// (maxFailures <= 0), so the handler builder leaves /login unwrapped.
func (o *options) buildLoginThrottle() *app.LoginThrottle {
	maxFailures := o.resolveLoginMaxFailures()
	if maxFailures <= 0 {
		return nil
	}
	return app.NewLoginThrottle(maxFailures, o.resolveLoginFailWindow(), o.resolveLoginLockoutDuration())
}

// resolveHTTPRateRPS returns the configured per-IP rate limit (requests per
// second). A zero value means rate limiting is disabled.
func (o *options) resolveHTTPRateRPS() float64 {
	if o.HTTPRateRPS != 0 {
		return o.HTTPRateRPS
	}
	if v := os.Getenv("GONACOS_HTTP_RATE_RPS"); v != "" {
		if rps, err := strconv.ParseFloat(v, 64); err == nil && rps > 0 {
			return rps
		}
	}
	return 0
}

// resolveHTTPRateBurst returns the burst size for the per-IP rate limiter.
// Defaults to 2x the rps when unset.
func (o *options) resolveHTTPRateBurst() int {
	if o.HTTPRateBurst > 0 {
		return o.HTTPRateBurst
	}
	rps := o.resolveHTTPRateRPS()
	if rps <= 0 {
		return 0
	}
	burst := int(rps) * 2
	if burst < 1 {
		burst = 1
	}
	return burst
}

// resolveHTTPMaxBody returns the maximum HTTP request body size in bytes.
// Defaults to 10 MiB when unset. A negative return value disables the cap.
func (o *options) resolveHTTPMaxBody() int64 {
	if o.HTTPMaxBodyBytes != 0 {
		return o.HTTPMaxBodyBytes
	}
	if v := os.Getenv("GONACOS_HTTP_MAX_BODY"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 10 * 1024 * 1024
}

// resolveHTTPWriteTimeout returns the HTTP write timeout. Defaults to 30s.
// A negative return value disables the timeout.
func (o *options) resolveHTTPWriteTimeout() time.Duration {
	if o.HTTPWriteTimeout != 0 {
		return o.HTTPWriteTimeout
	}
	if v := os.Getenv("GONACOS_HTTP_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 30 * time.Second
}

// resolveHTTPIdleTimeout returns the HTTP idle (keep-alive) timeout.
// Defaults to 120s. A negative return value disables the timeout.
func (o *options) resolveHTTPIdleTimeout() time.Duration {
	if o.HTTPIdleTimeout != 0 {
		return o.HTTPIdleTimeout
	}
	if v := os.Getenv("GONACOS_HTTP_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 120 * time.Second
}

// resolveHTTPReadTimeout returns the maximum duration for reading an
// entire HTTP request, including headers and body. Defaults to 30s.
// A negative return value disables the timeout (not recommended —
// exposes the server to slowloris-on-body attacks).
//
// Distinct from the hardcoded ReadHeaderTimeout (5s), which only
// covers the request line and headers. Without this body-level cap,
// a client can send a request body very slowly (1 byte/second) and
// hold a goroutine indefinitely, even with maxBodyMiddleware
// limiting the total size — that middleware caps bytes, not read
// rate. The slowloris-on-body attack exhausts the server's goroutine
// and fd budget without sending much data.
//
// 30s gives a 10 MiB upload (the default max body) a minimum
// sustained rate of ~333 KB/s, which is fine for LAN and most WAN
// deployments.
func (o *options) resolveHTTPReadTimeout() time.Duration {
	if o.HTTPReadTimeout != 0 {
		return o.HTTPReadTimeout
	}
	if v := os.Getenv("GONACOS_HTTP_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 30 * time.Second
}

// resolveHTTPVerboseLog returns whether to log every HTTP request including
// health/metrics probes. Defaults to false.
func (o *options) resolveHTTPVerboseLog() bool {
	if o.HTTPVerboseLog {
		return true
	}
	switch strings.ToLower(os.Getenv("GONACOS_HTTP_VERBOSE_LOG")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// resolveLoginMaxFailures returns the login brute-force lockout threshold.
// Returns 0 (disabled) when unset.
func (o *options) resolveLoginMaxFailures() int {
	if o.LoginMaxFailures > 0 {
		return o.LoginMaxFailures
	}
	if v := os.Getenv("GONACOS_LOGIN_MAX_FAILURES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// resolveLoginFailWindow returns the window over which consecutive login
// failures are counted. Defaults to 5m when throttling is enabled.
func (o *options) resolveLoginFailWindow() time.Duration {
	if o.LoginFailWindow > 0 {
		return o.LoginFailWindow
	}
	if v := os.Getenv("GONACOS_LOGIN_FAIL_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Minute
}

// resolveLoginLockoutDuration returns the lockout duration applied after the
// failure threshold is reached. Defaults to 15m when throttling is enabled.
func (o *options) resolveLoginLockoutDuration() time.Duration {
	if o.LoginLockoutDuration > 0 {
		return o.LoginLockoutDuration
	}
	if v := os.Getenv("GONACOS_LOGIN_LOCKOUT_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}

// resolveSnapshotBackupCount returns how many prior disk-dump snapshots to
// retain. Returns 0 (no rotation) when unset.
func (o *options) resolveSnapshotBackupCount() int {
	if o.SnapshotBackupCount > 0 {
		return o.SnapshotBackupCount
	}
	if v := os.Getenv("GONACOS_SNAPSHOT_BACKUP_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// resolveShutdownTimeout returns the shutdown timeout. Defaults to 30s. A
// negative return value disables the timeout (wait forever).
func (o *options) resolveShutdownTimeout() time.Duration {
	if o.ShutdownTimeout != 0 {
		return o.ShutdownTimeout
	}
	if v := os.Getenv("GONACOS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 30 * time.Second
}

// resolveGRPCMaxFrameBytes returns the per-gRPC-frame size cap. Defaults to
// 4 MiB (matching the standard gRPC client default). A negative return value
// disables the cap.
func (o *options) resolveGRPCMaxFrameBytes() int {
	if o.GRPCMaxFrameBytes != 0 {
		return o.GRPCMaxFrameBytes
	}
	if v := os.Getenv("GONACOS_GRPC_MAX_FRAME_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 4 * 1024 * 1024
}

// resolveGRPCReadFrameTimeout returns the per-gRPC-frame read deadline.
// Defaults to 30s — generous for legitimate clients (a 4 MiB frame at
// ~133 KB/s) while bounding the slowloris-on-body window. A negative
// return value disables the cap (not recommended — re-opens the
// slowloris window where a peer sends a frame body 1 byte at a time
// and holds the server's goroutine for up to GRPCMaxFrameBytes
// seconds). Falls back to GONACOS_GRPC_READ_FRAME_TIMEOUT env var.
func (o *options) resolveGRPCReadFrameTimeout() time.Duration {
	if o.GRPCReadFrameTimeout != 0 {
		return o.GRPCReadFrameTimeout
	}
	if v := os.Getenv("GONACOS_GRPC_READ_FRAME_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 30 * time.Second
}

// resolveGRPCMaxConcurrentStreams returns the per-connection concurrent-
// stream cap for the gRPC server. Defaults to 100 (matching Go's
// http2.Server default and the gRPC client's advertised limit); negative
// disables the cap (returns 0 — http2.Server then applies its own 100
// default; not recommended — re-opens the per-connection goroutine-
// exhaustion vector where a single peer opens 100 streams each holding
// a goroutine). Falls back to GONACOS_GRPC_MAX_CONCURRENT_STREAMS env
// var.
func (o *options) resolveGRPCMaxConcurrentStreams() int {
	if o.GRPCMaxConcurrentStreams != 0 {
		return o.GRPCMaxConcurrentStreams
	}
	if v := os.Getenv("GONACOS_GRPC_MAX_CONCURRENT_STREAMS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 100
}

// resolveGRPCWriteByteTimeout returns the HTTP/2 server-side write
// timeout for the gRPC server. Defaults to 0 (disabled — the legacy
// behavior that relies on IdleTimeout and TCP write deadlines to
// eventually fail). When set, a connection that cannot flush buffered
// response bytes within this duration is closed, bounding the
// stuck-write window where a slow client holds a server goroutine +
// the buffered response bytes indefinitely. Falls back to
// GONACOS_GRPC_WRITE_BYTE_TIMEOUT env var.
func (o *options) resolveGRPCWriteByteTimeout() time.Duration {
	if o.GRPCWriteByteTimeout != 0 {
		return o.GRPCWriteByteTimeout
	}
	if v := os.Getenv("GONACOS_GRPC_WRITE_BYTE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d != 0 {
			return d
		}
	}
	return 0
}

// resolveMaxConns returns the concurrent-connection cap. Defaults to 10000
// — generous enough for production, low enough to prevent fd exhaustion.
// A negative return value disables the cap.
func (o *options) resolveMaxConns() int {
	if o.MaxConns != 0 {
		return o.MaxConns
	}
	if v := os.Getenv("GONACOS_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 10000
}

// resolveLogLevel returns the minimum log level the default logger emits.
// Returns the explicitly configured level when set, otherwise the
// GONACOS_LOG_LEVEL env var (case-insensitive: DEBUG, INFO, WARN, ERROR),
// otherwise InfoLevel. Unknown env var values fall back to InfoLevel so a
// typo never silently suppresses all logs.
func (o *options) resolveLogLevel() LogLevel {
	if o.LogLevel != nil {
		return *o.LogLevel
	}
	return ParseLogLevel(os.Getenv("GONACOS_LOG_LEVEL"))
}

// resolveMetricsToken returns the Bearer token required to scrape /metrics.
// Returns the explicitly configured token when set, otherwise the
// GONACOS_METRICS_TOKEN env var, otherwise empty (public scrape). The token
// is compared in constant time on every request.
func (o *options) resolveMetricsToken() string {
	if o.MetricsToken != "" {
		return o.MetricsToken
	}
	return os.Getenv("GONACOS_METRICS_TOKEN")
}

// resolveAuditLogFile returns the path to the audit log file. Returns the
// explicitly configured path when set, otherwise the GONACOS_AUDIT_LOG_FILE
// env var, otherwise empty (audit events go only to the application logger).
func (o *options) resolveAuditLogFile() string {
	if o.AuditLogFile != "" {
		return o.AuditLogFile
	}
	return os.Getenv("GONACOS_AUDIT_LOG_FILE")
}

// resolveAuditLogMaxBytes returns the file size that triggers automatic
// rotation. Returns the explicitly configured value when > 0, otherwise
// the GONACOS_AUDIT_LOG_MAX_BYTES env var (parsed as bytes), otherwise 0
// (size-based rotation disabled; SIGHUP + logrotate(8) is still honored).
func (o *options) resolveAuditLogMaxBytes() int64 {
	if o.AuditLogMaxBytes > 0 {
		return o.AuditLogMaxBytes
	}
	if v := os.Getenv("GONACOS_AUDIT_LOG_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// resolveAuditLogMaxBackups returns the number of rotated backup files
// to keep. Returns the explicitly configured value when > 0, otherwise
// the GONACOS_AUDIT_LOG_MAX_BACKUPS env var, otherwise 5 (the default
// when size-based rotation is enabled).
func (o *options) resolveAuditLogMaxBackups() int {
	if o.AuditLogMaxBackups > 0 {
		return o.AuditLogMaxBackups
	}
	if v := os.Getenv("GONACOS_AUDIT_LOG_MAX_BACKUPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return 5
}

// resolveTrustedProxies returns the list of CIDR ranges whose
// X-Forwarded-For and X-Real-IP headers are honored. Returns the
// explicitly configured list when set, otherwise the
// GONACOS_TRUSTED_PROXIES env var (comma-separated), otherwise nil
// (no proxy trusted — X-Forwarded-For is ignored entirely).
func (o *options) resolveTrustedProxies() []string {
	if len(o.TrustedProxies) > 0 {
		return o.TrustedProxies
	}
	if v := os.Getenv("GONACOS_TRUSTED_PROXIES"); v != "" {
		var out []string
		for _, c := range strings.Split(v, ",") {
			if c = strings.TrimSpace(c); c != "" {
				out = append(out, c)
			}
		}
		return out
	}
	return nil
}

// resolveCORS returns the effective CORS config. The explicitly configured
// CORS field wins; when its Enabled flag is false, environment variables
// are consulted so operators can enable CORS without code changes:
//
//   - GONACOS_CORS_ENABLED=1            — gate the middleware
//   - GONACOS_CORS_ALLOW_ORIGINS        — comma-separated origin list
//   - GONACOS_CORS_ALLOW_METHODS        — comma-separated method list
//   - GONACOS_CORS_ALLOW_HEADERS        — comma-separated header list
//   - GONACOS_CORS_ALLOW_CREDENTIALS=1  — emit Allow-Credentials: true
//   - GONACOS_CORS_MAX_AGE              — preflight cache seconds
//
// When GONACOS_CORS_ENABLED is unset (or "0"), CORS stays disabled.
func (o *options) resolveCORS() app.CORSConfig {
	if o.CORS.Enabled {
		return o.CORS
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("GONACOS_CORS_ENABLED"))); v != "1" && v != "true" {
		return app.CORSConfig{}
	}
	cfg := app.CORSConfig{Enabled: true}
	if v := os.Getenv("GONACOS_CORS_ALLOW_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.AllowOrigins = append(cfg.AllowOrigins, o)
			}
		}
	}
	if v := os.Getenv("GONACOS_CORS_ALLOW_METHODS"); v != "" {
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				cfg.AllowMethods = append(cfg.AllowMethods, strings.ToUpper(m))
			}
		}
	}
	if v := os.Getenv("GONACOS_CORS_ALLOW_HEADERS"); v != "" {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				cfg.AllowHeaders = append(cfg.AllowHeaders, h)
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GONACOS_CORS_ALLOW_CREDENTIALS"))) {
	case "1", "true", "yes":
		cfg.AllowCredentials = true
	}
	if v := os.Getenv("GONACOS_CORS_MAX_AGE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxAge = n
		}
	}
	return cfg
}

// resolveGRPCKeepAlive returns the gRPC HTTP/2 keepalive config. Returns the
// explicitly configured values when set, otherwise the
// GONACOS_GRPC_KEEPALIVE_READ_IDLE and GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT
// env vars, otherwise zero values (PINGs disabled — legacy behavior).
func (o *options) resolveGRPCKeepAlive() grpcsrv.KeepAliveConfig {
	cfg := o.GRPCKeepAlive
	if cfg.ReadIdleTimeout == 0 {
		if v := os.Getenv("GONACOS_GRPC_KEEPALIVE_READ_IDLE"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				cfg.ReadIdleTimeout = d
			}
		}
	}
	if cfg.PingTimeout == 0 {
		if v := os.Getenv("GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				cfg.PingTimeout = d
			}
		}
	}
	return grpcsrv.KeepAliveConfig{
		ReadIdleTimeout: cfg.ReadIdleTimeout,
		PingTimeout:     cfg.PingTimeout,
	}
}

// splitHostPort splits an address into host and port. Returns "127.0.0.1" and
// "8848" for ":8848".
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

// grpcAddrFor returns the gRPC listen address derived from an HTTP address
// (HTTP port + 1000, per Nacos convention).
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
		return ":" + strconv.Itoa(p+1000)
	}
	return host + ":" + strconv.Itoa(p+1000)
}
