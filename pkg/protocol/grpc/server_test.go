package grpc

import (
	"bytes"
	"context"
	"fmt"
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
	if !strings.Contains(logBuf.String(), "grpc panic recovered") {
		t.Errorf("log missing 'grpc panic recovered': %s", logBuf.String())
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

	logged := logBuf.String()
	if !strings.Contains(logged, "grpc POST /Request/request") {
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
	mu        sync.Mutex
	observed  int64
	obsCount  int
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
}
