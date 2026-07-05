package grpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Status codes matching gRPC's codes.Code.
const (
	StatusOK                 = 0
	StatusCancelled          = 1
	StatusUnknown            = 2
	StatusInvalidArgument    = 3
	StatusDeadlineExceeded   = 4
	StatusNotFound           = 5
	StatusAlreadyExists      = 6
	StatusPermissionDenied   = 7
	StatusResourceExhausted  = 8
	StatusFailedPrecondition = 9
	StatusAborted            = 10
	StatusOutOfRange         = 11
	StatusUnimplemented      = 12
	StatusInternal           = 13
	StatusUnavailable        = 14
	StatusDataLoss           = 15
	StatusUnauthenticated    = 16
)

// grpcLatencyBuckets is the bucket set for gonacos_grpc_request_duration_seconds.
// Covers sub-millisecond fast paths (in-memory reads) through multi-second
// slow paths (snapshot loads, slow upstream calls) at a resolution that lets
// operators distinguish "p99 under 50ms" from "p99 over 1s" without
// excessive series cardinality. Values are in milliseconds.
var grpcLatencyBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// HTTPLatencyBuckets is the bucket set for gonacos_http_request_duration_seconds.
// Mirrors the gRPC buckets so HTTP and gRPC panels can share a single
// Grafana panel definition without per-protocol overrides. Exposed so the
// HTTP middleware in pkg/server can use the same buckets without duplicating
// the values.
func HTTPLatencyBuckets() []float64 {
	return []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}
}

// HTTPBytesBuckets is the bucket set for gonacos_http_response_bytes.
// Covers small responses (health checks ~100B) through large responses
// (config exports, service list snapshots at multi-MB) at a resolution
// that lets operators spot a regression where a response balloons from
// 1KB to 100KB without per-method cardinality. Values are in bytes.
// Exponential boundaries match common payload sizes: 100B (health),
// 1KB (single config), 16KB (small list), 64KB (page), 1MB (large list),
// 16MB (export). The +Inf bucket captures anything beyond.
func HTTPBytesBuckets() []float64 {
	return []float64{100, 256, 512, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216}
}

// StatusError carries a gRPC status code and message.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("grpc code %d: %s", e.Code, e.Message) }

// NewStatusError creates a StatusError.
func NewStatusError(code int, message string) *StatusError {
	return &StatusError{Code: code, Message: message}
}

// Handler handles a unary Request RPC. The handler receives the decoded
// Payload and returns a response Payload or an error.
type Handler func(ctx context.Context, req Payload) (Payload, error)

// StreamHandler handles a RequestStream RPC (server-streaming). The handler
// receives the request Payload and a sender for response frames.
type StreamHandler func(ctx context.Context, req Payload, send func(Payload) error) error

// BiStreamHandler handles a BiRequestStream RPC (bidi-streaming). The handler
// reads request frames from recv and sends response frames via send.
type BiStreamHandler func(ctx context.Context, recv func() (Payload, error), send func(Payload) error) error

// ClientRateLimiter is the per-IP rate-limiting interface the gRPC server
// calls before dispatching a request. Implementations must be safe for
// concurrent use. The server calls Allow(clientIP) once per incoming HTTP/2
// request — for unary RPCs that's once per request, for streaming RPCs
// that's once per long-lived stream. A false return yields
// RESOURCE_EXHAUSTED without invoking the handler, so a flooded peer cannot
// starve legitimate clients or drive up goroutine count.
//
// Implementations typically reuse the same token-bucket pool as the HTTP
// side so a single IP's HTTP and gRPC traffic share one bucket — set this
// to the same *app.rateLimiter passed to NewRateLimitMiddleware to enforce
// a unified per-IP cap across both protocols.
type ClientRateLimiter interface {
	Allow(clientIP string) bool
}

