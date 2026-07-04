package server

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
