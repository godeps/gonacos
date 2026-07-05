package server

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// requestIDHeader is the standard header used to correlate a request across
// the load balancer, the gonacos process, and downstream logs.
const requestIDHeader = "X-Request-Id"

// requestIDKey is the context key carrying the request ID.
type requestIDKey struct{}

// requestIDFromContext returns the request ID injected by
// [newRequestIDMiddleware], or "" when none is set.
func requestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// requestIDMiddleware ensures every request has an X-Request-Id. If the
// incoming request has one (e.g., from an upstream proxy), it is reused;
// otherwise a fresh ID is generated. The ID is set on the response header
// and injected into the request context so downstream handlers and the
// access log can include it.
type requestIDMiddleware struct {
	next http.Handler
}

func newRequestIDMiddleware(next http.Handler) http.Handler {
	return &requestIDMiddleware{next: next}
}

func (m *requestIDMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get(requestIDHeader)
	if id == "" {
		id = nextRequestID()
	}
	w.Header().Set(requestIDHeader, id)
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
	m.next.ServeHTTP(w, r)
}

// requestIDSeq is a monotonic counter paired with the process start time so
// request IDs are unique within a process and ordered by issue time.
var requestIDSeq atomic.Uint64

// processStartNano captures the process start time used as the prefix for
// request IDs so IDs from different processes don't collide.
var processStartNano = time.Now().UnixNano()

func nextRequestID() string {
	return fmt.Sprintf("rid-%d-%d", processStartNano, requestIDSeq.Add(1))
}
