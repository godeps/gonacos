package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

// TestRateLimiterPerIP verifies that the per-IP rate limiter throttles the
// second burst of requests from the same IP while still allowing a request
// from a different IP.
func TestRateLimiterPerIP(t *testing.T) {
	rl := NewRateLimiter(1, 1) // 1 rps, burst 1
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}), nil)

	// First request from IP1 passes (burst).
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request from IP1: got %d, want %d", w1.Code, http.StatusOK)
	}

	// Second immediate request from IP1 is throttled.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from IP1: got %d, want %d", w2.Code, http.StatusTooManyRequests)
	}
	if got := w2.Header().Get("Retry-After"); got == "" {
		t.Fatalf("Retry-After header missing on 429 response")
	}

	// First request from IP2 passes (different bucket).
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "10.0.0.2:1234"
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("first request from IP2: got %d, want %d", w3.Code, http.StatusOK)
	}
}

// TestRateLimiterXForwardedFor verifies that X-Forwarded-For is honored
// when the peer is a configured trusted proxy, so a deployment behind
// a layer-7 proxy gets per-client buckets. Without the trusted-proxy
// gate, XFF would be ignored — a non-trusted peer must not be able to
// forge XFF to bypass rate limits.
func TestRateLimiterXForwardedFor(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	rl := NewRateLimiter(1, 1)
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil)

	// First request from XFF client A.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.99:1234" // trusted proxy
	req1.Header.Set("X-Forwarded-For", "203.0.113.1")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request from XFF client A: got %d, want %d", w1.Code, http.StatusOK)
	}

	// Second immediate request from same XFF client A is throttled.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.99:1234"
	req2.Header.Set("X-Forwarded-For", "203.0.113.1")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from XFF client A: got %d, want %d", w2.Code, http.StatusTooManyRequests)
	}

	// First request from XFF client B (different) passes — different
	// XFF IP yields a fresh bucket.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "10.0.0.99:1234"
	req3.Header.Set("X-Forwarded-For", "203.0.113.2")
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("first request from XFF client B: got %d, want %d", w3.Code, http.StatusOK)
	}
}

// TestRateLimiterUntrustedProxyIgnoresXFF verifies that when the peer
// is NOT a configured trusted proxy, X-Forwarded-For is ignored — a
// non-trusted peer must not be able to forge XFF to get a fresh
// rate-limit bucket on every request.
func TestRateLimiterUntrustedProxyIgnoresXFF(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	rl := NewRateLimiter(1, 1)
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil)

	// First request from a peer NOT in 10.0.0.0/8 — XFF should be
	// ignored, RemoteAddr (203.0.113.99) used.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "203.0.113.99:1234"
	req1.Header.Set("X-Forwarded-For", "1.1.1.1")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want %d", w1.Code, http.StatusOK)
	}

	// Second request, same RemoteAddr but DIFFERENT forged XFF — the
	// forged XFF must be ignored, so this hits the same bucket and
	// is throttled. Without the trusted-proxy gate, the forged XFF
	// would yield a fresh bucket and the request would pass.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.99:1234"
	req2.Header.Set("X-Forwarded-For", "2.2.2.2")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request with forged XFF: got %d, want %d (forged XFF must NOT bypass rate limit)", w2.Code, http.StatusTooManyRequests)
	}
}

// TestRateLimiterCleanup verifies that idle buckets are reaped.
func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(100, 100)
	for i := 0; i < 10; i++ {
		rl.allow("10.0.0." + string(rune('0'+i)))
	}
	rl.mu.Lock()
	n := len(rl.buckets)
	rl.mu.Unlock()
	if n != 10 {
		t.Fatalf("after 10 allows: got %d buckets, want 10", n)
	}

	// Force all buckets to look idle by backdating lastSeen.
	rl.mu.Lock()
	cutoff := time.Now().Add(-2 * time.Minute)
	for _, b := range rl.buckets {
		b.lastSeen = cutoff
	}
	rl.mu.Unlock()

	rl.cleanup(time.Minute)
	rl.mu.Lock()
	n = len(rl.buckets)
	rl.mu.Unlock()
	if n != 0 {
		t.Fatalf("after cleanup: got %d buckets, want 0", n)
	}
}

// TestRateLimiterConcurrent exercises the mutex under concurrent access to
// catch data races when run with -race.
func TestRateLimiterConcurrent(t *testing.T) {
	rl := NewRateLimiter(1000, 1000)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rl.allow("10.0.0." + string(rune('0'+i%10)))
		}(i)
	}
	wg.Wait()
}

