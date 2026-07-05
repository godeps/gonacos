package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

// AuditAction is the category of auditable event. Keep this list small and
// stable — operators build dashboards and alerts on these strings.
type AuditAction string

const (
	AuditActionLogin            AuditAction = "login"
	AuditActionLoginFailed      AuditAction = "login_failed"
	AuditActionUserCreate       AuditAction = "user_create"
	AuditActionUserDelete       AuditAction = "user_delete"
	AuditActionUserUpdate       AuditAction = "user_update"
	AuditActionRoleCreate       AuditAction = "role_create"
	AuditActionRoleDelete       AuditAction = "role_delete"
	AuditActionPermissionCreate AuditAction = "permission_create"
	AuditActionPermissionDelete AuditAction = "permission_delete"
	AuditActionNamespaceCreate  AuditAction = "namespace_create"
	AuditActionNamespaceUpdate  AuditAction = "namespace_update"
	AuditActionNamespaceDelete  AuditAction = "namespace_delete"
	AuditActionConfigPublish    AuditAction = "config_publish"
	AuditActionConfigDelete     AuditAction = "config_delete"
	AuditActionBackup           AuditAction = "backup"
	AuditActionRestore          AuditAction = "restore"
)

// AuditResult is the outcome of an auditable event.
type AuditResult string

const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailure AuditResult = "failure"
)

// AuditEvent records a security-relevant operation. Fields are deliberately
// minimal so the log line stays scannable. Detail carries operation-specific
// context (e.g., "namespaceId=foo" or "reason=invalid credentials").
type AuditEvent struct {
	Timestamp time.Time   `json:"timestamp"`
	Action    AuditAction `json:"action"`
	User      string      `json:"user"`
	IP        string      `json:"ip"`
	Resource  string      `json:"resource,omitempty"`
	Result    AuditResult `json:"result"`
	Detail    string      `json:"detail,omitempty"`
}

// AuditLogger receives audit events. Implementations typically write to the
// same logger as access logs, but may forward to a SIEM or dedicated audit
// store. A nil AuditLogger is treated as a no-op.
type AuditLogger interface {
	Log(event AuditEvent)
}

// AuditLogReopener is an optional interface implemented by AuditLoggers
// that hold a long-lived file descriptor. Reopen closes the current
// descriptor and opens a fresh one at the configured path.
//
// The canonical use case is logrotate(8) with SIGHUP: the operator renames
// the audit log file (audit.log -> audit.log.1) and sends SIGHUP to gonacos.
// Without Reopen, the logger would keep writing to the renamed inode
// (audit.log.1) and the new audit.log would stay empty. With Reopen, the
// logger closes the old fd and opens the new file at the configured path.
//
// The alternative (logrotate's copytruncate mode) races: events in flight
// during the copy-truncate window are lost. SIGHUP+Reopen is the
// race-free rotation strategy.
//
// Implementations must be safe to call while Log is concurrent: Reopen
// takes the same lock as Log so an in-flight write completes before the
// fd is swapped.
type AuditLogReopener interface {
	Reopen() error
}

// noopAuditLogger discards all events. Used when no logger is configured.
type noopAuditLogger struct{}

func (noopAuditLogger) Log(AuditEvent) {}

// loggerAuditLogger writes events to a [Logger] at INFO level using a
// key=value format that matches the access log style. One line per event
// so the audit trail is greppable.
type loggerAuditLogger struct {
	logger interface {
		Infof(format string, args ...any)
	}
}

func (l loggerAuditLogger) Log(e AuditEvent) {
	fields := "audit action=" + string(e.Action) +
		" user=" + e.User +
		" ip=" + e.IP +
		" result=" + string(e.Result)
	if e.Resource != "" {
		fields += " resource=" + e.Resource
	}
	if e.Detail != "" {
		fields += " detail=" + e.Detail
	}
	l.logger.Infof("%s", fields)
}

// NewLoggerAuditLogger adapts a [Logger]-shaped value to the AuditLogger
// interface. Events are written at INFO level.
func NewLoggerAuditLogger(logger interface {
	Infof(format string, args ...any)
}) AuditLogger {
	if logger == nil {
		return noopAuditLogger{}
	}
	return loggerAuditLogger{logger: logger}
}

