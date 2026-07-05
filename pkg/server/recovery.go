package server

import (
	"net/http"
	"runtime/debug"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
)

// recoveryMiddleware recovers from panics in downstream handlers. Without it,
// Go's net/http server recovers the panic but closes the connection without
// writing a response — the client sees a connection reset and there is no
// structured log line tying the panic to a request ID. With this middleware,
// a panic produces a 500 JSON response carrying the request ID and a single
// log line with the stack trace, so the server stays up and the operator can
// find the failing request.
//
// When a registry is wired, the middleware increments
// gonacos_http_panics_total on every recovered panic — the alerting signal
// for "a handler is crashing". A non-zero rate pages on-call: a panic is
// either a freshly-deployed bug or a malformed request the handler can't
// process. The log line carries the stack; the metric carries the rate.
type recoveryMiddleware struct {
	logger   Logger
	registry *observability.Registry
	panicCtr *observability.Counter
	next     http.Handler
}

func newRecoveryMiddleware(logger Logger, next http.Handler) http.Handler {
	return &recoveryMiddleware{logger: logger, next: next}
}

// newRecoveryMiddlewareWithRegistry is the same as newRecoveryMiddleware
// but also wires a panic counter into the registry. The counter is cached
// on the struct so we don't pay a map lookup per request — only per panic,
// which is the rare path.
func newRecoveryMiddlewareWithRegistry(logger Logger, next http.Handler, registry *observability.Registry) http.Handler {
	mw := &recoveryMiddleware{logger: logger, next: next, registry: registry}
	if registry != nil {
		mw.panicCtr = registry.Counter("gonacos_http_panics_total", nil)
	}
	return mw
}

func (m *recoveryMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rv := recover(); rv != nil {
			rid := requestIDFromContext(r.Context())
			if m.logger != nil {
				m.logger.Warnf("panic recovered: %v\n%s rid=%s %s %s",
					rv, debug.Stack(), rid, r.Method, r.URL.RequestURI())
			}
			if m.panicCtr != nil {
				m.panicCtr.Inc()
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
