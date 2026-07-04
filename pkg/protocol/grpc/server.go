package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status codes matching gRPC's codes.Code.
const (
	StatusOK                 = 0
	StatusCancelled          = 1
	StatusUnknown            = 2
	StatusInvalidArgument    = 3
	StatusDeadlineExceeded   = 4
	StatusNotFound           = 5
	StatusAlreadyExists      = 6
	StatusPermissionDenied   = 7
	StatusResourceExhausted  = 8
	StatusFailedPrecondition = 9
	StatusAborted            = 10
	StatusOutOfRange         = 11
	StatusUnimplemented      = 12
	StatusInternal           = 13
	StatusUnavailable        = 14
	StatusDataLoss           = 15
	StatusUnauthenticated    = 16
)

// StatusError carries a gRPC status code and message.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("grpc code %d: %s", e.Code, e.Message) }

// NewStatusError creates a StatusError.
func NewStatusError(code int, message string) *StatusError {
	return &StatusError{Code: code, Message: message}
}

// Handler handles a unary Request RPC. The handler receives the decoded
// Payload and returns a response Payload or an error.
type Handler func(ctx context.Context, req Payload) (Payload, error)

// StreamHandler handles a RequestStream RPC (server-streaming). The handler
// receives the request Payload and a sender for response frames.
type StreamHandler func(ctx context.Context, req Payload, send func(Payload) error) error

// BiStreamHandler handles a BiRequestStream RPC (bidi-streaming). The handler
// reads request frames from recv and sends response frames via send.
type BiStreamHandler func(ctx context.Context, recv func() (Payload, error), send func(Payload) error) error

// Server is a minimal gRPC server over HTTP/2 using net/http.
type Server struct {
	mu        sync.RWMutex
	unary     map[string]Handler
	stream    map[string]StreamHandler
	bistream  map[string]BiStreamHandler
	server    *http.Server
	listener  net.Listener
}

// NewServer returns an empty gRPC server.
func NewServer() *Server {
	return &Server{
		unary:    map[string]Handler{},
		stream:   map[string]StreamHandler{},
		bistream: map[string]BiStreamHandler{},
	}
}

// RegisterUnary registers a handler for the Request service.
func (s *Server) RegisterUnary(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unary[method] = h
}

// RegisterStream registers a handler for the RequestStream service.
func (s *Server) RegisterStream(method string, h StreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stream[method] = h
}

// RegisterBiStream registers a handler for the BiRequestStream service.
func (s *Server) RegisterBiStream(method string, h BiStreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bistream[method] = h
}

// ListenAndServe starts the gRPC server on the given address. The server
// uses unencrypted HTTP/2 (h2c) so the gRPC client can connect without TLS.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %q: %w", addr, err)
	}
	return s.Serve(ln)
}

// Serve runs the gRPC server on a pre-bound listener using unencrypted
// HTTP/2 (h2c). Useful when the caller wants to pre-bind the port (for
// example, to capture the actual port when binding to :0) before starting
// the server. Takes ownership of ln; closing ln is done via [Server.Shutdown].
func (s *Server) Serve(ln net.Listener) error {
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{
		Handler:     s,
		IdleTimeout: 5 * time.Minute,
		Protocols:   protocols,
	}
	s.mu.Unlock()
	return s.server.Serve(ln)
}

// ListenAndServeTLS starts the gRPC server with TLS. The server negotiates
// HTTP/2 via ALPN (h2) so standard gRPC clients can connect with TLS enabled.
// certFile and keyFile must be PEM-encoded. Returns an error if either file
// cannot be read or parsed.
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %q: %w", addr, err)
	}
	return s.ServeTLS(ln, certFile, keyFile)
}

// ServeTLS runs the gRPC server with TLS on a pre-bound listener. The server
// negotiates HTTP/2 via ALPN (h2). Useful when the caller wants to pre-bind
// the port (for example, to capture the actual port when binding to :0)
// before starting the server.
func (s *Server) ServeTLS(ln net.Listener, certFile, keyFile string) error {
	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{
		Handler:     s,
		IdleTimeout: 5 * time.Minute,
	}
	s.mu.Unlock()
	return s.server.ServeTLS(ln, certFile, keyFile)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	srv := s.server
	s.mu.RUnlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Addr returns the listen address; nil before ListenAndServe.
func (s *Server) Addr() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// clientIPKey is the context key used to carry the remote client IP from the
// HTTP layer into the gRPC handler. Handlers use ClientIPFromContext to
// recover the IP for listener tracking and beta-aware config queries.
type clientIPKey struct{}

// ClientIPFromContext returns the client IP injected by the server, or "" if
// the context was not created by ServeHTTP.
func ClientIPFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(clientIPKey{}).(string); ok {
		return v
	}
	return ""
}

// ServeHTTP routes the incoming HTTP/2 request to the registered handler.
// The path format is /{Service}/{Method}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/grpc") {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		writeGRPCStatus(w, StatusUnimplemented, "unknown path: "+r.URL.Path)
		return
	}
	service, method := parts[0], parts[1]
	full := service + "/" + method

	s.mu.RLock()
	unary, hasUnary := s.unary[full]
	stream, hasStream := s.stream[full]
	bistream, hasBi := s.bistream[full]
	s.mu.RUnlock()

	if !hasUnary && !hasStream && !hasBi {
		writeGRPCStatus(w, StatusUnimplemented, "unknown method: "+full)
		return
	}

	ctx := context.WithValue(r.Context(), clientIPKey{}, clientIPFromRequest(r))

	switch {
	case hasUnary:
		s.handleUnary(ctx, w, r, unary)
	case hasStream:
		s.handleStream(ctx, w, r, stream)
	case hasBi:
		s.handleBiStream(ctx, w, r, bistream)
	}
}