// fileAuditLogger writes JSON-encoded audit events to a dedicated file, one
// event per line (JSON-lines). The file is opened in append mode (created
// if missing) and writes are serialized with a mutex so concurrent Log
// calls don't interleave. Rotation is the operator's responsibility
// (logrotate(8) with copytruncate, or similar) — the file handle is kept
// open for the process lifetime to avoid per-event open overhead.
//
// On open or write failure the logger falls back to stderr so events are
// not lost silently; the underlying file handle is closed and reopened on
// the next successful open.
type fileAuditLogger struct {
	mu   sync.Mutex
	path string
	f    *os.File
	// metrics records write-failure counters so operators can alert
	// from /metrics when the audit pipeline is silently dropping
	// events. Nil when no registry is wired (tests, embedders that
	// opted out of observability) — the stderr fallback still fires.
	metrics *observability.Registry
	// maxBytes is the file size threshold that triggers automatic
	// rotation. When written bytes reach this threshold, the current
	// file is closed, renamed to .1 (shifting existing backups down),
	// and a fresh file is opened. Zero disables size-based rotation;
	// the operator must rely on SIGHUP + logrotate(8) alone. Size-
	// based rotation is the safety net for deployments where
	// logrotate is not configured — without it, a high-volume audit
	// stream can fill the disk in hours.
	maxBytes int64
	// maxBackups is the number of rotated backup files to keep.
	// When maxBackups=5, the chain is audit.log (current) →
	// audit.log.1 (most recent) → ... → audit.log.5 (oldest).
	// When the chain is full, the oldest backup is deleted before
	// the new rotation. Zero keeps a single backup (path.1).
	maxBackups int
	// written tracks bytes written to the current file since the
	// last rotation (or since open). Maintained incrementally to
	// avoid a per-write stat syscall. Reset to 0 on rotate/reopen.
	written int64
}

// SetMetricsRegistry wires the registry that fileAuditLogger uses to
// count write failures. Called from [server.buildAuditLogger] after
// construction. Nil registry means no metrics — the stderr fallback
// still fires on failure, but /metrics won't surface the issue.
//
// The metric exposed is gonacos_audit_write_failures_total{reason}
// where reason is "open" (file couldn't be opened/reopened),
// "marshal" (JSON encoding failed — should never happen for an
// AuditEvent), or "write" (the underlying Write call failed —
// disk full, permission revoked, NFS mount dropped).
func (l *fileAuditLogger) SetMetricsRegistry(r *observability.Registry) {
	l.metrics = r
}

// recordFailure increments the write-failure counter for the given
// reason. Best-effort: a nil registry or a malformed reason string is
// silently dropped — metrics must not break the actual audit call.
func (l *fileAuditLogger) recordFailure(reason string) {
	if l.metrics == nil {
		return
	}
	l.metrics.Counter("gonacos_audit_write_failures_total",
		map[string]string{"reason": reason},
	).Inc()
}

// NewFileAuditLogger opens (or creates) the audit log file at path and
// returns an AuditLogger that writes JSON-lines to it. The parent
// directory is created with mode 0o700 if missing — the audit log
// contains user IPs, usernames, and security-relevant actions (login
// success/failure, user/role/permission mutations, backup/restore), all
// of which are PII or compliance-relevant. Restricting the directory to
// the gonacos process user is defense in depth: an attacker with shell
// access as another user on the host cannot traverse into the audit
// directory to read or tamper with the trail. MkdirAll only sets the mode
// on directories it creates; pre-existing directories keep their mode.
//
// Returns an error if the file cannot be opened; callers should fall back
// to a logger-based audit logger in that case.
func NewFileAuditLogger(path string) (AuditLogger, error) {
	return newFileAuditLogger(path, 0, 0)
}

// NewFileAuditLoggerWithRotation opens (or creates) the audit log file at
// path and returns an AuditLogger that automatically rotates when the
// file reaches maxBytes, keeping maxBackups rotated copies. Set maxBytes
// to 0 to disable size-based rotation (SIGHUP-only, like NewFileAuditLogger).
//
// Rotation naming: path (current) → path.1 (most recent backup) →
// path.2 → ... → path.<maxBackups> (oldest). When the chain is full and
// a new rotation fires, path.<maxBackups> is deleted before the shift.
// maxBackups <= 0 is treated as 1 (keep a single backup).
//
// The rotation is atomic from the caller's perspective: it runs under
// the same mutex as Log, so an in-flight write completes before the fd
// is swapped. Events written during rotation are not lost — they land
// in the new file after rotation completes.
func NewFileAuditLoggerWithRotation(path string, maxBytes int64, maxBackups int) (AuditLogger, error) {
	return newFileAuditLogger(path, maxBytes, maxBackups)
}

