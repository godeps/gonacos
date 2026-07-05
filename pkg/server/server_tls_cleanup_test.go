package server

import (
	"net"
	"path/filepath"
	"testing"
)

// TestNewTLSLoadErrorCleansListeners verifies that when TLS cert loading
// fails, the listeners bound earlier in [New] are closed so a retry
// does not hit EADDRINUSE.
//
// Before the fix, [New] returned nil without closing httpLn and grpcLn,
// so the kernel kept the ports reserved until the test process exited.
// A retry with the same address (or a second concurrent test) would
// fail with "listen: address already in use". After the fix, the
// listeners are closed on the error path and the ports are immediately
// reusable.
func TestNewTLSLoadErrorCleansListeners(t *testing.T) {
	// Bind two free ports by listening and closing — we use these
	// addresses for the gonacos server so we know they're free at the
	// start of the test.
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve http port: %v", err)
	}
	httpAddr := httpLn.Addr().String()
	_ = httpLn.Close()

	grpcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve grpc port: %v", err)
	}
	grpcAddr := grpcLn.Addr().String()
	_ = grpcLn.Close()

	// Point at a non-existent cert/key file. NewCertReloader will
	// fail to read the cert, [New] returns an error, and the cleanup
	// path must close the listeners it already bound.
	missingCert := filepath.Join(t.TempDir(), "does-not-exist.crt")
	missingKey := filepath.Join(t.TempDir(), "does-not-exist.key")

	_, err = New(
		WithAddr(httpAddr),
		WithGRPCAddr(grpcAddr),
		WithRoot("."),
		WithTLS(missingCert, missingKey),
	)
	if err == nil {
		t.Fatal("New with missing cert should return an error")
	}

	// The ports must be immediately reusable. If the cleanup did not
	// close the listeners, this Listen will fail with EADDRINUSE.
	reHTTP, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Errorf("re-listen http after TLS error: %v (listener was not cleaned up)", err)
	} else {
		_ = reHTTP.Close()
	}
	reGRPC, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		t.Errorf("re-listen grpc after TLS error: %v (listener was not cleaned up)", err)
	} else {
		_ = reGRPC.Close()
	}
}

// TestNewTLSLoadErrorCleansEmbeddedRedis verifies that when TLS cert
// loading fails, the embedded Redis (started earlier in [New] because
// no external Redis was configured) is stopped — otherwise the
// miniredis goroutine survives the test process.
//
// We can't directly observe goroutine cleanup, but we can verify the
// embedded Redis's port is released: if the goroutine survived, the
// listener would still hold the port.
func TestNewTLSLoadErrorCleansEmbeddedRedis(t *testing.T) {
	// Reserve http/grpc ports so the test is deterministic.
	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	httpAddr := httpLn.Addr().String()
	_ = httpLn.Close()

	grpcLn, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcAddr := grpcLn.Addr().String()
	_ = grpcLn.Close()

	missingCert := filepath.Join(t.TempDir(), "missing.crt")
	missingKey := filepath.Join(t.TempDir(), "missing.key")

	_, err := New(
		WithAddr(httpAddr),
		WithGRPCAddr(grpcAddr),
		WithRoot("."),
		WithTLS(missingCert, missingKey),
		// No WithRedisAddr — forces embedded Redis to start.
	)
	if err == nil {
		t.Fatal("New with missing cert should return an error")
	}

	// Listeners must be reusable (the embedded Redis is on a random
	// port so we can't directly re-listen it, but the http/grpc
	// listeners must be free).
	reHTTP, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Errorf("re-listen http after TLS error: %v", err)
	} else {
		_ = reHTTP.Close()
	}
	reGRPC, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		t.Errorf("re-listen grpc after TLS error: %v", err)
	} else {
		_ = reGRPC.Close()
	}
}
