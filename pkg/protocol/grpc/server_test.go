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
