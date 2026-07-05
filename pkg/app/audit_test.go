package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	authsvc "github.com/godeps/gonacos/pkg/auth"
)

// recordingAuditLogger captures audit events for test assertions. Safe for
// concurrent use — the same handler instance serves parallel requests.
type recordingAuditLogger struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (r *recordingAuditLogger) Log(e AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingAuditLogger) Events() []AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]AuditEvent, len(r.events))
	copy(cp, r.events)
	return cp
}

// TestAuditLoginSuccess verifies that a successful login emits an audit
// event with action=login, result=success, and the form-supplied username.
// The login handler runs before the auth middleware populates claims, so
// the username must come from the form value.
func TestAuditLoginSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "username=nacos&password=nacos"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionLogin {
		t.Errorf("action = %v, want login", e.Action)
	}
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos", e.User)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
}

// TestAuditLoginFailure verifies that a failed login emits an audit event
// with action=login_failed, result=failure, and the error in detail.
func TestAuditLoginFailure(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "username=nacos&password=wrong"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 for wrong password")
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionLoginFailed {
		t.Errorf("action = %v, want login_failed", e.Action)
	}
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos (form value preserved on failure)", e.User)
	}
	if e.Result != AuditResultFailure {
		t.Errorf("result = %v, want failure", e.Result)
	}
	if e.Detail == "" {
		t.Error("detail is empty, want the error message")
	}
}

// TestAuditUserCreate verifies that creating a user emits an audit event
// with action=user_create, result=success, and the new username as
// resource.
func TestAuditUserCreate(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "username=newuser&password=newpass"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionUserCreate {
		t.Errorf("action = %v, want user_create", e.Action)
	}
	if e.Resource != "newuser" {
		t.Errorf("resource = %v, want newuser", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
	// The actor should be the admin user from the token.
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos (from token claims)", e.User)
	}
}

// TestAuditBackupSuccess verifies that a backup emits an audit event with
// action=backup, result=success. Backup is admin-only, so the request
// carries an admin token.
func TestAuditBackupSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	req := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/backup", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionBackup {
		t.Errorf("action = %v, want backup", e.Action)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
}

// TestAuditRestoreFailure verifies that a restore with a bad payload emits
// an audit event with action=restore, result=failure, and the error in
// detail.
func TestAuditRestoreFailure(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ops/restore", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionRestore {
		t.Errorf("action = %v, want restore", e.Action)
	}
	if e.Result != AuditResultFailure {
		t.Errorf("result = %v, want failure", e.Result)
	}
	if !strings.Contains(e.Detail, "invalid") {
		t.Errorf("detail = %v, want 'invalid' substring", e.Detail)
	}
}

// TestAuditNilLoggerIsNoop verifies that a nil AuditLogger does not panic
// and does not emit any events. This guards the production path where
// auditing is disabled.
func TestAuditNilLoggerIsNoop(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// bundle.AuditLogger is nil by default
	handler := NewHandlerWithServices("../..", bundle)

	body := "username=nacos&password=nacos"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// loginAdmin is a test helper that logs in the admin user and returns the
// access token.
func loginAdmin(t *testing.T, svc *authsvc.Service) (string, authsvc.LoginResult) {
	t.Helper()
	result, err := svc.Login("nacos", "nacos")
	if err != nil {
		t.Fatalf("login admin: %v", err)
	}
	return result.AccessToken, result
}
