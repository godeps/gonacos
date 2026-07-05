package app

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/protocol"
	"golang.org/x/time/rate"
)

// rateLimiter is a per-IP token bucket rate limiter. Bursty by design: a
// client can briefly exceed the steady-state rate up to the burst size, then
// is throttled. Designed to protect against abusive clients without
// penalizing legitimate SDK traffic (which is low-volume per client).
//
// Buckets are lazily created on first request from each IP and periodically
// swept by [rateLimiter.cleanup] to bound memory growth.
type rateLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	buckets map[string]*rateLimiterBucket
}

type rateLimiterBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewRateLimiter(rps float64, burst int) *rateLimiter {
	return &rateLimiter{
		limit:   rate.Limit(rps),
		burst:   burst,
		buckets: map[string]*rateLimiterBucket{},
	}
}

// allow returns true if the client IP is allowed to proceed.
func (r *rateLimiter) allow(clientIP string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[clientIP]
	if !ok {
		b = &rateLimiterBucket{limiter: rate.NewLimiter(r.limit, r.burst)}
		r.buckets[clientIP] = b
	}
	b.lastSeen = time.Now()
	return b.limiter.Allow()
}

// Allow satisfies the grpc.ClientRateLimiter interface so the same
// per-IP token-bucket pool can cap both HTTP and gRPC traffic. When the
// server wires the same *rateLimiter into both NewRateLimitMiddleware
// (HTTP) and grpc.Server.RateLimiter (gRPC), a single client IP shares
// one bucket across both protocols — an SDK client cannot bypass its
// HTTP quota by switching to gRPC.
func (r *rateLimiter) Allow(clientIP string) bool {
	return r.allow(clientIP)
}

// cleanup drops buckets that haven't been touched within maxIdle. Designed
// to be called periodically (e.g. every 5 minutes) from a background
// goroutine so the buckets map doesn't grow unbounded under spoofed-IP
// attacks.
func (r *rateLimiter) cleanup(maxIdle time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-maxIdle)
	for ip, b := range r.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(r.buckets, ip)
		}
	}
}

// StartCleanup launches a background goroutine that periodically drops idle
// buckets. Returns a stop function that is safe to call multiple times.
func (r *rateLimiter) StartCleanup(interval, maxIdle time.Duration) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.cleanup(maxIdle)
			case <-stop:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

// rateLimitMiddleware wraps an http.Handler with a per-IP rate limiter. IPs
// exceeding the configured rate receive 429 Too Many Requests.
type rateLimitMiddleware struct {
	limiter *rateLimiter
	next    http.Handler
}

func NewRateLimitMiddleware(limiter *rateLimiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return &rateLimitMiddleware{limiter: limiter, next: next}
}

func (m *rateLimitMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := clientIPForLimit(r)
	if !m.limiter.allow(ip) {
		w.Header().Set("Retry-After", strconv.Itoa(int(time.Second.Seconds())))
		protocol.WriteError(w, http.StatusTooManyRequests, protocol.Error{
			Code:    http.StatusTooManyRequests,
			Message: "rate limit exceeded for client IP",
		})
		return
	}
	m.next.ServeHTTP(w, r)
}

// clientIPForLimit extracts the client IP for rate-limiting purposes. Honors
// X-Forwarded-For (first hop) when present so a deployment behind a layer-7
// proxy still gets per-client buckets; falls back to RemoteAddr.
func clientIPForLimit(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// maxBodyMiddleware wraps an http.Handler with a MaxBytesReader on the
// request body. Bodies exceeding maxBytes return 413 Request Entity Too
// Large. Prevents OOM from oversized request bodies.
type maxBodyMiddleware struct {
	next     http.Handler
	maxBytes int64
}

func NewMaxBodyMiddleware(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return &maxBodyMiddleware{next: next, maxBytes: maxBytes}
}

func (m *maxBodyMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes)
	}
	m.next.ServeHTTP(w, r)
}
