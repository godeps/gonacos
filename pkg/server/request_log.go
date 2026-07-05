package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

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
type requestLogMiddleware struct {
	logger   Logger
	verbose  bool
	next     http.Handler
	exclude  map[string]struct{}
	registry *observability.Registry
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

func (m *requestLogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !m.verbose {
		if _, skip := m.exclude[r.URL.Path]; skip {
			m.next.ServeHTTP(w, r)
			return
		}
	}
	start := time.Now()
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
			r.URL.RequestURI(),
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
