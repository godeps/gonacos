package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/godeps/gonacos/pkg/auth"
)

// TestAuditRoleCreateSuccess verifies that creating a role emits an audit
// event with action=role_create, result=success, and the role name as
// resource. The detail carries the linked user (user=<username>).
func TestAuditRoleCreateSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := bundle.Auth.CreateUser("linked-user", "pass"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "role=ops&username=linked-user"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader(body))
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
	if e.Action != AuditActionRoleCreate {
		t.Errorf("action = %v, want role_create", e.Action)
	}
	if e.Resource != "ops" {
		t.Errorf("resource = %v, want ops", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
	if !strings.Contains(e.Detail, "linked-user") {
		t.Errorf("detail = %v, want 'user=linked-user' substring", e.Detail)
	}
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos (from token claims)", e.User)
	}
}

// TestAuditRoleCreateFailure verifies that creating a role with a missing
// role name emits a failure audit event.
func TestAuditRoleCreateFailure(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "role=&username=anyone"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 for empty role")
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionRoleCreate {
		t.Errorf("action = %v, want role_create", e.Action)
	}
	if e.Result != AuditResultFailure {
		t.Errorf("result = %v, want failure", e.Result)
	}
	if e.Detail == "" {
		t.Error("detail is empty, want the error message")
	}
}

// TestAuditRoleDelete verifies that deleting a role emits an audit event
// with action=role_delete.
func TestAuditRoleDelete(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := bundle.Auth.CreateUser("linked-user", "pass"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := bundle.Auth.CreateRole("ops", "linked-user"); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "role=ops&username=linked-user"
	req := httptest.NewRequest(http.MethodDelete, "/v3/auth/role", strings.NewReader(body))
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
	if e.Action != AuditActionRoleDelete {
		t.Errorf("action = %v, want role_delete", e.Action)
	}
	if e.Resource != "ops" {
		t.Errorf("resource = %v, want ops", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
}

// TestAuditPermissionCreateSuccess verifies that creating a permission
// emits an audit event with action=permission_create and the role as
// resource. The detail carries resource and action.
func TestAuditPermissionCreateSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := bundle.Auth.CreateUser("linked-user", "pass"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := bundle.Auth.CreateRole("ops", "linked-user"); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "role=ops&resource=config:*&action=r"
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/permission", strings.NewReader(body))
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
	if e.Action != AuditActionPermissionCreate {
		t.Errorf("action = %v, want permission_create", e.Action)
	}
	if e.Resource != "ops" {
		t.Errorf("resource = %v, want ops", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
	if !strings.Contains(e.Detail, "config:*") || !strings.Contains(e.Detail, "action=r") {
		t.Errorf("detail = %v, want resource and action substring", e.Detail)
	}
}

// TestAuditPermissionDelete verifies that deleting a permission emits an
// audit event with action=permission_delete.
func TestAuditPermissionDelete(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := bundle.Auth.CreateUser("linked-user", "pass"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := bundle.Auth.CreateRole("ops", "linked-user"); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if err := bundle.Auth.CreatePermission("ops", "config:*", "r"); err != nil {
		t.Fatalf("seed permission: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "role=ops&resource=config:*&action=r"
	req := httptest.NewRequest(http.MethodDelete, "/v3/auth/permission", strings.NewReader(body))
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
	if e.Action != AuditActionPermissionDelete {
		t.Errorf("action = %v, want permission_delete", e.Action)
	}
	if e.Resource != "ops" {
		t.Errorf("resource = %v, want ops", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
}