// Server is a minimal gRPC server over HTTP/2 using net/http.
type Server struct {
	mu       sync.RWMutex
	unary    map[string]Handler
	stream   map[string]StreamHandler
	bistream map[string]BiStreamHandler
	server   *http.Server
	listener net.Listener

	// MaxFrameBytes caps the payload size of a single gRPC frame the server
	// will accept from a peer. Zero falls back to [DefaultMaxFrameBytes].
	// Set to a negative value to disable the cap (not recommended — a
	// malicious peer can claim a 4 GiB body and drive the process into OOM
	// before the handler runs).
	MaxFrameBytes int

	// RateLimiter, when non-nil, is called once per incoming HTTP/2 request
	// with the client IP. A false return yields RESOURCE_EXHAUSTED without
	// invoking the handler. Typically the same *app.rateLimiter used by the
	// HTTP side so a single IP's HTTP + gRPC traffic share one bucket.
	RateLimiter ClientRateLimiter

	// MetricsRegistry, when non-nil, receives gonacos_grpc_requests_total
	// {method,status} increments per RPC. Method is the gRPC path
	// ("/Service/Method"); status is the gRPC status code (0=OK,
	// 8=RESOURCE_EXHAUSTED, 13=INTERNAL, ...). Operators use this to build
	// error-rate panels that distinguish "all requests failing" from "one
	// method failing". The registry is the same one used by the HTTP side,
	// so /metrics exposes both under a single scrape.
	//
	// Latency is also recorded: gonacos_grpc_request_duration_seconds
	// {method} is observed per RPC. Operators use the histogram buckets to
	// compute p50/p90/p99 latency per method in Grafana.
	MetricsRegistry interface {
		Counter(name string, labels map[string]string) CounterMetric
		Histogram(name string, labels map[string]string, buckets []float64) HistogramMetric
	}

	// Logf, when non-nil, is called for diagnostic messages (currently
	// panic recovery). When nil, panics are still recovered but not logged.
	Logf func(format string, args ...any)

	// KeepAlive configures HTTP/2 PING-based liveness detection on the
	// underlying http2.Server. When ReadIdleTimeout > 0, the server sends
	// a PING after that many seconds of connection silence; if no PING ack
	// arrives within PingTimeout, the connection is closed. This catches
	// half-open connections (client crashed but the TCP stack has not
	// noticed) that would otherwise consume a goroutine + file descriptor
	// indefinitely. Recommended production: ReadIdleTimeout=15s,
	// PingTimeout=15s. Zero values disable PINGs (legacy behavior) —
	// connections then rely solely on IdleTimeout, which cannot detect a
	// dead peer.
	KeepAlive KeepAliveConfig

	// ReadFrameTimeout caps the time spent reading a single gRPC frame
	// (header + body) from a peer. When the deadline elapses, the read
	// is aborted and the stream is closed. This closes the slowloris
	// attack on the gRPC body path: without a per-frame deadline, a
	// peer can send a frame body 1 byte at a time and hold the server's
	// goroutine for up to MaxFrameBytes seconds (4 MiB at 1 byte/sec =
	// ~48 days), even with MaxConns capping the total connection count
	// — each held connection still holds a goroutine and a fd.
	// Zero falls back to 30s (see [DefaultReadFrameTimeout]); a
	// negative value disables the cap (not recommended in production).
	// The timeout applies per-frame on both unary and streaming RPCs;
	// a streaming peer that sends a frame every <30s is unaffected.
	ReadFrameTimeout time.Duration

	// MaxConcurrentStreams caps the number of concurrent HTTP/2 streams
	// accepted on a single client connection. Zero falls back to
	// [DefaultMaxConcurrentStreams] (100). A negative value disables the
	// cap (not recommended — Go's http2.Server defaults to 100 anyway,
	// but a peer can still open 100 streams per connection, and across
	// many connections the total in-flight stream count is unbounded).
	//
	// This is the per-connection defense complementary to MaxConns (which
	// caps total connections): a single connection that opens 100 streams
	// each holding a server goroutine + ~4 MiB of frame-buffer headroom
	// can still burn goroutines and memory. Lowering this to e.g. 32
	// tightens the per-connection blast radius; legitimate SDK clients
	// rarely need more than a handful of concurrent streams.
	MaxConcurrentStreams int

	// WriteByteTimeout is the HTTP/2 server-side write timeout: when
	// data is buffered to write but cannot be flushed within this
	// duration, the connection is closed. This is the write-side
	// counterpart to ReadFrameTimeout — ReadFrameTimeout caps the time
	// spent reading a request frame (closing the slowloris-on-body
	// window on the request path), while WriteByteTimeout caps the time
	// spent writing a response frame (closing the symmetric window
	// where a slow client cannot drain the server's response buffer,
	// holding a server goroutine + the buffered response bytes
	// indefinitely). Zero disables (the legacy behavior — relies on
	// IdleTimeout and TCP write deadlines to eventually fail). 30s is
	// a reasonable production default — generous for legitimate clients
	// (a 4 MiB response at ~133 KB/s) while bounding the stuck-write
	// window. The timeout is per-write-byte, not per-RPC: a streaming
	// RPC that continuously writes is unaffected; only a connection
	// that stalls mid-write is closed.
	WriteByteTimeout time.Duration
}

