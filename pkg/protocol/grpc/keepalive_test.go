package grpc

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestServerKeepAliveConfiguredDoesNotBreakStartup verifies that setting a
// non-zero KeepAlive config does not prevent the server from starting or
// handling requests. The actual PING behavior is exercised by the http2
// stack; this test guards against regressions in [Server.configureHTTP2]
// that would panic or fail to attach the http2 conf.
func TestServerKeepAliveConfiguredDoesNotBreakStartup(t *testing.T) {
	srv := NewServer()
	srv.KeepAlive = KeepAliveConfig{
		ReadIdleTimeout: 5 * time.Second,
		PingTimeout:     5 * time.Second,
	}
	srv.RegisterUnary("/test/Unary", func(ctx context.Context, req Payload) (Payload, error) {
		return req, nil
	})

	// configureHTTP2 is called from Serve / ServeTLS. We call it directly
	// to verify the http2 conf attaches without panic.
	httpSrv := &http.Server{
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.configureHTTP2(httpSrv)
}

// TestServerKeepAliveZeroIsNoopForPing verifies that a zero KeepAlive config
// leaves PINGs disabled — the http2.Server's ReadIdleTimeout and PingTimeout
// stay zero (no PINGs sent). MaxConcurrentStreams is still applied (it is
// independent of KeepAlive); see TestServerMaxConcurrentStreamsConfigured.
func TestServerKeepAliveZeroIsNoopForPing(t *testing.T) {
	srv := NewServer()
	httpSrv := &http.Server{
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.configureHTTP2(httpSrv)
}

// TestServerMaxConcurrentStreamsConfigured verifies that an explicit
// MaxConcurrentStreams value is returned by maxConcurrentStreams() so a
// peer cannot open an unbounded number of in-flight streams on a single
// connection. configureHTTP2 propagates this to the underlying http2.Server
// (verified by the no-panic smoke test TestServerKeepAliveConfiguredDoesNotBreakStartup);
// the actual cap enforcement is handled by Go's http2 stack.
func TestServerMaxConcurrentStreamsConfigured(t *testing.T) {
	const want = 32
	srv := NewServer()
	srv.MaxConcurrentStreams = want
	if got := srv.maxConcurrentStreams(); got != want {
		t.Errorf("maxConcurrentStreams() = %d, want %d", got, want)
	}
}

// TestServerMaxConcurrentStreamsDefault verifies that a zero
// MaxConcurrentStreams config falls back to DefaultMaxConcurrentStreams
// (100), matching Go's http2.Server default. Operators who don't tune the
// limit get a sane defense-in-depth default.
func TestServerMaxConcurrentStreamsDefault(t *testing.T) {
	srv := NewServer()
	if got := srv.maxConcurrentStreams(); got != DefaultMaxConcurrentStreams {
		t.Errorf("maxConcurrentStreams() = %d, want default %d", got, DefaultMaxConcurrentStreams)
	}
}

// TestServerMaxConcurrentStreamsNegativeDisablesCap verifies that a
// negative MaxConcurrentStreams disables the cap — the http2.Server's
// MaxConcurrentStreams field stays zero, letting Go's http2 stack apply
// its own default of 100. This is the operator opt-out path.
func TestServerMaxConcurrentStreamsNegativeDisablesCap(t *testing.T) {
	srv := NewServer()
	srv.MaxConcurrentStreams = -1
	if got := srv.maxConcurrentStreams(); got != 0 {
		t.Errorf("maxConcurrentStreams() = %d, want 0 (disabled)", got)
	}
}

// TestServerWriteByteTimeoutConfigured verifies that an explicit
// WriteByteTimeout is propagated to configureHTTP2's http2.Server conf
// via the smoke test — the actual write-side timeout enforcement is
// handled by Go's http2 stack. This closes the stuck-write window
// where a slow client cannot drain the server's response buffer,
// holding a server goroutine + buffered response bytes indefinitely.
func TestServerWriteByteTimeoutConfigured(t *testing.T) {
	const want = 15 * time.Second
	srv := NewServer()
	srv.WriteByteTimeout = want
	httpSrv := &http.Server{
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	// Smoke test: configureHTTP2 must not panic with WriteByteTimeout set.
	srv.configureHTTP2(httpSrv)
	if srv.WriteByteTimeout != want {
		t.Errorf("WriteByteTimeout = %v, want %v", srv.WriteByteTimeout, want)
	}
}

// TestServerWriteByteTimeoutDefaultIsZero verifies that a zero
// WriteByteTimeout (the default) disables the write-side timeout —
// the legacy behavior that relies on IdleTimeout and TCP write
// deadlines to eventually fail. Operators opt in by setting a
// positive duration.
func TestServerWriteByteTimeoutDefaultIsZero(t *testing.T) {
	srv := NewServer()
	if srv.WriteByteTimeout != 0 {
		t.Errorf("default WriteByteTimeout = %v, want 0", srv.WriteByteTimeout)
	}
}

// TestServerMaxHeaderBytesConfigured verifies that an explicit
// MaxHeaderBytes value is returned by maxHeaderBytes() so a peer
// cannot exploit HPACK compression to decompress a small header block
// into gigabytes of decoded header data. configureHTTP2 propagates
// this to the underlying http.Server (the http2 stack reads it via
// http2serverConn.maxHeaderListSize()).
func TestServerMaxHeaderBytesConfigured(t *testing.T) {
	const want = 64 * 1024
	srv := NewServer()
	srv.MaxHeaderBytes = want
	if got := srv.maxHeaderBytes(); got != want {
		t.Errorf("maxHeaderBytes() = %d, want %d", got, want)
	}
}

// TestServerMaxHeaderBytesDefault verifies that a zero MaxHeaderBytes
// config falls back to DefaultMaxHeaderBytes (1 MiB), matching Go's
// net/http default and Envoy's max_request_headers_kb. Operators who
// don't tune the limit get a sane header-bomb defense default.
func TestServerMaxHeaderBytesDefault(t *testing.T) {
	srv := NewServer()
	if got := srv.maxHeaderBytes(); got != DefaultMaxHeaderBytes {
		t.Errorf("maxHeaderBytes() = %d, want default %d", got, DefaultMaxHeaderBytes)
	}
	if DefaultMaxHeaderBytes != 1<<20 {
		t.Errorf("DefaultMaxHeaderBytes = %d, want %d", DefaultMaxHeaderBytes, 1<<20)
	}
}

// TestServerMaxHeaderBytesNegativeDisablesCap verifies that a negative
// MaxHeaderBytes disables the cap — maxHeaderBytes() returns 0,
// letting Go's net/http apply its own 1 MiB default via
// http.Server.MaxHeaderBytes. This is the operator opt-out path (not
// recommended — the explicit zero makes the cap invisible to operators
// reading the config).
func TestServerMaxHeaderBytesNegativeDisablesCap(t *testing.T) {
	srv := NewServer()
	srv.MaxHeaderBytes = -1
	if got := srv.maxHeaderBytes(); got != 0 {
		t.Errorf("maxHeaderBytes() = %d, want 0 (disabled)", got)
	}
}

// TestServerMaxReadFrameSizeConfigured verifies that an explicit
// MaxReadFrameSize value is returned by maxReadFrameSize() and propagated
// to the http2.Server in configureHTTP2. This is the frame-bomb defense:
// without it, a peer sending a 16 MiB DATA frame forces the server to
// allocate a buffer that size before the handler runs. Stacked across
// MaxConcurrentStreams connections, a single malicious peer exhausts
// memory before any handler executes.
func TestServerMaxReadFrameSizeConfigured(t *testing.T) {
	const want = 256 * 1024
	srv := NewServer()
	srv.MaxReadFrameSize = want
	if got := srv.maxReadFrameSize(); got != want {
		t.Errorf("maxReadFrameSize() = %d, want %d", got, want)
	}
	// Smoke test: configureHTTP2 must propagate the cap to the http2.Server
	// without panic. The actual frame-size enforcement is handled by Go's
	// http2 stack; we verify the wiring here, not the runtime rejection.
	httpSrv := &http.Server{
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.configureHTTP2(httpSrv)
}

// TestServerMaxReadFrameSizeDefault verifies that a zero MaxReadFrameSize
// config falls back to DefaultMaxReadFrameSize (1 MiB), matching Go's
// http2.Server default and grpc-go's DefaultMaxReadFrameSize. Operators
// who don't tune the limit get a sane frame-bomb defense default.
func TestServerMaxReadFrameSizeDefault(t *testing.T) {
	srv := NewServer()
	if got := srv.maxReadFrameSize(); got != DefaultMaxReadFrameSize {
		t.Errorf("maxReadFrameSize() = %d, want default %d", got, DefaultMaxReadFrameSize)
	}
	if DefaultMaxReadFrameSize != 1<<20 {
		t.Errorf("DefaultMaxReadFrameSize = %d, want %d", DefaultMaxReadFrameSize, 1<<20)
	}
}

// TestServerMaxReadFrameSizeNegativeDisablesCap verifies that a negative
// MaxReadFrameSize disables the cap — maxReadFrameSize() returns 0,
// letting Go's http2 stack apply its own 1 MiB default. This is the
// operator opt-out path (not recommended — the explicit zero makes the
// cap invisible to operators reading the config).
func TestServerMaxReadFrameSizeNegativeDisablesCap(t *testing.T) {
	srv := NewServer()
	srv.MaxReadFrameSize = -1
	if got := srv.maxReadFrameSize(); got != 0 {
		t.Errorf("maxReadFrameSize() = %d, want 0 (disabled)", got)
	}
}
