package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

// sensitiveQueryKeys is the set of query-parameter keys whose values must
// not appear in logs. The Nacos SDK passes accessToken as a query
// parameter on GET requests (auth_middleware.go:extractAccessToken reads
// it from r.URL.Query()), so without redaction the access token would
// land in the access log — a log leak becomes a session hijack.
//
// Matching is case-insensitive: SDKs across languages disagree on
// capitalization (accessToken vs accesstoken vs ACCESS_TOKEN), and an
// attacker probing variants shouldn't bypass the redaction by picking
// a different case.
var sensitiveQueryKeys = map[string]struct{}{
	"accesstoken":   {},
	"password":      {},
	"passwd":        {},
	"secret":        {},
	"token":         {},
	"authorization": {},
	"apikey":        {},
	"api_key":       {},
}

// sanitizeRequestURI returns the request URI with sensitive query
// parameter values replaced by "***". The key is preserved so operators
// can still see that a token was present (useful for debugging auth
// failures) but the value is not recoverable from the log line.
//
// The original r.URL is not mutated — the request handler still sees the
// real query parameters. A cloned url.URL is used for rendering.
//
// Used by both the request log and the panic recovery log so neither
// path leaks secrets to disk.
func sanitizeRequestURI(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.RawQuery == "" {
		return u.RequestURI()
	}
	redacted := redactQuery(u.RawQuery)
	clone := *u
	clone.RawQuery = redacted
	return clone.RequestURI()
}

// redactQuery parses a URL-encoded query string and replaces the values
// of sensitive keys with "***". Returns the re-encoded query string.
//
// Manually re-encodes rather than using url.Values.Encode() so the
// redaction marker "***" stays literal — Encode() would percent-encode
// '*' to '%2A', producing a log line like "accessToken=%2A%2A%2A" that
// is harder to grep and harder to reason about at a glance.
//
// Operates on the raw encoded form so the original decoding is
// preserved (a key that arrives as %61ccessToken still matches
// "accessToken" after decoding).
func redactQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		// Malformed query string — return as-is rather than risk
		// dropping it entirely (the path is still useful in logs).
		// The likelihood is low; the failure mode is "no redaction",
		// which is the same as the pre-redaction behavior.
		return raw
	}
	var b strings.Builder
	first := true
	for key, vals := range values {
		for _, val := range vals {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(url.QueryEscape(key))
			b.WriteByte('=')
			if _, sensitive := sensitiveQueryKeys[strings.ToLower(key)]; sensitive {
				b.WriteString("***")
			} else {
				b.WriteString(url.QueryEscape(val))
			}
		}
	}
	return b.String()
}

// requestLogMiddleware logs each HTTP request via the configured Logger.
// Designed for production: a single line per request with method, path,
// status, duration, and remote address. Skip path-based sampling — the
// volume is bounded by the rate limiter and legitimate SDK traffic is
// low-frequency per client.
//
// When a metrics registry is wired in, the middleware also increments
// gonacos_http_requests_total{method,status} so operators can build
// request-rate and error-rate panels in Grafana without parsing logs.
// Labels are intentionally low-cardinality (method + status code class)
// to avoid blowing up the metrics series count on a high-traffic node.
//
// activeRequests tracks in-flight HTTP requests and is reported via
// gonacos_http_active_requests gauge — symmetric to
// gonacos_grpc_active_streams. Operators alert when concurrency
// approaches MaxConns to catch a peer exhausting the connection budget
// or a slow handler pinning goroutines.
type requestLogMiddleware struct {
	logger         Logger
	verbose        bool
	next           http.Handler
	exclude        map[string]struct{}
	registry       *observability.Registry
	activeRequests atomic.Int64
}

// requestLogExclude is the set of paths that are noisy enough to skip by
// default. Health and metrics probes are hit every few seconds by kubelet
// and Prometheus; logging them adds noise without value.
var requestLogExclude = map[string]struct{}{
	"/v3/console/health/liveness":    {},
	"/v3/console/health/readiness":   {},
	"/v3/admin/core/state/liveness":  {},
	"/v3/admin/core/state/readiness": {},
	"/metrics":                       {},
	"/v3/console/ui":                 {},
	"/v3/console/ui/":                {},
}

// newRequestLogMiddleware wraps next with a request logger. When verbose is
// false, paths in requestLogExclude are skipped (still served, just not
// logged). When verbose is true, every request is logged. When registry is
// non-nil, the middleware also increments gonacos_http_requests_total.
func newRequestLogMiddleware(logger Logger, verbose bool, registry *observability.Registry, next http.Handler) http.Handler {
	if logger == nil && registry == nil {
		return next
	}
	return &requestLogMiddleware{
		logger:   logger,
		verbose:  verbose,
		next:     next,
		exclude:  requestLogExclude,
		registry: registry,
	}
}

