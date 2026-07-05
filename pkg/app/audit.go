package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
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
	return &fileAuditLogger{path: path, f: f}, nil
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
			fmt.Fprintf(os.Stderr, "audit: reopen %s failed: %v\n", l.path, err)
			return
		}
		l.f = f
	}
	line, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: marshal failed: %v\n", err)
		return
	}
	line = append(line, '\n')
	if _, err := l.f.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "audit: write %s failed: %v\n", l.path, err)
		_ = l.f.Close()
		l.f = nil
	}
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
