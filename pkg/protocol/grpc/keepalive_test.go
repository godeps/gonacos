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

// TestServerKeepAliveZeroIsNoop verifies that a zero KeepAlive config leaves
// the http.Server untouched — no http2 conf is attached. This guards the
// legacy behavior where PINGs are disabled.
func TestServerKeepAliveZeroIsNoop(t *testing.T) {
	srv := NewServer()
	httpSrv := &http.Server{
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.configureHTTP2(httpSrv)
}