// DefaultReadFrameTimeout is the per-frame read deadline when
// ReadFrameTimeout is zero. 30s is generous for legitimate clients
// (a 4 MiB frame at ~133 KB/s) while bounding the slowloris window.
const DefaultReadFrameTimeout = 30 * time.Second

// DefaultMaxConcurrentStreams is the per-connection concurrent-stream cap
// when MaxConcurrentStreams is zero. 100 matches Go's http2.Server default
// and the gRPC client's advertised limit; legitimate SDK traffic rarely
// exceeds a handful of in-flight streams per connection.
const DefaultMaxConcurrentStreams = 100

// KeepAliveConfig configures HTTP/2 keepalive PINGs. Zero values disable the
// corresponding behavior.
type KeepAliveConfig struct {
	// ReadIdleTimeout is the duration of connection silence after which
	// the server sends an HTTP/2 PING to check the peer is still alive.
	// If zero, no PINGs are sent (legacy behavior). 15s is a reasonable
	// production default — frequent enough to catch dead peers within
	// half a minute, infrequent enough not to add measurable load.
	ReadIdleTimeout time.Duration

	// PingTimeout is how long the server waits for a PING ack before
	// closing the connection. Defaults to 15s when zero and
	// ReadIdleTimeout > 0. Should be >= the round-trip time to the
	// slowest legitimate client; too low will close healthy connections
	// across high-latency links.
	PingTimeout time.Duration
}

// CounterMetric is the subset of the observability.Counter interface the
// gRPC server needs. Declared here so pkg/protocol/grpc doesn't import
// pkg/observability (avoids a cycle when observability later imports
// protocol types for its own dashboards).
type CounterMetric interface {
	Inc()
}

// HistogramMetric is the subset of the observability.Histogram interface
// the gRPC server needs. Observe takes milliseconds to match the
// observability.Histogram convention.
type HistogramMetric interface {
	Observe(ms int64)
}

// NewServer returns an empty gRPC server.
func NewServer() *Server {
	return &Server{
		unary:    map[string]Handler{},
		stream:   map[string]StreamHandler{},
		bistream: map[string]BiStreamHandler{},
	}
}

// maxFrameBytes returns the configured per-frame size cap, falling back to
// DefaultMaxFrameBytes when unset. A negative value disables the cap.
func (s *Server) maxFrameBytes() int {
	if s.MaxFrameBytes != 0 {
		return s.MaxFrameBytes
	}
	return DefaultMaxFrameBytes
}

// readFrameTimeout returns the per-frame read deadline, falling back to
// DefaultReadFrameTimeout when unset. A negative value disables the cap
// (not recommended — re-opens the slowloris-on-body window).
func (s *Server) readFrameTimeout() time.Duration {
	if s.ReadFrameTimeout != 0 {
		return s.ReadFrameTimeout
	}
	return DefaultReadFrameTimeout
}

// maxConcurrentStreams returns the per-connection stream cap, falling back
// to DefaultMaxConcurrentStreams when unset. A negative value disables the
// cap (returns 0 — http2.Server then applies its own default of 100; not
// recommended — re-opens the per-connection goroutine-exhaustion vector
// where a single peer opens 100 streams each holding a goroutine).
func (s *Server) maxConcurrentStreams() int {
	if s.MaxConcurrentStreams < 0 {
		return 0
	}
	if s.MaxConcurrentStreams != 0 {
		return s.MaxConcurrentStreams
	}
	return DefaultMaxConcurrentStreams
}

// recordFrameReadTimeout increments gonacos_grpc_frame_read_timeouts_total
// when a frame read is aborted by the per-frame deadline. The metric is
// the alerting signal for slowloris-on-body attacks against the gRPC path
// — a non-zero rate means peers are stalling mid-frame and getting
// cut off by ReadFrameTimeout. Without it, operators can only see the
// resulting DEADLINE_EXCEEDED statuses in gonacos_grpc_requests_total,
// which conflate slowloris with legitimate slow clients and handler
// timeouts. No-label counter: the attack pattern is "many timeouts
// across the whole gRPC surface", not per-method, so cardinality stays
// at 1. Callers handle a nil MetricsRegistry gracefully (no-op).
func (s *Server) recordFrameReadTimeout() {
	if s.MetricsRegistry == nil {
		return
	}
	s.MetricsRegistry.Counter("gonacos_grpc_frame_read_timeouts_total", nil).Inc()
}

