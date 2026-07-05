package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLoginThrottleLocksAfterMaxFailures verifies that maxFailures
// consecutive failures lock the (ip, username) pair.
func TestLoginThrottleLocksAfterMaxFailures(t *testing.T) {
	throttle := NewLoginThrottle(3, time.Minute, 5*time.Minute)
	for i := 0; i < 3; i++ {
		throttle.recordFailure("10.0.0.1", "admin")
	}
	locked, retry := throttle.isLocked("10.0.0.1", "admin")
	if !locked {
		t.Fatal("after 3 failures: expected locked=true")
	}
	if retry <= 0 || retry > 5*time.Minute {
		t.Fatalf("retryAfter out of range: %v", retry)
	}
}

// TestLoginThrottleSuccessResetsCounter verifies that a successful login
// clears the failure counter so a subsequent failure doesn't immediately
// count toward the lockout.
func TestLoginThrottleSuccessResetsCounter(t *testing.T) {
	throttle := NewLoginThrottle(3, time.Minute, 5*time.Minute)
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordSuccess("10.0.0.1", "admin")
	// Now 2 more failures should not lock (counter reset to 0, need 3).
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	locked, _ := throttle.isLocked("10.0.0.1", "admin")
	if locked {
		t.Fatal("after success + 2 failures: expected locked=false")
	}
}

// TestLoginThrottlePerUsernameIsolation verifies that failures for one
// username don't lock a different username at the same IP.
func TestLoginThrottlePerUsernameIsolation(t *testing.T) {
	throttle := NewLoginThrottle(2, time.Minute, 5*time.Minute)
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	// admin is locked, root is not.
	locked, _ := throttle.isLocked("10.0.0.1", "admin")
	if !locked {
		t.Fatal("admin should be locked")
	}
	locked, _ = throttle.isLocked("10.0.0.1", "root")
	if locked {
		t.Fatal("root should NOT be locked")
	}
}

// TestLoginThrottlePerIPIsolation verifies that failures from one IP don't
// lock a different IP for the same username.
func TestLoginThrottlePerIPIsolation(t *testing.T) {
	throttle := NewLoginThrottle(2, time.Minute, 5*time.Minute)
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	// 10.0.0.1 admin locked, 10.0.0.2 admin not.
	locked, _ := throttle.isLocked("10.0.0.1", "admin")
	if !locked {
		t.Fatal("10.0.0.1 admin should be locked")
	}
	locked, _ = throttle.isLocked("10.0.0.2", "admin")
	if locked {
		t.Fatal("10.0.0.2 admin should NOT be locked")
	}
}

// TestLoginThrottleLockExpiry verifies that a lock expires after the
// lockout duration.
func TestLoginThrottleLockExpiry(t *testing.T) {
	throttle := NewLoginThrottle(1, time.Minute, 50*time.Millisecond)
	throttle.recordFailure("10.0.0.1", "admin")
	locked, _ := throttle.isLocked("10.0.0.1", "admin")
	if !locked {
		t.Fatal("expected locked immediately after failure")
	}
	time.Sleep(60 * time.Millisecond)
	locked, _ = throttle.isLocked("10.0.0.1", "admin")
	if locked {
		t.Fatal("expected lock expired after 60ms")
	}
}

// TestLoginThrottleFailWindowExpiry verifies that the failure counter
// resets when the fail window elapses (so old failures don't count toward
// a fresh lockout).
func TestLoginThrottleFailWindowExpiry(t *testing.T) {
	throttle := NewLoginThrottle(3, 50*time.Millisecond, 5*time.Minute)
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	time.Sleep(60 * time.Millisecond)
	// Window expired; 2 more failures should not lock (counter reset).
	throttle.recordFailure("10.0.0.1", "admin")
	throttle.recordFailure("10.0.0.1", "admin")
	locked, _ := throttle.isLocked("10.0.0.1", "admin")
	if locked {
		t.Fatal("after window expiry + 2 failures: expected locked=false")
	}
}

// TestLoginThrottleMiddlewareRejectsLocked verifies that a locked pair
// receives 429 from the middleware without calling the inner handler.
func TestLoginThrottleMiddlewareRejectsLocked(t *testing.T) {
	throttle := NewLoginThrottle(2, time.Minute, 5*time.Minute)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := newLoginThrottleMiddleware(throttle, inner)

	// Two failures to lock.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader("username=admin&password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		// Simulate the inner handler returning 401.
		inner401 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusUnauthorized)
		})
		mw401 := newLoginThrottleMiddleware(throttle, inner401)
		mw401.ServeHTTP(w, req)
	}

	// Third attempt should be locked and rejected at the middleware.
	called = false
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader("username=admin&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if called {
		t.Fatal("inner handler should NOT be called when locked")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login: got %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header missing on locked login")
	}
}

// TestLoginThrottleMiddlewarePassesUnlocked verifies that an unlocked pair
// reaches the inner handler.
func TestLoginThrottleMiddlewarePassesUnlocked(t *testing.T) {
	throttle := NewLoginThrottle(5, time.Minute, 5*time.Minute)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := newLoginThrottleMiddleware(throttle, inner)

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader("username=admin&password=right"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if !called {
		t.Fatal("inner handler should be called when unlocked")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("unlocked login: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestLoginThrottleConcurrent exercises the mutex under concurrent access.
func TestLoginThrottleConcurrent(t *testing.T) {
	throttle := NewLoginThrottle(100, time.Minute, 5*time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := "10.0.0." + string(rune('0'+i%10))
			throttle.recordFailure(ip, "admin")
			_, _ = throttle.isLocked(ip, "admin")
		}(i)
	}
	wg.Wait()
}
