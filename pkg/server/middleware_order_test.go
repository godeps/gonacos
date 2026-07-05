package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/app"
)

// TestRequestIDPropagatedToAccessLog verifies that the request ID assigned by
// the request ID middleware appears in the access log line. Before the
// middleware-order fix, RequestID was inside RequestLog — the rid was set
// in the context of the inner request, not the outer one RequestLog reads,
// so the log line always had rid="".
func TestRequestIDPropagatedToAccessLog(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Build the chain in the production order: RequestID wraps RequestLog.
	chain := newRequestIDMiddleware(
		newRequestLogMiddleware(logger, true, nil, inner),
	)

	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)

	logLine := buf.String()
	if !strings.Contains(logLine, "rid=rid-") {
		t.Fatalf("log line missing rid=rid- prefix: %q", logLine)
	}
	// The response header should also carry the rid.
	if rid := w.Header().Get("X-Request-Id"); rid == "" || !strings.HasPrefix(rid, "rid-") {
		t.Fatalf("response X-Request-Id missing or wrong: %q", rid)
	}
}

// TestRequestIDOnRejectedRequests verifies that rejection responses (413 from
// MaxBody, 429 from RateLimit) carry the X-Request-Id header. Before the
// middleware-order fix, MaxBody and RateLimit were outside RequestID, so
// they returned before the rid was assigned — the rejection response had no
// X-Request-Id header for correlation.
func TestRequestIDOnRejectedRequests(t *testing.T) {
	t.Run("MaxBody 413 carries rid", func(t *testing.T) {
		var buf bytes.Buffer
		logger := stubLogger{buf: &buf}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read the body so MaxBytesReader triggers the 413. In
			// httptest.NewRecorder, MaxBytesReader returns an error
			// (http.MaxBytesError) but does not auto-set the 413 status
			// the way the real http.Server framework does — the handler
			// must translate the error into a status itself.
			_, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		// Production order: RequestID wraps MaxBody.
		chain := newRequestIDMiddleware(
			newRequestLogMiddleware(logger, true, nil,
				app.NewMaxBodyMiddleware(10, inner),
			),
		)

		req := httptest.NewRequest(http.MethodPost, "/v3/cs/config", strings.NewReader("oversized body padding past 10 bytes"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status: got %d, want 413", w.Code)
		}
		if rid := w.Header().Get("X-Request-Id"); rid == "" || !strings.HasPrefix(rid, "rid-") {
			t.Fatalf("413 response missing X-Request-Id: %q", rid)
		}
		// The log line should also carry the rid.
		if !strings.Contains(buf.String(), "rid=rid-") {
			t.Fatalf("413 log line missing rid: %q", buf.String())
		}
	})

	t.Run("RateLimit 429 carries rid", func(t *testing.T) {
		var buf bytes.Buffer
		logger := stubLogger{buf: &buf}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		// Production order: RequestID wraps RateLimit. Use a very low rps
		// so the second request is rejected.
		rl := app.NewRateLimiter(1, 1)

		chain := newRequestIDMiddleware(
			newRequestLogMiddleware(logger, true, nil,
				app.NewRateLimitMiddleware(rl, inner),
			),
		)

		// First request: allowed.
		req1 := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
		req1.RemoteAddr = "10.0.0.1:1234"
		w1 := httptest.NewRecorder()
		chain.ServeHTTP(w1, req1)
		if w1.Code != http.StatusOK {
			t.Fatalf("first request: got %d, want 200", w1.Code)
		}

		// Second request: rejected (429). Must still carry rid.
		req2 := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
		req2.RemoteAddr = "10.0.0.1:1234"
		w2 := httptest.NewRecorder()
		chain.ServeHTTP(w2, req2)
		if w2.Code != http.StatusTooManyRequests {
			t.Fatalf("second request: got %d, want 429", w2.Code)
		}
		if rid := w2.Header().Get("X-Request-Id"); rid == "" || !strings.HasPrefix(rid, "rid-") {
			t.Fatalf("429 response missing X-Request-Id: %q", rid)
		}
	})
}

// TestRequestIDOnPanic500 verifies that a 500 from a panic carries the rid
// in both the response body and the log line. This was already working
// (RequestID was outside Recovery), but the test guards against regressions
// in the middleware ordering.
func TestRequestIDOnPanic500(t *testing.T) {
	var buf bytes.Buffer
	logger := stubLogger{buf: &buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	// Production order: RequestID wraps RequestLog wraps Recovery.
	chain := newRequestIDMiddleware(
		newRequestLogMiddleware(logger, true, nil,
			newRecoveryMiddleware(logger, inner),
		),
	)

	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
	if rid := w.Header().Get("X-Request-Id"); rid == "" || !strings.HasPrefix(rid, "rid-") {
		t.Fatalf("500 response missing X-Request-Id: %q", rid)
	}
	if !strings.Contains(buf.String(), "rid=rid-") {
		t.Fatalf("panic log line missing rid: %q", buf.String())
	}
}