func newFileAuditLogger(path string, maxBytes int64, maxBackups int) (AuditLogger, error) {
	if path == "" {
		return nil, errAuditPathEmpty
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	// Initialize written from the current file size so a pre-existing
	// audit log doesn't skip rotation on the first write. Stat is cheap
	// (one syscall at open time, not per-write).
	var size int64
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	fl := &fileAuditLogger{
		path:       path,
		f:          f,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		written:    size,
	}
	// Pick up the package-level registry when set so fileAuditLogger
	// counts write failures without forcing every caller to wire
	// SetMetricsRegistry separately. The registry is set by
	// [SetAuditMetricsRegistry] from server.New before [NewFileAuditLogger]
	// is called via [buildAuditLogger]. Nil registry means no metrics —
	// the stderr fallback still fires on failure.
	if AuditMetricsRegistry != nil {
		fl.metrics = AuditMetricsRegistry
	}
	return fl, nil
}

// errAuditPathEmpty signals NewFileAuditLogger was called with an empty path.
var errAuditPathEmpty = auditError("audit log file path is empty")

type auditError string

func (e auditError) Error() string { return string(e) }

func (l *fileAuditLogger) Log(e AuditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		// Reopen on demand after a previous write failure.
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			l.recordFailure("open")
			fmt.Fprintf(os.Stderr, "audit: reopen %s failed: %v\n", l.path, err)
			return
		}
		l.f = f
		// Reset written from the reopened file's size — a previous
		// failure may have left the file with partial content.
		if info, err := f.Stat(); err == nil {
			l.written = info.Size()
		} else {
			l.written = 0
		}
	}
	line, err := json.Marshal(e)
	if err != nil {
		l.recordFailure("marshal")
		fmt.Fprintf(os.Stderr, "audit: marshal failed: %v\n", err)
		return
	}
	line = append(line, '\n')
	if _, err := l.f.Write(line); err != nil {
		l.recordFailure("write")
		fmt.Fprintf(os.Stderr, "audit: write %s failed: %v\n", l.path, err)
		_ = l.f.Close()
		l.f = nil
		return
	}
	l.written += int64(len(line))
	// Size-based rotation: when the current file reaches maxBytes,
	// rotate before the next event lands. This is the safety net for
	// deployments where logrotate(8) is not configured — without it,
	// a high-volume audit stream can fill the disk. Rotation runs
	// under the same mutex as the write, so no events are lost.
	if l.maxBytes > 0 && l.written >= l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			// Rotation failed — record the metric but don't drop
			// the event we just wrote. The file stays open and
			// events keep accumulating; the next write will retry
			// rotation when written >= maxBytes (which is still
			// true since we didn't reset written).
			l.recordFailure("rotate")
			fmt.Fprintf(os.Stderr, "audit: rotate %s failed: %v\n", l.path, err)
		}
	}
}