// TestMaxBodyMiddlewareRejectsOversized verifies that bodies exceeding the
// limit are rejected mid-read with the standard http.MaxBytesReader behavior.
func TestMaxBodyMiddlewareRejectsOversized(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body; MaxBytesReader will return an error once the
		// limit is exceeded.
		_, err := io.ReadAll(r.Body)
		if err == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		// MaxBytesReader triggers http.MaxBytesError, which the response
		// writer converts to a 413 status via the http server framework.
		// In httptest.NewRecorder, we simulate by checking the error.
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	mw := NewMaxBodyMiddleware(8, handler)

	body := bytes.Repeat([]byte("x"), 64) // 64 bytes, limit 8
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: got %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestMaxBodyMiddlewareAllowsWithinLimit verifies that bodies within the
// limit pass through unchanged.
func TestMaxBodyMiddlewareAllowsWithinLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mw := NewMaxBodyMiddleware(1024, handler)

	body := []byte("hello")
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("within-limit body: got %d, want %d", w.Code, http.StatusOK)
	}
	if string(w.Body.Bytes()) != "hello" {
		t.Fatalf("within-limit body: got %q, want %q", w.Body.String(), "hello")
	}
}

// TestMaxBodyMiddlewareZeroDisables verifies that a zero or negative limit
// returns the inner handler untouched (no wrapping). Verified behaviorally:
// when unwrapped, a large body is fully readable (no 413-style truncation).
func TestMaxBodyMiddlewareZeroDisables(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	body := bytes.Repeat([]byte("x"), 4096)
	for _, max := range []int64{0, -1} {
		mw := NewMaxBodyMiddleware(max, inner)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("max=%d: got status %d, want %d (handler should be unwrapped)", max, w.Code, http.StatusOK)
		}
		if got := w.Body.Len(); got != len(body) {
			t.Fatalf("max=%d: got %d bytes, want %d (body should pass through)", max, got, len(body))
		}
	}
}

// TestRateLimitMiddlewareRejectionMetric verifies that rejected requests
// increment gonacos_rate_limit_rejections_total{protocol="http"} — the
// alerting signal that HTTP rate limiting is firing. Without it, operators
// can only infer from gonacos_http_requests_total{status="429"}, which
// is indirect and breaks if any other path ever returns 429.
func TestRateLimitMiddlewareRejectionMetric(t *testing.T) {
	rl := NewRateLimiter(1, 1) // 1 rps, burst 1
	registry := observability.NewRegistry()
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), registry)

	// First request from IP1 passes (burst).
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req1)

	// Second immediate request from IP1 is throttled.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req2)

	// First request from IP2 passes (separate bucket).
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "10.0.0.2:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req3)

	// Second immediate request from IP2 is throttled.
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4.RemoteAddr = "10.0.0.2:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req4)

	rejections := registry.Counter("gonacos_rate_limit_rejections_total",
		map[string]string{"protocol": "http", "reason": "rate_limit"}).Value()
	if rejections != 2 {
		t.Fatalf("gonacos_rate_limit_rejections_total{protocol=\"http\",reason=\"rate_limit\"} = %d, want 2 (one per IP)", rejections)
	}
}

// TestRateLimitMiddlewareNilRegistryNoop verifies that the rejection
// counter doesn't panic when the registry is nil — production callers
// that opt out of metrics must not crash on rate-limit rejection.
func TestRateLimitMiddlewareNilRegistryNoop(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil)

	// First request passes.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req1)

	// Second request is throttled — must not panic with nil registry.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req2)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("nil registry should still reject: got %d, want 429", rec.Code)
	}
}

// TestRateLimiterMaxBucketsCap verifies that when the bucket map reaches
// maxBuckets, a new IP is rejected instead of allocating a new bucket.
// This is the spoofed-IP attack guard: without the cap, an attacker
// sending requests from 10M spoofed IPs would allocate ~500MB of bucket
// memory before the next cleanup sweep.
func TestRateLimiterMaxBucketsCap(t *testing.T) {
	rl := NewRateLimiterWithMaxBuckets(100, 10, 3)
	// Fill the cap with three distinct IPs.
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if !rl.allow(ip) {
			t.Fatalf("allow(%s) = false, want true (cap not yet reached)", ip)
		}
	}
	// A fourth IP should be rejected — the cap is hit.
	if rl.allow("10.0.0.4") {
		t.Fatal("allow(10.0.0.4) = true, want false (cap reached)")
	}
}