func (s *Server) handleUnary(ctx context.Context, w http.ResponseWriter, r *http.Request, h Handler) {
	frame, err := ReadFrame(r.Body)
	if err != nil && !errors.Is(err, io.EOF) {
		writeGRPCStatus(w, StatusInternal, "read frame: "+err.Error())
		return
	}
	req, err := DecodePayload(frame.Payload)
	if err != nil {
		writeGRPCStatus(w, StatusInternal, "decode payload: "+err.Error())
		return
	}
	resp, err := h(ctx, req)
	if err != nil {
		code, msg := statusFromError(err)
		writeGRPCStatus(w, code, msg)
		return
	}
	writeGRPCPayload(w, resp)
}

func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, r *http.Request, h StreamHandler) {
	frame, err := ReadFrame(r.Body)
	if err != nil && !errors.Is(err, io.EOF) {
		writeGRPCStatus(w, StatusInternal, "read frame: "+err.Error())
		return
	}
	req, err := DecodePayload(frame.Payload)
	if err != nil {
		writeGRPCStatus(w, StatusInternal, "decode payload: "+err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGRPCStatus(w, StatusInternal, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(p Payload) error {
		return writeGRPCStreamFrame(w, flusher, p)
	}
	if err := h(ctx, req, send); err != nil {
		code, msg := statusFromError(err)
		writeGRPCStreamStatus(w, flusher, code, msg)
		return
	}
	writeGRPCStreamStatus(w, flusher, StatusOK, "")
}

func (s *Server) handleBiStream(ctx context.Context, w http.ResponseWriter, r *http.Request, h BiStreamHandler) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGRPCStatus(w, StatusInternal, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	recv := func() (Payload, error) {
		frame, err := ReadFrame(r.Body)
		if err != nil {
			return Payload{}, err
		}
		return DecodePayload(frame.Payload)
	}
	send := func(p Payload) error {
		return writeGRPCStreamFrame(w, flusher, p)
	}
	if err := h(ctx, recv, send); err != nil {
		code, msg := statusFromError(err)
		writeGRPCStreamStatus(w, flusher, code, msg)
		return
	}
	writeGRPCStreamStatus(w, flusher, StatusOK, "")
}

func statusFromError(err error) (int, string) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code, se.Message
	}
	return StatusInternal, err.Error()
}

func writeGRPCStatus(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/grpc")
	// For status-only responses (no body), set grpc-status as a regular
	// header so both HTTP/1.1 and HTTP/2 clients can read it from the
	// response headers without needing to parse trailers.
	w.Header().Set("grpc-status", strconv.Itoa(code))
	if message != "" {
		w.Header().Set("grpc-message", message)
	}
	w.WriteHeader(http.StatusOK)
}

func writeGRPCPayload(w http.ResponseWriter, p Payload) {
	w.Header().Set("Content-Type", "application/grpc")
	// Set grpc-status as a header so HTTP/1.1 clients can read it before
	// consuming the body, and also declare it as a trailer for HTTP/2 gRPC
	// clients that expect it there.
	w.Header().Set("grpc-status", strconv.Itoa(StatusOK))
	w.Header().Add("Trailer", "grpc-status, grpc-message")
	w.WriteHeader(http.StatusOK)
	_ = WriteFrame(w, Frame{Payload: p.Encode()})
	w.Header().Set("grpc-status", strconv.Itoa(StatusOK))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeGRPCStreamFrame(w http.ResponseWriter, flusher http.Flusher, p Payload) error {
	if err := WriteFrame(w, Frame{Payload: p.Encode()}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeGRPCStreamStatus(w http.ResponseWriter, flusher http.Flusher, code int, message string) {
	w.Header().Set("grpc-status", strconv.Itoa(code))
	if message != "" {
		w.Header().Set("grpc-message", message)
	}
	flusher.Flush()
}

// DefaultServer is the package-level gRPC server used by the app.
var (
	defaultServer     *Server
	defaultServerOnce sync.Once
)

// clientIPFromRequest extracts the remote IP from an HTTP request, honoring
// the X-Forwarded-For and X-Real-IP headers so requests routed through a
// proxy still report the originating client.
func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// DefaultServer returns the shared gRPC server.
func DefaultServer() *Server {
	defaultServerOnce.Do(func() {
		defaultServer = NewServer()
	})
	return defaultServer
}

// ServeInBackground launches the gRPC server on addr in a goroutine. Errors
// are logged; the caller should use Shutdown to stop.
func ServeInBackground(addr string) {
	srv := DefaultServer()
	go func() {
		if err := srv.ListenAndServe(addr); err != nil && err != http.ErrServerClosed {
			log.Printf("grpc server error: %v", err)
		}
	}()
	// Give the listener a moment to bind so tests can connect immediately.
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
}

// ShutdownDefault stops the shared gRPC server.
func ShutdownDefault(ctx context.Context) error {
	if defaultServer == nil {
		return nil
	}
	return defaultServer.Shutdown(ctx)
}