// recordRateLimitRejection increments
// gonacos_rate_limit_rejections_total{protocol="grpc"} when the
// per-IP rate limiter denies a gRPC request. The metric is the
// alerting signal that gRPC rate limiting is firing — without it,
// operators can only infer from gonacos_grpc_requests_total{status=
// "8"} (RESOURCE_EXHAUSTED), which is indirect and breaks if any
// other path ever returns status 8. The {protocol="grpc"} label
// distinguishes gRPC rejections from HTTP rejections (recorded by
// the HTTP rate-limit middleware with protocol="http") so operators
// can see which protocol is being abused. Callers handle a nil
// MetricsRegistry gracefully (no-op).
func (s *Server) recordRateLimitRejection() {
	if s.MetricsRegistry == nil {
		return
	}
	s.MetricsRegistry.Counter("gonacos_rate_limit_rejections_total",
		map[string]string{"protocol": "grpc"},
	).Inc()
}

// RegisterUnary registers a handler for the Request service.
func (s *Server) RegisterUnary(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unary[method] = h
}

// RegisterStream registers a handler for the RequestStream service.
func (s *Server) RegisterStream(method string, h StreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stream[method] = h
}

// RegisterBiStream registers a handler for the BiRequestStream service.
func (s *Server) RegisterBiStream(method string, h BiStreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bistream[method] = h
}

// ListenAndServe starts the gRPC server on the given address. The server
// uses unencrypted HTTP/2 (h2c) so the gRPC client can connect without TLS.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %q: %w", addr, err)
	}
	return s.Serve(ln)
}

// Serve runs the gRPC server on a pre-bound listener using unencrypted
// HTTP/2 (h2c). Useful when the caller wants to pre-bind the port (for
// example, to capture the actual port when binding to :0) before starting
// the server. Takes ownership of ln; closing ln is done via [Server.Shutdown].
func (s *Server) Serve(ln net.Listener) error {
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{
		Handler:           s,
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
		Protocols:         protocols,
	}
	s.configureHTTP2(s.server)
	s.mu.Unlock()
	return s.server.Serve(ln)
}

// ListenAndServeTLS starts the gRPC server with TLS. The server negotiates
// HTTP/2 via ALPN (h2) so standard gRPC clients can connect with TLS enabled.
// certFile and keyFile must be PEM-encoded. Returns an error if either file
// cannot be read or parsed.
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %q: %w", addr, err)
	}
	return s.ServeTLS(ln, certFile, keyFile)
}

