package grpc

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// threadSafeBuffer is a bytes.Buffer protected by a mutex so concurrent
// goroutines (the test main goroutine + the HTTP handler goroutine) can
// safely write and read it.
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) WriteString(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(s)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForLogSubstring polls logBuf for up to timeout waiting for substring to
// appear. Returns the log content (so the caller can include it in failure
// messages) and a boolean indicating whether the substring was found.
//
// Why this exists: an HTTP client's Post returns as soon as the response
// headers are received — the server's handler may still be running when
// Post returns. The gRPC server writes the access log via Logf AFTER writing
// the response body, so a test that reads the log buffer immediately after
// Post can race the handler's Logf call and see an empty log. Polling
// eliminates the race without arbitrary sleeps.
//
// The timeout is bounded so a missing log line fails fast rather than
// hanging the test suite.
func waitForLogSubstring(t *testing.T, logBuf *threadSafeBuffer, substring string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		logged := logBuf.String()
		if strings.Contains(logged, substring) {
			return logged, true
		}
		if time.Now().After(deadline) {
			return logged, false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestServerStartsAndRespondsToUnknownPath(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	if srv.Addr() == nil {
		t.Fatalf("server did not start")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/UnknownService/unknownMethod",
		"application/grpc",
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("grpc-status") != "12" {
		t.Fatalf("grpc-status = %v, want 12 (Unimplemented)", resp.Header.Get("grpc-status"))
	}
}

func TestServerRejectsNonGRPCContentType(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"text/plain",
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", resp.StatusCode)
	}
}

func TestServerUnaryHandlerReturnsPayload(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	req := Payload{Metadata: Metadata{Type: "TestRequest"}}
	body := encodeGRPCRequestBody(req)
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("grpc-status") != "0" {
		t.Fatalf("grpc-status = %v, want 0", resp.Header.Get("grpc-status"))
	}
}

func TestServerUnaryHandlerReturnsErrorStatus(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{}, NewStatusError(StatusInvalidArgument, "bad request")
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	req := Payload{Metadata: Metadata{Type: "TestRequest"}}
	body := encodeGRPCRequestBody(req)
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("grpc-status") != "3" {
		t.Fatalf("grpc-status = %v, want 3 (InvalidArgument)", resp.Header.Get("grpc-status"))
	}
}

func TestServerUnaryHandlerPanicReturnsInternal(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	var logBuf threadSafeBuffer
	srv.Logf = func(format string, args ...any) {
		logBuf.WriteString(fmt.Sprintf(format, args...))
		logBuf.WriteString("\n")
	}
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		panic("boom")
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	req := Payload{Metadata: Metadata{Type: "TestRequest"}}
	body := encodeGRPCRequestBody(req)
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("grpc-status") != "13" {
		t.Fatalf("grpc-status = %v, want 13 (Internal)", resp.Header.Get("grpc-status"))
	}
	// Poll the log buffer: the panic recovery path runs in the deferred
	// function, which may execute AFTER http.Post returns (the client
	// returns on response headers; the server's handler is still running
	// the deferred recover). Without polling, this test races the log
	// write and intermittently fails.
	logged, ok := waitForLogSubstring(t, &logBuf, "grpc panic recovered", 2*time.Second)
	if !ok {
		t.Errorf("log missing 'grpc panic recovered': %s", logged)
	}
}

func TestServerAccessLogEmittedOnUnaryCall(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	var logBuf threadSafeBuffer
	srv.Logf = func(format string, args ...any) {
		logBuf.WriteString(fmt.Sprintf(format, args...))
		logBuf.WriteString("\n")
	}
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	req := Payload{Metadata: Metadata{Type: "TestRequest"}}
	body := encodeGRPCRequestBody(req)
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	// Poll the log buffer: the access log is written via Logf AFTER the
	// response body is flushed, which can happen AFTER http.Post returns
	// (the client returns on response headers). Reading logBuf immediately
	// races the handler's Logf call and intermittently fails with an empty
	// log. Polling eliminates the race without arbitrary sleeps.
	logged, ok := waitForLogSubstring(t, &logBuf, "grpc POST", 2*time.Second)
	if !ok {
		t.Errorf("access log missing method/path: %s", logged)
	}
	if !strings.Contains(logged, "status=0") {
		t.Errorf("access log missing status=0 (OK): %s", logged)
	}
	if !strings.Contains(logged, "duration=") {
		t.Errorf("access log missing duration: %s", logged)
	}
	if !strings.Contains(logged, "remote=") {
		t.Errorf("access log missing remote: %s", logged)
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	if srv.Addr() == nil {
		t.Fatalf("server did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestServerRejectsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	resp, err := http.Get("http://" + srv.Addr().String() + "/Request/request")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// encodeGRPCRequestBody wraps a Payload in a gRPC frame.
func encodeGRPCRequestBody(p Payload) []byte {
	encoded := p.Encode()
	frame := Frame{Payload: encoded}
	var buf bytes.Buffer
	_ = WriteFrame(&buf, frame)
	return buf.Bytes()
}

// TestServerRejectsOversizedFrame verifies that a peer claiming a 1 GiB
// frame body gets RESOURCE_EXHAUSTED without the server allocating any
// memory for the body. The frame header declares length=1 GiB but the
// request body stops short — the server must reject before reading body.
func TestServerRejectsOversizedFrame(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.MaxFrameBytes = 4 * 1024 * 1024
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// Build a 5-byte frame header that claims a 1 GiB body, then send
	// nothing else. The server must reject without waiting for body bytes.
	header := make([]byte, 5)
	header[1] = 0x40 // length = 1 << 30 = 1 GiB
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(header),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (gRPC status conveyed via header)", resp.StatusCode)
	}
	// gRPC status 8 = RESOURCE_EXHAUSTED.
	if resp.Header.Get("grpc-status") != "8" {
		t.Fatalf("grpc-status = %v, want 8 (RESOURCE_EXHAUSTED)", resp.Header.Get("grpc-status"))
	}
}

// stubRateLimiter is a test ClientRateLimiter that denies all requests
// after the first allowThreshold calls. Used to verify the gRPC server
// returns RESOURCE_EXHAUSTED when the limiter rejects a peer.
type stubRateLimiter struct {
	mu      sync.Mutex
	calls   int
	allowAt int
}

func (s *stubRateLimiter) Allow(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.calls <= s.allowAt
}

// TestServerRateLimitedReturnsResourceExhausted verifies that when the
// per-IP rate limiter rejects a peer, the server returns RESOURCE_EXHAUSTED
// (gRPC status 8) without invoking the handler.
func TestServerRateLimitedReturnsResourceExhausted(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	limiter := &stubRateLimiter{allowAt: 1} // allow first, deny rest
	srv.RateLimiter = limiter
	handlerCalled := 0
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		handlerCalled++
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	body := encodeGRPCRequestBody(Payload{Metadata: Metadata{Type: "TestRequest"}})

	// First request: allowed, handler runs.
	resp1, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post1: %v", err)
	}
	resp1.Body.Close()
	if resp1.Header.Get("grpc-status") != "0" {
		t.Fatalf("grpc-status1 = %v, want 0", resp1.Header.Get("grpc-status"))
	}

	// Second request: denied, RESOURCE_EXHAUSTED, handler not called.
	resp2, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post2: %v", err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("grpc-status") != "8" {
		t.Fatalf("grpc-status2 = %v, want 8 (RESOURCE_EXHAUSTED)", resp2.Header.Get("grpc-status"))
	}
	if handlerCalled != 1 {
		t.Fatalf("handlerCalled = %d, want 1 (denied request must not invoke handler)", handlerCalled)
	}
}

// stubCounter is a test CounterMetric that counts Inc() calls.
type stubCounter struct {
	mu    sync.Mutex
	count int
}

func (c *stubCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
}

// stubHistogram is a test HistogramMetric that records the last observed
// value and the number of observations.
type stubHistogram struct {
	mu       sync.Mutex
	observed int64
	obsCount int
}

func (h *stubHistogram) Observe(ms int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observed = ms
	h.obsCount++
}

// stubMetricsRegistry is a test MetricsRegistry that holds a single counter
// per (name, labels) pair so the test can assert increments.
type stubMetricsRegistry struct {
	mu       sync.Mutex
	counters map[string]*stubCounter
	histos   map[string]*stubHistogram
}

func newStubMetricsRegistry() *stubMetricsRegistry {
	return &stubMetricsRegistry{
		counters: map[string]*stubCounter{},
		histos:   map[string]*stubHistogram{},
	}
}

func (r *stubMetricsRegistry) Counter(name string, labels map[string]string) CounterMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := name + "/" + labels["method"] + "/" + labels["status"]
	c, ok := r.counters[key]
	if !ok {
		c = &stubCounter{}
		r.counters[key] = c
	}
	return c
}

func (r *stubMetricsRegistry) Histogram(name string, labels map[string]string, buckets []float64) HistogramMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := name + "/" + labels["method"]
	h, ok := r.histos[key]
	if !ok {
		h = &stubHistogram{}
		r.histos[key] = h
	}
	return h
}

// TestServerMetricsRegistryIncrements verifies that the gRPC server records
// each request under gonacos_grpc_requests_total{method,status}.
func TestServerMetricsRegistryIncrements(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	registry := newStubMetricsRegistry()
	srv.MetricsRegistry = registry
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	body := encodeGRPCRequestBody(Payload{Metadata: Metadata{Type: "TestRequest"}})
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	// Look up the counter for /Request/request with status=0 (OK).
	key := "gonacos_grpc_requests_total//Request/request/0"
	registry.mu.Lock()
	c, ok := registry.counters[key]
	registry.mu.Unlock()
	if !ok {
		t.Fatalf("no counter recorded for key %q (counters: %v)", key, registry.counters)
	}
	c.mu.Lock()
	got := c.count
	c.mu.Unlock()
	if got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}

	// Histogram should also have one observation for /Request/request.
	hkey := "gonacos_grpc_request_duration_seconds//Request/request"
	registry.mu.Lock()
	h, hok := registry.histos[hkey]
	registry.mu.Unlock()
	if !hok {
		t.Fatalf("no histogram recorded for key %q", hkey)
	}
	h.mu.Lock()
	hCount := h.obsCount
	h.mu.Unlock()
	if hCount != 1 {
		t.Fatalf("histogram observations = %d, want 1", hCount)
	}

	// Response bytes histogram should also have one observation for
	// /Request/request. The observed value is the byte count of the
	// response payload written by the unary handler. The handler
	// returns a Payload{Metadata: Metadata{Type: "TestResponse"}}, so
	// the response is non-empty and the observed value should be > 0.
	rkey := "gonacos_grpc_response_bytes//Request/request"
	registry.mu.Lock()
	rh, rhok := registry.histos[rkey]
	registry.mu.Unlock()
	if !rhok {
		t.Fatalf("no response-bytes histogram recorded for key %q (histos: %v)", rkey, registry.histos)
	}
	rh.mu.Lock()
	rhCount := rh.obsCount
	rhObserved := rh.observed
	rh.mu.Unlock()
	if rhCount != 1 {
		t.Fatalf("response-bytes histogram observations = %d, want 1", rhCount)
	}
	if rhObserved <= 0 {
		t.Fatalf("response-bytes observed = %d, want > 0 (handler wrote a non-empty payload)", rhObserved)
	}

	// Request bytes histogram should also have one observation for
	// /Request/request. The observed value is the byte count read from
	// the request body (the gRPC frame payload + 5-byte length-prefix
	// header). The request is encodeGRPCRequestBody(Payload{...}), so
	// the request is non-empty and the observed value should be > 0.
	qkey := "gonacos_grpc_request_bytes//Request/request"
	registry.mu.Lock()
	qh, qhok := registry.histos[qkey]
	registry.mu.Unlock()
	if !qhok {
		t.Fatalf("no request-bytes histogram recorded for key %q (histos: %v)", qkey, registry.histos)
	}
	qh.mu.Lock()
	qhCount := qh.obsCount
	qhObserved := qh.observed
	qh.mu.Unlock()
	if qhCount != 1 {
		t.Fatalf("request-bytes histogram observations = %d, want 1", qhCount)
	}
	if qhObserved <= 0 {
		t.Fatalf("request-bytes observed = %d, want > 0 (client sent a non-empty payload)", qhObserved)
	}
}

// TestServerUnaryHandlerPanicIncrementsMetric verifies that a panicking
// unary handler increments gonacos_grpc_panics_total{method}. The metric
// is the alerting signal for handler crashes — a non-zero rate pages
// on-call (deployed bug or malformed request). Without the metric,
// panics only show in logs (via Logf), which are easy to miss under high
// request volume.
//
// The metric must increment even when Logf is nil — silent panics are
// still visible in monitoring.
func TestServerUnaryHandlerPanicIncrementsMetric(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	registry := newStubMetricsRegistry()
	srv.MetricsRegistry = registry
	// Intentionally leave Logf nil — metric must still increment.
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		panic("boom")
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	body := encodeGRPCRequestBody(Payload{Metadata: Metadata{Type: "TestRequest"}})
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("grpc-status") != "13" {
		t.Fatalf("grpc-status = %v, want 13 (Internal)", resp.Header.Get("grpc-status"))
	}

	// stubMetricsRegistry keys counters as name + "/" + method + "/" +
	// status. The panic counter has no status label, so the key uses an
	// empty status suffix.
	key := "gonacos_grpc_panics_total//Request/request/"
	registry.mu.Lock()
	c, ok := registry.counters[key]
	registry.mu.Unlock()
	if !ok {
		t.Fatalf("no panic counter recorded for key %q (counters: %v)", key, registry.counters)
	}
	c.mu.Lock()
	got := c.count
	c.mu.Unlock()
	if got != 1 {
		t.Errorf("gonacos_grpc_panics_total = %d, want 1", got)
	}
}

// TestServerUnaryFrameReadTimeoutIncrementsMetric verifies that a
// slowloris-style peer sending a frame body 1 byte at a time triggers
// ReadFrameTimeout, and that the timeout is recorded under
// gonacos_grpc_frame_read_timeouts_total. The metric is the alerting
// signal for slowloris-on-body attacks against the gRPC path — operators
// use it to distinguish "legitimate slow clients hitting handler
// timeouts" from "peers stalling mid-frame and getting cut off".
//
// Without this metric, operators can only see the resulting
// DEADLINE_EXCEEDED statuses in gonacos_grpc_requests_total, which
// conflate slowloris with legitimate slow clients and handler timeouts.
func TestServerUnaryFrameReadTimeoutIncrementsMetric(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.ReadFrameTimeout = 50 * time.Millisecond
	registry := newStubMetricsRegistry()
	srv.MetricsRegistry = registry
	handlerCalled := 0
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		handlerCalled++
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// Body: 5-byte gRPC frame header claiming a 100-byte body, then
	// bytes dribbled 1 per 50ms. The 50ms ReadFrameTimeout fires
	// before the body completes, so the handler is never invoked.
	body := &slowReader{
		interval:  50 * time.Millisecond,
		preappend: []byte{0, 0, 0, 0, 100},
	}
	// Connection: close — the server otherwise drains the request
	// body for keep-alive reuse after the handler returns, but the
	// slow body would block that drain indefinitely.
	req, err := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr().String()+"/Request/request", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Close = true
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Client may see a connection error if the server closes
		// the connection before the client finishes sending the body.
		// The metric should still be recorded; verify it below.
	} else {
		resp.Body.Close()
	}

	// Give the metric a moment to be recorded — the timeout fires
	// near 50ms, but the goroutine cleanup may lag slightly.
	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mu.Lock()
		c, ok := registry.counters["gonacos_grpc_frame_read_timeouts_total//"]
		registry.mu.Unlock()
		if ok {
			c.mu.Lock()
			got := c.count
			c.mu.Unlock()
			if got >= 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gonacos_grpc_frame_read_timeouts_total not incremented (counters: %v)", registry.counters)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if handlerCalled != 0 {
		t.Errorf("handlerCalled = %d, want 0 (timed-out read must not invoke handler)", handlerCalled)
	}
}

