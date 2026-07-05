package app

import (
	"net/http"
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
