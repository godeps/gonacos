package app

import (
	"context"
	"net/http"
	"time"

	"github.com/godeps/gonacos/pkg/protocol"
)

// readinessCheckTimeout caps how long a readiness probe waits for a
// dependency (e.g., Redis Ping) before returning 503. Tight by design so a
// stuck dependency doesn't make load balancers queue traffic against an
// unready backend.
const readinessCheckTimeout = 2 * time.Second

// ReadinessChecker returns nil if the server is ready to serve traffic, or an
// error describing why it is not ready. Used by the /readiness endpoints to
// return 503 when a dependency (e.g., Redis) is unreachable.
//
// Implementations must be safe for concurrent use. A nil ReadinessChecker is
// treated as always-ready (matching the legacy behavior).
type ReadinessChecker interface {
	CheckReadiness(ctx context.Context) error
}

// ReadinessCheckerFunc lets a bare function satisfy ReadinessChecker.
type ReadinessCheckerFunc func(ctx context.Context) error

func (f ReadinessCheckerFunc) CheckReadiness(ctx context.Context) error { return f(ctx) }

// readinessHandler returns an http.HandlerFunc that runs the readiness check
// and returns 200/ok when ready, 503/error when not. A nil checker always
// returns ready.
func readinessHandler(checker ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if checker == nil {
			protocol.WriteResult(w, http.StatusOK, "ok")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readinessCheckTimeout)
		defer cancel()
		if err := checker.CheckReadiness(ctx); err != nil {
			protocol.WriteError(w, http.StatusServiceUnavailable, protocol.Error{
				Code:    http.StatusServiceUnavailable,
				Message: "not ready: " + err.Error(),
			})
			return
		}
		protocol.WriteResult(w, http.StatusOK, "ok")
	}
}