// ServeTLS runs the gRPC server with TLS on a pre-bound listener. The server
// negotiates HTTP/2 via ALPN (h2). Useful when the caller wants to pre-bind
// the port (for example, to capture the actual port when binding to :0)
// before starting the server.
func (s *Server) ServeTLS(ln net.Listener, certFile, keyFile string) error {
	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{
		Handler:           s,
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.configureHTTP2(s.server)
	s.mu.Unlock()
	return s.server.ServeTLS(ln, certFile, keyFile)
}

// ServeTLSConfig runs the gRPC server with TLS on a pre-bound listener using
// a pre-configured *tls.Config. This is the hot-reload path: callers pass a
// Config whose GetCertificate callback reloads the cert from disk on file
// change, so certificate rotation does not require a restart. The Config
// must negotiate HTTP/2 via ALPN (h2) — callers should use
// http2.ConfigureServer or append "h2" to NextProtos.
func (s *Server) ServeTLSConfig(ln net.Listener, cfg *tls.Config) error {
	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{
		Handler:           s,
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         cfg,
	}
	s.configureHTTP2(s.server)
	s.mu.Unlock()
	// Empty cert/key paths: http.Server.ServeTLS uses TLSConfig instead.
	return s.server.ServeTLS(ln, "", "")
}

// configureHTTP2 wires [Server.KeepAlive] and [Server.MaxConcurrentStreams]
// into the http.Server's HTTP/2 transport. Called under s.mu. When
// ReadIdleTimeout > 0, the http2 server sends a PING after that duration of
// silence and closes the connection if no ack arrives within PingTimeout
// (defaulting to 15s). This catches half-open connections — a client that
// crashed without sending a FIN keeps its server-side goroutine + file
// descriptor alive indefinitely without PINGs, since TCP only learns the
// peer is gone when it tries to write.
//
// MaxConcurrentStreams is always set: a negative config disables the cap
// (http2.Server then uses its own default of 100), zero falls back to
// DefaultMaxConcurrentStreams (also 100), and any positive value is
// applied as-is. Lowering it tightens the per-connection blast radius
// when a peer opens many in-flight streams.
//
// ConfigureServer is a no-op when the http.Server already has an http2 conf
// attached, so calling it on a server that was previously configured is
// safe.
func (s *Server) configureHTTP2(srv *http.Server) {
	h2s := &http2.Server{
		IdleTimeout:          srv.IdleTimeout,
		MaxConcurrentStreams: uint32(s.maxConcurrentStreams()),
		WriteByteTimeout:     s.WriteByteTimeout,
	}
	if s.KeepAlive.ReadIdleTimeout > 0 {
		h2s.ReadIdleTimeout = s.KeepAlive.ReadIdleTimeout
		if s.KeepAlive.PingTimeout > 0 {
			h2s.PingTimeout = s.KeepAlive.PingTimeout
		}
	}
	_ = http2.ConfigureServer(srv, h2s)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	srv := s.server
	s.mu.RUnlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Addr returns the listen address; nil before ListenAndServe.
func (s *Server) Addr() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// clientIPKey is the context key used to carry the remote client IP from the
// HTTP layer into the gRPC handler. Handlers use ClientIPFromContext to
// recover the IP for listener tracking and beta-aware config queries.
type clientIPKey struct{}

// ClientIPFromContext returns the client IP injected by the server, or "" if
// the context was not created by ServeHTTP.
func ClientIPFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(clientIPKey{}).(string); ok {
		return v
	}
	return ""
}

// ServeHTTP routes the incoming HTTP/2 request to the registered handler.
// The path format is /{Service}/{Method}. When Logf is set, each request is
// logged with method, path, grpc-status, duration, and remote address.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/grpc") {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		writeGRPCStatus(w, StatusUnimplemented, "unknown path: "+r.URL.Path)
		return
	}
	service, method := parts[0], parts[1]
	full := service + "/" + method

	s.mu.RLock()
	unary, hasUnary := s.unary[full]
	stream, hasStream := s.stream[full]
	bistream, hasBi := s.bistream[full]
	s.mu.RUnlock()

	if !hasUnary && !hasStream && !hasBi {
		writeGRPCStatus(w, StatusUnimplemented, "unknown method: "+full)
		return
	}

	ctx := context.WithValue(r.Context(), clientIPKey{}, clientIPFromRequest(r))

	start := time.Now()

	// Per-IP rate limiting. A false return yields RESOURCE_EXHAUSTED without
	// invoking the handler, so a flooded peer cannot starve legitimate
	// clients or drive up goroutine count. Unary RPCs consume one token per
	// request; streaming RPCs consume one token per long-lived stream.
	if s.RateLimiter != nil {
		ip := ClientIPFromContext(ctx)
		if !s.RateLimiter.Allow(ip) {
			s.recordRateLimitRejection()
			writeGRPCStatus(w, StatusResourceExhausted, "rate limit exceeded for client IP")
			if s.Logf != nil {
				s.Logf("grpc %s %s status=%d duration=%s remote=%s (rate-limited)",
					r.Method, r.URL.Path, StatusResourceExhausted,
					formatGRPCDuration(time.Since(start)), r.RemoteAddr)
			}
			return
		}
	}
	switch {
	case hasUnary:
		s.handleUnary(ctx, w, r, unary)
	case hasStream:
		s.handleStream(ctx, w, r, stream)
	case hasBi:
		s.handleBiStream(ctx, w, r, bistream)
	}
	if s.Logf != nil {
		status := w.Header().Get("grpc-status")
		if status == "" {
			status = "?"
		}
		s.Logf("grpc %s %s status=%s duration=%s remote=%s",
			r.Method, r.URL.Path, status, formatGRPCDuration(time.Since(start)), r.RemoteAddr)
	}
	if s.MetricsRegistry != nil {
		status := w.Header().Get("grpc-status")
		if status == "" {
			status = "?"
		}
		s.MetricsRegistry.Counter("gonacos_grpc_requests_total", map[string]string{
			"method": r.URL.Path,
			"status": status,
		}).Inc()
		s.MetricsRegistry.Histogram("gonacos_grpc_request_duration_seconds",
			map[string]string{"method": r.URL.Path},
			grpcLatencyBuckets,
		).Observe(time.Since(start).Milliseconds())
	}
}

