package server

import (
	"context"
	"net"
	"path/filepath"
	"testing"
)

// TestHTTPServerHTTP2HardeningApplied verifies that when TLS is enabled,
// the HTTP server has http2.ConfigureServer called on it — attaching
// the h2 ALPN handler so the server actually negotiates HTTP/2 with
// browsers, and applying the MaxConcurrentStreams / WriteByteTimeout
// caps from the GRPC* knobs (they are HTTP/2 protocol-level configs,
// not gRPC-specific).
//
// Without this wiring, the HTTP/2 path on the HTTP listener (advertised
// via NextProtos ["h2","http/1.1"] in TLSConfig) would use Go's
// defaults: MaxConcurrentStreams uncapped (a peer can open 100 streams
// per connection, holding 100 goroutines + frame-buffer headroom),
// WriteByteTimeout disabled (a slow client cannot drain the server's
// response buffer, holding a goroutine + buffered bytes indefinitely).
//
// We assert via httpSrv.TLSNextProto: http2.ConfigureServer registers
// a "h2" handler there, which is the actual HTTP/2 wire-up. The
// MaxConcurrentStreams / WriteByteTimeout values are applied to the
// http2.Server passed to ConfigureServer — verifying them via the
// unexported http2 conf field is not possible from this package, but
// the h2 handler presence is the binary signal that ConfigureServer
// ran.
func TestHTTPServerHTTP2HardeningApplied(t *testing.T) {
	httpLn := reserveFreePort(t)
	grpcLn := reserveFreePort(t)

	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	writeTestCert(t, certPath, keyPath)

	srv, err := New(
		WithAddr(httpLn),
		WithGRPCAddr(grpcLn),
		WithRoot("."),
		WithTLS(certPath, keyPath),
	)
	if err != nil {
		t.Fatalf("New with TLS: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	if srv.httpSrv == nil {
		t.Fatal("httpSrv is nil")
	}
	if srv.httpSrv.TLSConfig == nil {
		t.Fatal("TLSConfig is nil — TLS not enabled")
	}
	// NextProtos must advertise h2 so browsers negotiate HTTP/2 via ALPN.
	foundH2 := false
	for _, p := range srv.httpSrv.TLSConfig.NextProtos {
		if p == "h2" {
			foundH2 = true
			break
		}
	}
	if !foundH2 {
		t.Fatalf("NextProtos missing h2: %v", srv.httpSrv.TLSConfig.NextProtos)
	}
	// http2.ConfigureServer registers an "h2" entry in TLSNextProto —
	// the actual HTTP/2 wire-up. Without it, the server would not
	// negotiate HTTP/2 even though NextProtos advertises h2.
	if _, ok := srv.httpSrv.TLSNextProto["h2"]; !ok {
		t.Fatal("TLSNextProto missing h2 handler — http2.ConfigureServer was not called")
	}
}

// TestHTTPServerHTTP2HardeningNotAppliedWithoutTLS verifies that when
// TLS is not enabled, the HTTP server does not have HTTP/2 wired up —
// the HTTP/2 path requires TLS (h2 via ALPN). Without TLS the server
// falls back to HTTP/1.1, and http2.ConfigureServer is not called.
func TestHTTPServerHTTP2HardeningNotAppliedWithoutTLS(t *testing.T) {
	httpLn := reserveFreePort(t)
	grpcLn := reserveFreePort(t)

	srv, err := New(
		WithAddr(httpLn),
		WithGRPCAddr(grpcLn),
		WithRoot("."),
	)
	if err != nil {
		t.Fatalf("New without TLS: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	if srv.httpSrv == nil {
		t.Fatal("httpSrv is nil")
	}
	if srv.httpSrv.TLSConfig != nil {
		t.Errorf("TLSConfig should be nil without TLS, got %v", srv.httpSrv.TLSConfig)
	}
	if len(srv.httpSrv.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto should be empty without TLS, got %v", srv.httpSrv.TLSNextProto)
	}
}

// TestHTTPServerHTTP2HardeningMaxConcurrentStreamsNeg verifies that a
// negative GRPCMaxConcurrentStreams value (disabled on the gRPC side)
// still allows http2.ConfigureServer to run — the h2 handler is
// registered regardless of the MaxConcurrentStreams value. The cap
// translates to MaxConcurrentStreams=0 on the HTTP/2 path, which lets
// Go's http2 stack apply its own 100 default.
func TestHTTPServerHTTP2HardeningMaxConcurrentStreamsNeg(t *testing.T) {
	httpLn := reserveFreePort(t)
	grpcLn := reserveFreePort(t)

	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	writeTestCert(t, certPath, keyPath)

	srv, err := New(
		WithAddr(httpLn),
		WithGRPCAddr(grpcLn),
		WithRoot("."),
		WithTLS(certPath, keyPath),
		WithGRPCMaxConcurrentStreams(-1),
	)
	if err != nil {
		t.Fatalf("New with TLS: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	// The h2 handler should still be registered — ConfigureServer runs
	// regardless of the MaxConcurrentStreams value.
	if _, ok := srv.httpSrv.TLSNextProto["h2"]; !ok {
		t.Fatal("TLSNextProto missing h2 handler with negative MaxConcurrentStreams")
	}
}

// reserveFreePort binds a free port and returns its address so tests
// don't hit EADDRINUSE.
func reserveFreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
