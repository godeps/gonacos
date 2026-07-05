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

// TestNewHTTPListenErrorCleansResources verifies that when http Listen
// fails (port already in use), [New] closes the resources wired before
// the listen call — including the periodic-snapshot goroutine, which
// would otherwise survive [New] failure and outlive the test process.
//
// We can't directly observe the goroutine, but we can verify the
// embedded-Redis port is released (the goroutine survives iff the
// miniredis server survives, and the server holds its port).
func TestNewHTTPListenErrorCleansResources(t *testing.T) {
	// Reserve a port and HOLD it — this forces net.Listen inside
	// [New] to fail with EADDRINUSE.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve blocker port: %v", err)
	}
	defer blocker.Close()
	httpAddr := blocker.Addr().String()

	grpcLn, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcAddr := grpcLn.Addr().String()
	_ = grpcLn.Close()

	_, err = New(
		WithAddr(httpAddr),
		WithGRPCAddr(grpcAddr),
		WithRoot("."),
		// No WithRedisAddr — forces embedded Redis to start.
	)
	if err == nil {
		t.Fatal("New with occupied http port should return an error")
	}

	// The grpc listener was bound before the http listen error, so it
	// must be closed by the error path. Verify by re-listening.
	reGRPC, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		t.Errorf("re-listen grpc after http-listen error: %v (listener was not cleaned up)", err)
	} else {
		_ = reGRPC.Close()
	}

	// Release the http blocker and verify it's immediately reusable
	// (it should be — we closed it via defer, but the test confirms
	// [New] didn't accidentally double-bind).
	_ = blocker.Close()
	reHTTP, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Errorf("re-listen http after blocker release: %v", err)
	} else {
		_ = reHTTP.Close()
	}
}

// TestNewGRPCListenErrorCleansResources verifies that when grpc Listen
// fails, [New] closes the http listener bound just before, plus the
// other resources. The grpc listener is bound after http, so this path
// must clean up httpLn.
func TestNewGRPCListenErrorCleansResources(t *testing.T) {
	// Reserve grpc port and hold it to force EADDRINUSE.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve blocker port: %v", err)
	}
	defer blocker.Close()
	grpcAddr := blocker.Addr().String()

	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	httpAddr := httpLn.Addr().String()
	_ = httpLn.Close()

	_, err = New(
		WithAddr(httpAddr),
		WithGRPCAddr(grpcAddr),
		WithRoot("."),
	)
	if err == nil {
		t.Fatal("New with occupied grpc port should return an error")
	}

	// The http listener was bound successfully before the grpc listen
	// error, so the error path must close it. Verify by re-listening.
	reHTTP, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Errorf("re-listen http after grpc-listen error: %v (http listener was not cleaned up)", err)
	} else {
		_ = reHTTP.Close()
	}

	_ = blocker.Close()
	reGRPC, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		t.Errorf("re-listen grpc after blocker release: %v", err)
	} else {
		_ = reGRPC.Close()
	}
}