// TestRateLimiterMaxBucketsExistingIPPasses verifies that an IP whose
// bucket already exists is still allowed after the cap is reached —
// legitimate clients with existing buckets are not penalized by the cap.
func TestRateLimiterMaxBucketsExistingIPPasses(t *testing.T) {
	rl := NewRateLimiterWithMaxBuckets(100, 10, 2)
	// Two IPs fill the cap.
	rl.allow("10.0.0.1")
	rl.allow("10.0.0.2")
	// Third IP rejected (cap reached).
	if rl.allow("10.0.0.3") {
		t.Fatal("allow(10.0.0.3) = true, want false (cap reached)")
	}
	// Existing IP still allowed (within its token bucket).
	if !rl.allow("10.0.0.1") {
		t.Fatal("allow(10.0.0.1) = false, want true (existing bucket)")
	}
}

// TestRateLimiterMaxBucketsZeroDisablesCap verifies that maxBuckets=0
// removes the cap — preserves the pre-cap behavior for callers that
// explicitly opt out (e.g. a single-tenant deployment with a known
// bounded client count).
func TestRateLimiterMaxBucketsZeroDisablesCap(t *testing.T) {
	rl := NewRateLimiterWithMaxBuckets(100, 10, 0)
	// Allocate well beyond DefaultMaxBuckets — no cap, no rejection.
	for i := 0; i < 1000; i++ {
		ip := "10.0.0." + strconv.Itoa(i)
		if !rl.allow(ip) {
			t.Fatalf("allow(%s) = false, want true (cap disabled)", ip)
		}
	}
}

// TestRateLimiterBucketsGauge verifies that gonacos_rate_limit_buckets
// reports the current bucket count. Operators alert when the gauge
// approaches maxBuckets — a sustained high value means either a large
// client base (raise the cap) or a spoofed-IP attack (investigate).
func TestRateLimiterBucketsGauge(t *testing.T) {
	registry := observability.NewRegistry()
	rl := NewRateLimiterWithMaxBuckets(100, 10, 100)
	rl.WithMetrics(registry)

	rl.allow("10.0.0.1")
	rl.allow("10.0.0.2")
	rl.allow("10.0.0.3")

	got := registry.Gauge("gonacos_rate_limit_buckets", nil).Value()
	if got != 3 {
		t.Fatalf("buckets gauge = %d, want 3", got)
	}
}

// TestRateLimiterMaxBucketsCapRejectionCounter verifies that a
// max_buckets rejection increments gonacos_rate_limit_rejections_total
// {reason="max_buckets"} — distinguished from token-bucket exhaustion
// (reason="rate_limit") so operators can tell "client is sending too
// fast" from "server is under IP-spoofing attack".
func TestRateLimiterMaxBucketsCapRejectionCounter(t *testing.T) {
	registry := observability.NewRegistry()
	rl := NewRateLimiterWithMaxBuckets(100, 10, 1)
	rl.WithMetrics(registry)

	rl.allow("10.0.0.1") // fills cap
	rl.allow("10.0.0.2") // rejected (cap)
	rl.allow("10.0.0.3") // rejected (cap)

	got := registry.Counter("gonacos_rate_limit_rejections_total",
		map[string]string{"reason": "max_buckets"}).Value()
	if got != 2 {
		t.Fatalf("max_buckets rejection counter = %d, want 2", got)
	}
}

// TestRateLimitMiddlewareMaxBucketsRejectsNewIP verifies the full HTTP
// middleware path: when the limiter hits maxBuckets, new IPs get 429
// and the cap rejection counter increments. Existing IPs continue to
// be throttled by their own token bucket (not 429 unless their bucket
// is empty).
func TestRateLimitMiddlewareMaxBucketsRejectsNewIP(t *testing.T) {
	registry := observability.NewRegistry()
	rl := NewRateLimiterWithMaxBuckets(100, 10, 1)
	mw := NewRateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), registry)

	// First IP fills cap.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req1)

	// Second IP (new) — rejected with 429.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.2:1234"
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("new IP at cap: status = %d, want 429", rec2.Code)
	}

	// Cap rejection counter should be 1.
	capRej := registry.Counter("gonacos_rate_limit_rejections_total",
		map[string]string{"reason": "max_buckets"}).Value()
	if capRej != 1 {
		t.Fatalf("max_buckets counter = %d, want 1", capRej)
	}
}