// statusRecorder captures the status code set by downstream handlers. The
// standard httptest.ResponseRecorder does this; for production we need a
// thin wrapper because http.ResponseWriter doesn't expose the status.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// countingReader wraps an io.ReadCloser to count bytes read, for the
// gonacos_http_request_bytes histogram. The wrapper is transparent —
// it forwards Read() and Close() to the underlying reader while
// tallying bytes read. Handlers read through this wrapper; the count
// is observed after the handler returns. A request with no body
// (GET, DELETE) observes 0; a POST with a JSON payload observes the
// payload size. chunked-encoded bodies that lack Content-Length are
// still tracked correctly because the wrapper counts actual bytes
// read, not the Content-Length header.
type countingReader struct {
	io.ReadCloser
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n += int64(n)
	return n, err
}

func (m *requestLogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Track in-flight requests so gonacos_http_active_requests reports
	// current concurrency. Increment before the exclude-list check so
	// even health/metrics probes (which skip logging but still hold a
	// goroutine) are counted — the gauge is "goroutines pinned by HTTP
	// requests", not "logged requests in flight". The deferred func
	// decrements first, then sets the gauge so the final reported value
	// reflects the post-decrement count.
	m.activeRequests.Add(1)
	defer func() {
		m.activeRequests.Add(-1)
		if m.registry != nil {
			m.registry.Gauge("gonacos_http_active_requests", nil).Set(m.activeRequests.Load())
		}
	}()
	if m.registry != nil {
		m.registry.Gauge("gonacos_http_active_requests", nil).Set(m.activeRequests.Load())
	}
	if !m.verbose {
		if _, skip := m.exclude[r.URL.Path]; skip {
			m.next.ServeHTTP(w, r)
			return
		}
	}
	start := time.Now()
	// Wrap the request body so we can record the request byte count
	// for gonacos_http_request_bytes. The wrapper is transparent —
	// handlers read through it normally; the byte count is read
	// after the handler returns. GET/DELETE requests with no body
	// observe 0.
	var cr *countingReader
	if r.Body != nil {
		cr = &countingReader{ReadCloser: r.Body}
		r.Body = cr
	}
	rec := &statusRecorder{ResponseWriter: w, status: 0}
	m.next.ServeHTTP(rec, r)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	duration := time.Since(start)
	rid := requestIDFromContext(r.Context())
	if m.logger != nil {
		m.logger.Infof("http %s %s status=%d bytes=%d duration=%s remote=%s rid=%s",
			r.Method,
			sanitizeRequestURI(r.URL),
			rec.status,
			rec.bytes,
			formatDuration(duration),
			r.RemoteAddr,
			rid,
		)
	}
	if m.registry != nil {
		m.registry.Counter("gonacos_http_requests_total", map[string]string{
			"method": r.Method,
			"status": strconv.Itoa(rec.status),
		}).Inc()
		m.registry.Histogram("gonacos_http_request_duration_seconds",
			map[string]string{"method": r.Method},
			grpcsrv.HTTPLatencyBuckets(),
		).Observe(duration.Milliseconds())
		// Response size distribution. Operators use this to spot
		// regressions where a response balloons from 1KB to 100KB
		// (e.g., a config list endpoint returning unbounded results),
		// and to estimate bandwidth for capacity planning. The
		// +Inf bucket captures anything beyond the largest boundary.
		m.registry.Histogram("gonacos_http_response_bytes",
			map[string]string{"method": r.Method},
			grpcsrv.HTTPBytesBuckets(),
		).Observe(int64(rec.bytes))
		// Request size distribution. The counter is the byte count
		// read from the request body. Operators use this to spot a
		// peer sending oversized requests (resource exhaustion
		// vector when maxBodyMiddleware is generous) and to estimate
		// ingress bandwidth. GET/DELETE requests with no body
		// observe 0 — distinct from a small POST payload.
		var reqN int64
		if cr != nil {
			reqN = cr.n
		}
		m.registry.Histogram("gonacos_http_request_bytes",
			map[string]string{"method": r.Method},
			grpcsrv.HTTPBytesBuckets(),
		).Observe(reqN)
	}
}

// formatDuration trims sub-millisecond noise for log readability.
func formatDuration(d time.Duration) string {
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
