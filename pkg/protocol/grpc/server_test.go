package grpc

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
	var logBuf bytes.Buffer
	srv.Logf = func(format string, args ...any) {
		logBuf.WriteString(format)
		logBuf.WriteByte('\n')
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