// formatGRPCDuration trims sub-millisecond noise for log readability, matching
// the HTTP access log format in pkg/server/request_log.go.
func formatGRPCDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return strconv.Itoa(int(d.Nanoseconds())) + "ns"
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	default:
		return fmt.Sprintf("%.3fs", d.Seconds())
	}
}

func (s *Server) handleUnary(ctx context.Context, w http.ResponseWriter, r *http.Request, h Handler) {
	defer recoverGRPC(s, w, nil, r)
	frame, err := ReadFrameWithLimitAndTimeout(r.Body, s.maxFrameBytes(), s.readFrameTimeout())
	if err != nil && !errors.Is(err, io.EOF) {
		code := StatusInternal
		switch {
		case errors.Is(err, ErrFrameTooLarge):
			code = StatusResourceExhausted
		case errors.Is(err, ErrReadFrameTimeout):
			code = StatusDeadlineExceeded
			s.recordFrameReadTimeout()
		}
		writeGRPCStatus(w, code, "read frame: "+err.Error())
		return
	}
	req, err := DecodePayload(frame.Payload)
	if err != nil {
		writeGRPCStatus(w, StatusInternal, "decode payload: "+err.Error())
		return
	}
	resp, err := h(ctx, req)
	if err != nil {
		code, msg := statusFromError(err)
		writeGRPCStatus(w, code, msg)
		return
	}
	writeGRPCPayload(w, resp)
}

