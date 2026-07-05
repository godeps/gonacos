package app

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
	"golang.org/x/time/rate"
)

// DefaultMaxBuckets caps the per-IP rateLimiter's bucket map at 100k
// entries. At ~50 bytes per bucket (rate.Limiter struct + lastSeen time),
// 100k buckets is ~5 MB — a generous bound that accommodates legitimate
// SDK clients (a Nacos deployment rarely has more than a few thousand
// unique client IPs) while preventing a spoofed-IP SYN-flood from
// ballooning the map to gigabytes between cleanup sweeps.
//
// The default is intentionally large enough that a healthy production
// node never hits it; the cap is a safety net for attack conditions,
// not a steady-state throttle. Operators who legitimately have more
// than 100k client IPs can raise it via [WithMaxBuckets].
const DefaultMaxBuckets = 100_000

// rateLimiter is a per-IP token bucket rate limiter. Bursty by design: a
// client can briefly exceed the steady-state rate up to the burst size, then
// is throttled. Designed to protect against abusive clients without
// penalizing legitimate SDK traffic (which is low-volume per client).
//
// Buckets are lazily created on first request from each IP and periodically
// swept by [rateLimiter.cleanup] to bound memory growth. A hard cap
// ([rateLimiter.maxBuckets]) prevents the map from growing unbounded
// between cleanup sweeps when an attacker spoofs source IPs — once the
// cap is reached, new IPs are rejected with 429 (HTTP) /
// RESOURCE_EXHAUSTED (gRPC) until cleanup frees space.
type rateLimiter struct {
	mu         sync.Mutex
	limit      rate.Limit
	burst      int
	buckets    map[string]*rateLimiterBucket
	maxBuckets int

	// bucketsGauge reports the current bucket count so operators can
	// alert when the limiter is approaching maxBuckets — a sustained
	// high value means either a legitimately large client base (raise
	// the cap) or a spoofed-IP attack (investigate).
	bucketsGauge *observability.Gauge

	// capRejectionCtr increments when a new IP is rejected because
	// maxBuckets has been reached. Distinguished from rate-limit
	// rejections (token bucket empty) via the reason label so operators
	// can tell "the client is sending too fast" from "the server is
	// under IP-spoofing attack".
	capRejectionCtr *observability.Counter
}

type rateLimiterBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter constructs a rateLimiter with the given steady-state
// requests-per-second and burst size. Cleanup of idle buckets is the
// caller's responsibility — see [StartCleanup].
func NewRateLimiter(rps float64, burst int) *rateLimiter {
	return NewRateLimiterWithMaxBuckets(rps, burst, DefaultMaxBuckets)
}

// NewRateLimiterWithMaxBuckets is like NewRateLimiter but accepts an
// explicit maxBuckets cap. Use when the default (100k) is too small for
// a known-large client population, or to tighten the cap on a
// security-sensitive deployment. A non-positive value disables the cap
// (matches the pre-cap behavior) — use with care, as it removes the
// spoofed-IP memory-exhaustion guard.
func NewRateLimiterWithMaxBuckets(rps float64, burst, maxBuckets int) *rateLimiter {
	if maxBuckets < 0 {
		maxBuckets = 0
	}
	return &rateLimiter{
		limit:      rate.Limit(rps),
		burst:      burst,
		buckets:    map[string]*rateLimiterBucket{},
		maxBuckets: maxBuckets,
	}
}

// WithMetrics wires observability for the limiter. Buckets gauge reports
// the current entry count; cap-rejection counter increments when a new IP
// is rejected due to maxBuckets. The cap counter is shared across HTTP
// and gRPC (no protocol label) because the limiter does not know which
// protocol triggered the rejection — operators query by reason="max_buckets"
// alone to see total cap rejections. Returns the receiver for chaining.
// Call once at construction; calling multiple times re-resolves the metrics
// (the registry deduplicates by name+labels so this is cheap).
func (r *rateLimiter) WithMetrics(registry *observability.Registry) *rateLimiter {
	if registry == nil {
		return r
	}
	r.bucketsGauge = registry.Gauge("gonacos_rate_limit_buckets", nil)
	r.capRejectionCtr = registry.Counter("gonacos_rate_limit_rejections_total",
		map[string]string{"reason": "max_buckets"})
	return r
}