// rotateLocked performs the size-based file rotation. Caller MUST hold
// l.mu. The chain is: path (current) → path.1 → path.2 → ... →
// path.<maxBackups>. The oldest backup (path.<maxBackups>) is deleted,
// then each path.<N> is renamed to path.<N+1> (from high to low to
// avoid clobbering), and finally the current path is renamed to
// path.1. A fresh file is then opened at path and written is reset.
//
// Failures mid-rotation (e.g., a rename fails due to cross-device link)
// leave the chain in a partial state but the logger is not broken: the
// new file is opened at path and events continue. The error is
// returned so Log can record the metric.
func (l *fileAuditLogger) rotateLocked() error {
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	backups := l.maxBackups
	if backups < 1 {
		backups = 1
	}
	// Delete the oldest backup if it exists.
	oldest := fmt.Sprintf("%s.%d", l.path, backups)
	if _, err := os.Stat(oldest); err == nil {
		if err := os.Remove(oldest); err != nil {
			// Can't remove oldest — abort rotation to avoid
			// clobbering the chain. The file will be reopened
			// at path and events continue.
			return fmt.Errorf("remove oldest backup %s: %w", oldest, err)
		}
	}
	// Shift backups down: path.<N> → path.<N+1> for N from
	// backups-1 down to 1. We go high-to-low so we don't clobber
	// path.<N+1> before moving it to path.<N+2>.
	for n := backups - 1; n >= 1; n-- {
		src := fmt.Sprintf("%s.%d", l.path, n)
		dst := fmt.Sprintf("%s.%d", l.path, n+1)
		if _, err := os.Stat(src); err != nil {
			continue // backup doesn't exist yet
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %s → %s: %w", src, dst, err)
		}
	}
	// Rename current file to .1.
	if _, err := os.Stat(l.path); err == nil {
		dst := fmt.Sprintf("%s.1", l.path)
		if err := os.Rename(l.path, dst); err != nil {
			return fmt.Errorf("rename %s → %s: %w", l.path, dst, err)
		}
	}
	// Open fresh file at path.
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: reopen %s after rotate: %w", l.path, err)
	}
	l.f = f
	l.written = 0
	return nil
}

// Reopen closes the current file handle and opens a fresh one at the
// configured path. Used by SIGHUP-based log rotation: an operator renames
// the audit log (e.g., audit.log -> audit.log.1) and sends SIGHUP; gonacos
// calls Reopen so subsequent events land in the freshly-created audit.log
// rather than the renamed inode.
//
// Safe to call while Log is concurrent: takes the same mutex as Log so an
// in-flight write completes before the fd is swapped. After Reopen returns,
// the next Log call writes to the new file.
//
// A failure to open the new file leaves the logger with no open fd; the
// next Log call will attempt its own reopen-on-failure path. The error is
// returned to the caller (typically the SIGHUP handler) so it can log the
// failure — but the logger is not left in a permanently broken state.
func (l *fileAuditLogger) Reopen() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: reopen %s: %w", l.path, err)
	}
	l.f = f
	// Reset written from the reopened file's size — logrotate(8) with
	// copytruncate may have left the file with partial content.
	if info, err := f.Stat(); err == nil {
		l.written = info.Size()
	} else {
		l.written = 0
	}
	return nil
}

// multiAuditLogger fans events out to multiple loggers. Useful when audit
// events should go to both the application logger (for greppable access-log
// style) and a dedicated file (for compliance/forwarding).
type multiAuditLogger struct {
	loggers []AuditLogger
}

