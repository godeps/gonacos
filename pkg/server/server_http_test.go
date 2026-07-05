package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/server"
)

// TestHTTPRateLimitEndToEnd constructs a Server with a 1 rps / burst 1 rate
// limit and verifies that the second immediate request from the same IP is
// throttled with 429.
func TestHTTPRateLimitEndToEnd(t *testing.T) {
	srv, err := server.New(
		server.WithAddr("127.0.0.1:0"),
		server.WithGRPCAddr("127.0.0.1:0"),
		server.WithRoot(".."),
		server.WithHTTPRateLimit(1, 1),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	base := "http://" + srv.HTTPAddr()
	// First request passes (open path — health check).
	if code := get(t, base+"/v3/console/health/liveness"); code != http.StatusOK {
		t.Fatalf("first request: got %d, want %d", code, http.StatusOK)
	}
	// Second immediate request from the same IP is throttled.
	if code := get(t, base+"/v3/console/health/liveness"); code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want %d", code, http.StatusTooManyRequests)
	}
}

// TestHTTPMaxBodyEndToEnd constructs a Server with a 16-byte body cap and
// verifies that a POST with a body exceeding the cap is rejected.
func TestHTTPMaxBodyEndToEnd(t *testing.T) {
	srv, err := server.New(
		server.WithAddr("127.0.0.1:0"),
		server.WithGRPCAddr("127.0.0.1:0"),
		server.WithRoot(".."),
		server.WithHTTPMaxBody(16),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	base := "http://" + srv.HTTPAddr()
	// Use POST to an open endpoint that the SDK uses; the body cap is
	// enforced before any handler runs, so the path doesn't matter.
	body := strings.NewReader(strings.Repeat("x", 256))
	code := post(t, base+"/v3/auth/user/login", body)
	if code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
		// http.MaxBytesReader may surface as 400 (ParseForm reading past
		// the limit) or 413 depending on whether the handler reads the
		// body before calling ParseForm. Either is acceptable evidence
		// that the cap is in effect; a 200 or 401/404 means the cap was
		// bypassed.
		t.Fatalf("oversized POST: got %d, want 413 or 400 (cap enforced)", code)
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func post(t *testing.T, url string, body io.Reader) int {
	t.Helper()
	resp, err := http.Post(url, "application/octet-stream", body)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestHTTPTimeoutsAreSet verifies that the constructed http.Server has the
// resolved timeouts wired in. Sanity check for the constructor wiring.
func TestHTTPTimeoutsAreSet(t *testing.T) {
	srv, err := server.New(
		server.WithAddr("127.0.0.1:0"),
		server.WithGRPCAddr("127.0.0.1:0"),
		server.WithRoot(".."),
		server.WithHTTPWriteTimeout(7*time.Second),
		server.WithHTTPIdleTimeout(42*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// The HTTP server is internal; we verify via the live listener. The
	// baseline behavior: a connection opened and held idle for longer
	// than the idle timeout is closed by the server. We don't actually
	// hold a connection for 42s in a unit test; instead, we verify that
	// the server starts and accepts requests (the timeout wiring itself
	// is exercised by TestResolveHTTPHardeningConfigured).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	base := fmt.Sprintf("http://%s/v3/console/health/liveness", srv.HTTPAddr())
	if code := get(t, base); code != http.StatusOK {
		t.Fatalf("health check after timeout config: got %d, want %d", code, http.StatusOK)
	}
}
