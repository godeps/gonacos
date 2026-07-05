package server

import (
	"net/http"
	"runtime/debug"

	"github.com/godeps/gonacos/pkg/protocol"
)

// recoveryMiddleware recovers from panics in downstream handlers. Without it,
// Go's net/http server recovers the panic but closes the connection without
// writing a response — the client sees a connection reset and there is no
// structured log line tying the panic to a request ID. With this middleware,
// a panic produces a 500 JSON response carrying the request ID and a single
// log line with the stack trace, so the server stays up and the operator can
// find the failing request.
type recoveryMiddleware struct {
	logger Logger
	next   http.Handler
}

func newRecoveryMiddleware(logger Logger, next http.Handler) http.Handler {
	return &recoveryMiddleware{logger: logger, next: next}
}

func (m *recoveryMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rv := recover(); rv != nil {
			rid := requestIDFromContext(r.Context())
			if m.logger != nil {
				m.logger.Warnf("panic recovered: %v\n%s rid=%s %s %s",
					rv, debug.Stack(), rid, r.Method, r.URL.RequestURI())
			}
			// If the inner handler already wrote headers before panicking
			// (e.g., streaming responses), WriteHeader would log a
			// "superfluous response.WriteHeader call" warning. That's
			// acceptable — the client already got partial output, and
			// we'd rather log the panic than silently swallow it.
			protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
				Message: "internal server error",
				Data:    map[string]string{"requestId": rid},
			})
		}
	}()
	m.next.ServeHTTP(w, r)
}