// TestServerBiStreamFrameReadTimeoutIncrementsMetric verifies that a
// bistream RPC whose peer stalls mid-frame triggers ReadFrameTimeout
// in the recv closure, recording gonacos_grpc_frame_read_timeouts_total.
// The bistream path records the metric inside the recv closure (rather
// than in a switch case like unary/stream) because the read happens
// inside a callback the handler invokes — this test pins that code path.
//
// Uses a raw TCP connection rather than http.Post because the HTTP
// client's chunked-encoding buffer can delay body bytes, making the
// timeout non-deterministic. A raw connection lets us control exactly
// when bytes arrive: send the 5-byte frame header, then stall — the
// server's recv closure blocks waiting for the body, the 50ms deadline
// fires, and the metric is recorded.
func TestServerBiStreamFrameReadTimeoutIncrementsMetric(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.ReadFrameTimeout = 50 * time.Millisecond
	registry := newStubMetricsRegistry()
	srv.MetricsRegistry = registry
	srv.RegisterBiStream("BiRequestStream/bi", func(ctx context.Context, recv func() (Payload, error), send func(Payload) error) error {
		// Drain recv until error — the slow body will trigger
		// ErrReadFrameTimeout on the first frame.
		_, err := recv()
		return err
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send HTTP/1.1 POST with a 100-byte body declared via Content-Length.
	// Send only the 5-byte gRPC frame header (which claims a 100-byte
	// body), then stall — the server's recv blocks waiting for the
	// remaining body bytes, the 50ms deadline fires, and the metric
	// is recorded.
	requestLine := "POST /BiRequestStream/bi HTTP/1.1\r\n" +
		"Host: " + srv.Addr().String() + "\r\n" +
		"Content-Type: application/grpc\r\n" +
		"Content-Length: 105\r\n" + // 5 (header) + 100 (body)
		"Connection: close\r\n\r\n"
	if _, err := conn.Write([]byte(requestLine)); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	// Send the 5-byte gRPC frame header (uncompressed, 100-byte body).
	// Don't send the body — the server's recv will block waiting for
	// the body, the 50ms deadline will fire, and the metric will be
	// recorded.
	if _, err := conn.Write([]byte{0, 0, 0, 0, 100}); err != nil {
		t.Fatalf("write frame header: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mu.Lock()
		c, ok := registry.counters["gonacos_grpc_frame_read_timeouts_total//"]
		registry.mu.Unlock()
		if ok {
			c.mu.Lock()
			got := c.count
			c.mu.Unlock()
			if got >= 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gonacos_grpc_frame_read_timeouts_total not incremented for bistream (counters: %v)", registry.counters)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestServerRecordFrameReadTimeoutNilRegistryNoop verifies that
// recordFrameReadTimeout does not panic when MetricsRegistry is nil —
// production callers that opt out of metrics must not crash on timeout.
func TestServerRecordFrameReadTimeoutNilRegistryNoop(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	// MetricsRegistry is nil by default.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordFrameReadTimeout panicked with nil registry: %v", r)
		}
	}()
	srv.recordFrameReadTimeout()
}

// TestServerRateLimitRejectionIncrementsMetric verifies that when the
// per-IP rate limiter rejects a peer, the server increments
// gonacos_rate_limit_rejections_total{protocol="grpc"} — the alerting
// signal that gRPC rate limiting is firing. Without it, operators can
// only infer from gonacos_grpc_requests_total{status="8"}
// (RESOURCE_EXHAUSTED), which is indirect and breaks if any other
// path ever returns status 8.
func TestServerRateLimitRejectionIncrementsMetric(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	limiter := &stubRateLimiter{allowAt: 0} // deny all
	srv.RateLimiter = limiter
	registry := newStubMetricsRegistry()
	srv.MetricsRegistry = registry
	handlerCalled := 0
	srv.RegisterUnary("Request/request", func(ctx context.Context, req Payload) (Payload, error) {
		handlerCalled++
		return Payload{Metadata: Metadata{Type: "TestResponse"}}, nil
	})
	go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	body := encodeGRPCRequestBody(Payload{Metadata: Metadata{Type: "TestRequest"}})
	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("grpc-status") != "8" {
		t.Fatalf("grpc-status = %v, want 8 (RESOURCE_EXHAUSTED)", resp.Header.Get("grpc-status"))
	}

	// stubMetricsRegistry keys counters as name + "/" + labels["method"]
	// + "/" + labels["status"]. The rate-limit counter has a "protocol"
	// label (not "method" or "status"), so those slots are empty and
	// the key collapses to name + "//".
	key := "gonacos_rate_limit_rejections_total//"
	registry.mu.Lock()
	c, ok := registry.counters[key]
	registry.mu.Unlock()
	if !ok {
		t.Fatalf("no rate-limit counter recorded for key %q (counters: %v)", key, registry.counters)
	}
	c.mu.Lock()
	got := c.count
	c.mu.Unlock()
	if got != 1 {
		t.Errorf("gonacos_rate_limit_rejections_total{protocol=\"grpc\"} = %d, want 1", got)
	}
	if handlerCalled != 0 {
		t.Errorf("handlerCalled = %d, want 0 (rate-limited request must not invoke handler)", handlerCalled)
	}
}