// NewMultiAuditLogger wraps multiple AuditLoggers so events fan out to all
// of them. Nil loggers are skipped.
func NewMultiAuditLogger(loggers ...AuditLogger) AuditLogger {
	var keep []AuditLogger
	for _, l := range loggers {
		if l == nil {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) == 0 {
		return noopAuditLogger{}
	}
	if len(keep) == 1 {
		return keep[0]
	}
	return multiAuditLogger{loggers: keep}
}

func (m multiAuditLogger) Log(e AuditEvent) {
	for _, l := range m.loggers {
		l.Log(e)
	}
}

// Reopen delegates to every wrapped logger that implements AuditLogReopener.
// Loggers that don't hold a file descriptor (e.g., loggerAuditLogger writing
// to stderr) are silently skipped — there's nothing to reopen. Returns the
// first error encountered; subsequent loggers still get a Reopen call so a
// single broken file doesn't block rotation of the others.
//
// Used by SIGHUP-based log rotation when the audit pipeline fans out to
// both a logger (for greppable access-log style) and a file (for compliance
// archival). Both files need to be rotated atomically.
func (m multiAuditLogger) Reopen() error {
	var firstErr error
	for _, l := range m.loggers {
		if r, ok := l.(AuditLogReopener); ok {
			if err := r.Reopen(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// auditFromRequest extracts the username (from auth claims) and client IP
// from a request, returning a partially-populated AuditEvent. The caller
// fills in Action, Resource, Result, and Detail.
func auditFromRequest(r *http.Request, action AuditAction) AuditEvent {
	claims := ClaimsFromContext(r.Context())
	return AuditEvent{
		Timestamp: time.Now(),
		Action:    action,
		User:      claims.Username,
		IP:        clientIP(r),
	}
}

// auditLog is a convenience that fills in the result and detail, then
// dispatches to the logger. No-op when logger is nil.
func auditLog(logger AuditLogger, r *http.Request, action AuditAction, resource, detail string, result AuditResult) {
	if logger == nil {
		return
	}
	event := auditFromRequest(r, action)
	event.Resource = resource
	event.Detail = detail
	event.Result = result
	logger.Log(event)
}

// withAuditUser returns a copy of the event with User set to the given
// username. Used by the login handler before the auth middleware has run
// (so claims are not in the context yet) — the caller knows the username
// from the form value.
func withAuditUser(r *http.Request, action AuditAction, username string) AuditEvent {
	event := auditFromRequest(r, action)
	event.User = username
	return event
}

// metricsAuditLogger wraps an AuditLogger and increments a counter on
// every event. The counter is the alerting signal for "audit event rate
// spiked" — a sudden burst of login_failed events is a brute-force
// attempt, a burst of failure results is a permission-scan, and a
// non-zero rate of any action confirms the audit pipeline is wired.
//
// Counter labels are deliberately low-cardinality: {action, result}.
// Username and IP are high-cardinality and belong in the log file, not
// in Prometheus — a per-user counter would blow up the series count
// and most values would have a single observation.
type metricsAuditLogger struct {
	next AuditLogger
	// registry is captured rather than a *Counter because the labelled
	// counter is looked up per-event with {action, result} — the
	// Registry deduplicates internally, so the per-event cost is a
	// map read (RLock) after the first event with a given label set.
	registry *observability.Registry
}

// AuditMetricsRegistry is the observability registry used by the
// metricsAuditLogger. Set once by [SetAuditMetricsRegistry] from
// server.New; the metricsAuditLogger is constructed afterwards via
// [WrapWithMetrics]. Nil registry means no metrics are recorded — the
// logger still works, just without the counter.
//
// Package-level rather than passed through every AuditLogger
// constructor because the loggers are constructed in multiple places
// (server.New, app.NewHandlerWithServicesAndRegistry, tests) and a
// package-level setter keeps the construction sites unchanged.
var AuditMetricsRegistry *observability.Registry

// SetAuditMetricsRegistry wires the registry that the metricsAuditLogger
// will use to count audit events. Called once from server.New after the
// registry is constructed. After this call, [WrapWithMetrics] returns a
// logger that increments gonacos_audit_events_total{action,result} per
// event. Safe to call with nil to disable metrics (e.g., in tests).
func SetAuditMetricsRegistry(r *observability.Registry) {
	AuditMetricsRegistry = r
}

// WrapWithMetrics wraps an AuditLogger so every event increments
// gonacos_audit_events_total{action,result}. When no registry is
// configured (SetAuditMetricsRegistry not called or called with nil),
// the original logger is returned unchanged — backward compatible with
// embedders that don't wire observability.
func WrapWithMetrics(logger AuditLogger) AuditLogger {
	if AuditMetricsRegistry == nil || logger == nil {
		return logger
	}
	return &metricsAuditLogger{
		next:     logger,
		registry: AuditMetricsRegistry,
	}
}

// Log increments gonacos_audit_events_total{action,result} and
// delegates to the wrapped logger. The labelled counter is looked up
// per-event; the Registry caches the *Counter for a given label set
// after the first observation, so the steady-state cost is an RLock'd
// map read plus an atomic increment — negligible against the file I/O
// the wrapped logger performs.
func (m *metricsAuditLogger) Log(e AuditEvent) {
	m.registry.Counter("gonacos_audit_events_total", map[string]string{
		"action": string(e.Action),
		"result": string(e.Result),
	}).Inc()
	m.next.Log(e)
}

// Reopen delegates to the wrapped logger when it implements
// [AuditLogReopener]. metricsAuditLogger holds no file descriptor of
// its own — it only counts events — so rotation must be handled by
// the underlying fileAuditLogger. Without this delegation,
// [Server.ReopenAuditLog]'s type assertion against the outermost
// AuditLogger (the metricsAuditLogger) would fail to find
// AuditLogReopener, and SIGHUP-based log rotation would silently
// no-op — the file descriptor would never be swapped and the
// renamed inode would keep receiving events while the new audit.log
// stayed empty.
func (m *metricsAuditLogger) Reopen() error {
	if r, ok := m.next.(AuditLogReopener); ok {
		return r.Reopen()
	}
	return nil
}