func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, r *http.Request, h StreamHandler) {
	defer func() {
		if rv := recover(); rv != nil {
			s.logPanic(rv, r)
			// Headers may or may not be written yet. tryStreamStatus
			// sets the grpc-status trailer if the response is still
			// writable; otherwise it's a no-op (the connection will
			// be torn down by the runtime).
			tryStreamStatus(w, StatusInternal, "internal server error")
		}
	}()
	frame, err := ReadFrameWithLimitAndTimeout(r.Body, s.maxFrameBytes(), s.readFrameTimeout())
	if err != nil && !errors.Is(err, io.EOF) {
		code := StatusInternal
		switch {
		case errors.Is(err, ErrFrameTooLarge):
			code = StatusResourceExhausted
		case errors.Is(err, ErrReadFrameTimeout):
			code = StatusDeadlineExceeded
			s.recordFrameReadTimeout()
		}
		writeGRPCStatus(w, code, "read frame: "+err.Error())
		return
	}
	req, err := DecodePayload(frame.Payload)
	if err != nil {
		writeGRPCStatus(w, StatusInternal, "decode payload: "+err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGRPCStatus(w, StatusInternal, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(p Payload) error {
		return writeGRPCStreamFrame(w, flusher, p)
	}
	if err := h(ctx, req, send); err != nil {
		code, msg := statusFromError(err)
		writeGRPCStreamStatus(w, flusher, code, msg)
		return
	}
	writeGRPCStreamStatus(w, flusher, StatusOK, "")
}

func (s *Server) handleBiStream(ctx context.Context, w http.ResponseWriter, r *http.Request, h BiStreamHandler) {
	defer func() {
		if rv := recover(); rv != nil {
			s.logPanic(rv, r)
			tryStreamStatus(w, StatusInternal, "internal server error")
		}
	}()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGRPCStatus(w, StatusInternal, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	recv := func() (Payload, error) {
		frame, err := ReadFrameWithLimitAndTimeout(r.Body, s.maxFrameBytes(), s.readFrameTimeout())
		if err != nil {
			if errors.Is(err, ErrReadFrameTimeout) {
				s.recordFrameReadTimeout()
			}
			return Payload{}, err
		}
		return DecodePayload(frame.Payload)
	}
	send := func(p Payload) error {
		return writeGRPCStreamFrame(w, flusher, p)
	}
	if err := h(ctx, recv, send); err != nil {
		code, msg := statusFromError(err)
		writeGRPCStreamStatus(w, flusher, code, msg)
		return
	}
	writeGRPCStreamStatus(w, flusher, StatusOK, "")
}

func statusFromError(err error) (int, string) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code, se.Message
	}
	return StatusInternal, err.Error()
}

// recoverGRPC is the deferred recover() for unary handlers. On panic it
// logs (if Logf is set) and writes a gRPC INTERNAL status. For unary calls
// the response hasn't started yet, so writeGRPCStatus can always emit a
// proper status.
func recoverGRPC(s *Server, w http.ResponseWriter, _ any, r *http.Request) {
	rv := recover()
	if rv == nil {
		return
	}
	if s != nil {
		s.logPanic(rv, r)
	}
	writeGRPCStatus(w, StatusInternal, "internal server error")
}

// logPanic emits a single log line with the stack trace when Logf is set,
// and increments gonacos_grpc_panics_total{method} when MetricsRegistry is
// set. The metric is the alerting signal for handler crashes — a non-zero
// rate pages on-call (deployed bug or malformed request the handler can't
// process). The log line carries the stack for diagnosis.
//
// The two paths are independent: a nil Logf still records the metric (so
// silent panics are still visible in monitoring), and a nil
// MetricsRegistry still logs (so panics are still diagnosable from logs).
func (s *Server) logPanic(rv any, r *http.Request) {
	if s.MetricsRegistry != nil {
		s.MetricsRegistry.Counter("gonacos_grpc_panics_total",
			map[string]string{"method": r.URL.Path},
		).Inc()
	}
	if s.Logf == nil {
		return
	}
	s.Logf("grpc panic recovered: %v\n%s %s %s", rv, debug.Stack(), r.Method, r.URL.Path)
}

// tryStreamStatus attempts to set the grpc-status trailer on a streaming
// response that may have already started. If headers were already flushed,
// the trailer may not reach the client, but we still try rather than
// silently dropping the panic.
func tryStreamStatus(w http.ResponseWriter, code int, message string) {
	w.Header().Set("grpc-status", strconv.Itoa(code))
	if message != "" {
		w.Header().Set("grpc-message", message)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeGRPCStatus(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/grpc")
	// For status-only responses (no body), set grpc-status as a regular
	// header so both HTTP/1.1 and HTTP/2 clients can read it from the
	// response headers without needing to parse trailers.
	w.Header().Set("grpc-status", strconv.Itoa(code))
	if message != "" {
		w.Header().Set("grpc-message", message)
	}
	w.WriteHeader(http.StatusOK)
}

func writeGRPCPayload(w http.ResponseWriter, p Payload) {
	w.Header().Set("Content-Type", "application/grpc")
	// Set grpc-status as a header so HTTP/1.1 clients can read it before
	// consuming the body, and also declare it as a trailer for HTTP/2 gRPC
	// clients that expect it there.
	w.Header().Set("grpc-status", strconv.Itoa(StatusOK))
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	_ = WriteFrame(w, Frame{Payload: p.Encode()})
	w.Header().Set("grpc-status", strconv.Itoa(StatusOK))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeGRPCStreamFrame(w http.ResponseWriter, flusher http.Flusher, p Payload) error {
	if err := WriteFrame(w, Frame{Payload: p.Encode()}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeGRPCStreamStatus(w http.ResponseWriter, flusher http.Flusher, code int, message string) {
	w.Header().Set("grpc-status", strconv.Itoa(code))
	if message != "" {
		w.Header().Set("grpc-message", message)
	}
	flusher.Flush()
}

// DefaultServer is the package-level gRPC server used by the app.
var (
	defaultServer     *Server
	defaultServerOnce sync.Once
)

// clientIPFromRequest extracts the remote IP from an HTTP request, honoring
// the X-Forwarded-For and X-Real-IP headers so requests routed through a
// proxy still report the originating client.
func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// DefaultServer returns the shared gRPC server.
func DefaultServer() *Server {
	defaultServerOnce.Do(func() {
		defaultServer = NewServer()
	})
	return defaultServer
}

// ServeInBackground launches the gRPC server on addr in a goroutine. Errors
// are logged; the caller should use Shutdown to stop.
func ServeInBackground(addr string) {
	srv := DefaultServer()
	go func() {
		if err := srv.ListenAndServe(addr); err != nil && err != http.ErrServerClosed {
			log.Printf("grpc server error: %v", err)
		}
	}()
	// Give the listener a moment to bind so tests can connect immediately.
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
}

// ShutdownDefault stops the shared gRPC server.
func ShutdownDefault(ctx context.Context) error {
	if defaultServer == nil {
		return nil
	}
	return defaultServer.Shutdown(ctx)
}
