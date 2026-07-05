package app

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
)

// LoginThrottle rate-limits failed login attempts per (clientIP, username)
// pair. After maxFailures consecutive failures within the failWindow, the
// pair is locked for the lockoutDuration. Successful logins reset the
// failure counter for that pair.
//
// Design notes:
//   - Per (IP, username) bucket instead of per-IP alone so a single abusive
//     client can't lock out all users behind the same NAT.
//   - Per-username bucket instead of per-IP alone so a single abusive client
//     guessing different usernames doesn't lock out a legitimate user at the
//     same IP.
//   - The counter is "consecutive failures" — a success clears it. This
//     means an attacker who knows the password is not blocked.
//   - Cleanup is piggybacked on each lock check; no background goroutine.
type LoginThrottle struct {
	mu              sync.Mutex
	maxFailures     int
	failWindow      time.Duration
	lockoutDuration time.Duration
	failures        map[string]*loginFailState
}

type loginFailState struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

// NewLoginThrottle constructs a LoginThrottle with the given policy. Zero
// values fall back to safe defaults (5 failures, 5m window, 15m lockout).
func NewLoginThrottle(maxFailures int, failWindow, lockoutDuration time.Duration) *LoginThrottle {
	if maxFailures < 1 {
		maxFailures = 5
	}
	if failWindow <= 0 {
		failWindow = 5 * time.Minute
	}
	if lockoutDuration <= 0 {
		lockoutDuration = 15 * time.Minute
	}
	return &LoginThrottle{
		maxFailures:     maxFailures,
		failWindow:      failWindow,
		lockoutDuration: lockoutDuration,
		failures:        map[string]*loginFailState{},
	}
}

// isLocked returns whether the (ip, username) pair is currently locked and
// how long until the lock expires. Caller must NOT hold mu.
//
// A zero lockedUntil means the pair is being tracked for failure counting but
// has not yet reached the lockout threshold — we return false WITHOUT deleting
// the entry, so the failure counter can continue accumulating. Only entries
// whose lock has actually expired (lockedUntil in the past) are dropped.
func (l *LoginThrottle) isLocked(ip, username string) (locked bool, retryAfter time.Duration) {
	key := ip + "|" + username
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.failures[key]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if s.lockedUntil.IsZero() {
		return false, 0
	}
	if now.After(s.lockedUntil) {
		// Lock expired; reset.
		delete(l.failures, key)
		return false, 0
	}
	return true, s.lockedUntil.Sub(now)
}

// recordFailure increments the failure counter for (ip, username), locking
// the pair when the counter reaches maxFailures.
func (l *LoginThrottle) recordFailure(ip, username string) {
	key := ip + "|" + username
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	s, ok := l.failures[key]
	if !ok || now.Sub(s.firstFailAt) > l.failWindow {
		s = &loginFailState{firstFailAt: now}
		l.failures[key] = s
	}
	s.count++
	if s.count >= l.maxFailures {
		s.lockedUntil = now.Add(l.lockoutDuration)
	}
}

// recordSuccess clears the failure counter for (ip, username).
func (l *LoginThrottle) recordSuccess(ip, username string) {
	key := ip + "|" + username
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// cleanup drops expired entries. Called inline on each lock check to avoid a
// background goroutine. Under sustained attack the map grows to the number
// of distinct (IP, username) pairs being probed within failWindow, which is
// bounded by the rate limiter upstream.
func (l *LoginThrottle) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, s := range l.failures {
		if now.After(s.lockedUntil) && now.Sub(s.firstFailAt) > l.failWindow {
			delete(l.failures, k)
		}
	}
}

// loginThrottleMiddleware wraps the login handler with brute-force protection.
// A locked (ip, username) pair receives 429 with Retry-After.
type loginThrottleMiddleware struct {
	throttle  *LoginThrottle
	next      http.HandlerFunc
	registry  *observability.Registry
	loginOK   *observability.Counter
	loginFail *observability.Counter
	loginLock *observability.Counter
}

func newLoginThrottleMiddleware(throttle *LoginThrottle, next http.HandlerFunc, registry *observability.Registry) http.Handler {
	if throttle == nil {
		return next
	}
	mw := &loginThrottleMiddleware{throttle: throttle, next: next, registry: registry}
	if registry != nil {
		mw.loginOK = registry.Counter("gonacos_login_attempts_total", map[string]string{"result": "success"})
		mw.loginFail = registry.Counter("gonacos_login_attempts_total", map[string]string{"result": "failure"})
		mw.loginLock = registry.Counter("gonacos_login_attempts_total", map[string]string{"result": "locked"})
	}
	return mw
}

func (m *loginThrottleMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	username := formValue(r, "username")
	ip := clientIPForLimit(r)

	if locked, retryAfter := m.throttle.isLocked(ip, username); locked {
		secs := int(retryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		if m.loginLock != nil {
			m.loginLock.Inc()
		}
		protocol.WriteError(w, http.StatusTooManyRequests, protocol.Error{
			Code:    http.StatusTooManyRequests,
			Message: "account temporarily locked due to repeated login failures",
		})
		return
	}

	// Wrap the response writer to capture the status code set by the login
	// handler so we can record success (2xx) or failure (everything else).
	rec := &statusRecorder{ResponseWriter: w}
	m.next.ServeHTTP(rec, r)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status >= 200 && rec.status < 300 {
		m.throttle.recordSuccess(ip, username)
		if m.loginOK != nil {
			m.loginOK.Inc()
		}
	} else if rec.status == http.StatusUnauthorized || rec.status == http.StatusForbidden {
		m.throttle.recordFailure(ip, username)
		if m.loginFail != nil {
			m.loginFail.Inc()
		}
		// Opportunistic cleanup; cheap because the map is bounded by the
		// rate limiter.
		if n := m.throttle.entryCount(); n > 1000 {
			m.throttle.cleanup()
		}
	}
}

// entryCount returns the current number of tracked (ip, username) pairs.
// Used by the middleware to decide when to trigger cleanup.
func (l *LoginThrottle) entryCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.failures)
}

// statusRecorder captures the status code set by downstream handlers. Kept
// here in app package because the server package's version is not exported.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}