// allow returns true if the client IP is allowed to proceed. A false
// return means either the token bucket is empty (client is sending too
// fast) or the bucket map is at capacity and the IP has no existing
// bucket (server is under spoofed-IP attack). The caller cannot
// distinguish the two from the boolean alone — use the rejection
// counter labels to tell them apart in metrics.
func (r *rateLimiter) allow(clientIP string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[clientIP]
	if !ok {
		// New IP. If the map is at capacity, reject instead of
		// allocating a new bucket — this prevents a spoofed-IP
		// attacker from inflating the map to gigabytes between
		// cleanup sweeps. Existing IPs are unaffected, so a
		// legitimate client whose bucket already exists continues
		// to be throttled by its own token bucket.
		if r.maxBuckets > 0 && len(r.buckets) >= r.maxBuckets {
			if r.capRejectionCtr != nil {
				r.capRejectionCtr.Inc()
			}
			if r.bucketsGauge != nil {
				r.bucketsGauge.Set(int64(len(r.buckets)))
			}
			return false
		}
		b = &rateLimiterBucket{limiter: rate.NewLimiter(r.limit, r.burst)}
		r.buckets[clientIP] = b
	}
	b.lastSeen = time.Now()
	if r.bucketsGauge != nil {
		r.bucketsGauge.Set(int64(len(r.buckets)))
	}
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
	if r.bucketsGauge != nil {
		r.bucketsGauge.Set(int64(len(r.buckets)))
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
	limiter      *rateLimiter
	next         http.Handler
	rejectionCtr *observability.Counter
}

// NewRateLimitMiddleware wraps next with a per-IP rate limiter. When
// registry is non-nil, rejected requests increment
// gonacos_rate_limit_rejections_total{protocol="http",reason="rate_limit"}
// — the alerting signal that rate limiting is firing. Without it,
// operators can only infer from gonacos_http_requests_total{status="429"},
// which is indirect and breaks if any other path ever returns 429.
//
// reason="max_buckets" rejections (the bucket cap is hit) are counted
// on a separate label so operators can distinguish "client is sending
// too fast" from "server is under IP-spoofing attack". The buckets
// gauge (gonacos_rate_limit_buckets) reports the current entry count
// so operators can alert before the cap is reached.
func NewRateLimitMiddleware(limiter *rateLimiter, next http.Handler, registry *observability.Registry) http.Handler {
	if limiter == nil {
		return next
	}
	// Wire the limiter's own metrics (buckets gauge + cap-rejection
	// counter) so the middleware's rejection counter and the limiter's
	// cap counter share the same registry. WithMetrics is idempotent
	// (registry deduplicates by name+labels), so calling it here even
	// when the gRPC server already wired the same limiter is cheap.
	limiter.WithMetrics(registry)
	mw := &rateLimitMiddleware{limiter: limiter, next: next}
	if registry != nil {
		mw.rejectionCtr = registry.Counter("gonacos_rate_limit_rejections_total",
			map[string]string{"protocol": "http", "reason": "rate_limit"})
	}
	return mw
}

func (m *rateLimitMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := clientIPForLimit(r)
	if !m.limiter.allow(ip) {
		if m.rejectionCtr != nil {
			m.rejectionCtr.Inc()
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(time.Second.Seconds())))
		protocol.WriteError(w, http.StatusTooManyRequests, protocol.Error{
			Code:    http.StatusTooManyRequests,
			Message: "rate limit exceeded for client IP",
		})
		return
	}
	m.next.ServeHTTP(w, r)
}

// clientIPForLimit extracts the client IP for rate-limiting purposes.
// Honors X-Forwarded-For (first hop) only when the peer is a configured
// trusted proxy; otherwise falls back to RemoteAddr. This prevents a
// non-trusted peer from forging X-Forwarded-For to get a fresh
// rate-limit bucket on every request.
func clientIPForLimit(r *http.Request) string {
	return clientIPFromRequest(r)
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
