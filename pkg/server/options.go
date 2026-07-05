package server

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/app"
)

type options struct {
	Addr             string
	GRPCAddr         string
	RedisAddr        string
	DataDir          string
	SnapshotInterval time.Duration
	Root             string
	AuthSecret       string
	TLSCertFile      string
	TLSKeyFile       string
	Logger           Logger
	StrictSnapshot   bool

	// HTTP production hardening. Zero values fall back to safe defaults
	// resolved in [options.resolveHTTP*].
	HTTPRateRPS      float64
	HTTPRateBurst    int
	HTTPMaxBodyBytes int64
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration

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

	// MaxConns caps the total number of concurrent TCP connections the
	// HTTP and gRPC servers accept. Zero falls back to resolveMaxConns
	// (10000 default). A negative value disables the cap. When the cap is
	// reached, new connections are immediately closed (the peer sees a
	// reset) rather than queued — queuing would still hold the file
	// descriptor, defeating the cap. Pair with the per-IP rate limiter
	// for request-level protection.
	MaxConns int
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

func (o *options) resolveTLS() (cert, key string) {
	if o.TLSCertFile != "" || o.TLSKeyFile != "" {
		return o.TLSCertFile, o.TLSKeyFile
	}
	return os.Getenv("GONACOS_TLS_CERT_FILE"), os.Getenv("GONACOS_TLS_KEY_FILE")
}

func (o *options) resolveLogger() Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return defaultLogger
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
