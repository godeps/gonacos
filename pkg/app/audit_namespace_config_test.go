package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/godeps/gonacos/pkg/auth"
	configsvc "github.com/godeps/gonacos/pkg/config"
)

// TestAuditNamespaceCreateSuccess verifies that creating a namespace emits
// an audit event with action=namespace_create, result=success, and the
// namespace ID as resource.
func TestAuditNamespaceCreateSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "customNamespaceId=audit-ns&namespaceName=Audit&namespaceDesc=test"
	req := httptest.NewRequest(http.MethodPost, "/v3/console/core/namespace", strings.NewReader(body))
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
	if e.Action != AuditActionNamespaceCreate {
		t.Errorf("action = %v, want namespace_create", e.Action)
	}
	if e.Resource != "audit-ns" {
		t.Errorf("resource = %v, want audit-ns", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos (from token claims)", e.User)
	}
}

// TestAuditNamespaceCreateFailure verifies that creating a namespace with a
// missing ID emits a failure audit event.
func TestAuditNamespaceCreateFailure(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	// Empty customNamespaceId on console route → service rejects.
	body := "customNamespaceId=&namespaceName=Audit&namespaceDesc=test"
	req := httptest.NewRequest(http.MethodPost, "/v3/console/core/namespace", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 for empty namespace ID")
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionNamespaceCreate {
		t.Errorf("action = %v, want namespace_create", e.Action)
	}
	if e.Result != AuditResultFailure {
		t.Errorf("result = %v, want failure", e.Result)
	}
	if e.Detail == "" {
		t.Error("detail is empty, want the error message")
	}
}

// TestAuditNamespaceUpdateAndDelete verifies that update and delete emit
// audit events with the correct actions and resource IDs.
func TestAuditNamespaceUpdateAndDelete(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	if err := bundle.Namespace.Create("audit-ns2", "Audit2", "test"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	// Update
	body := "namespaceId=audit-ns2&namespaceName=Updated&namespaceDesc=updated"
	req := httptest.NewRequest(http.MethodPut, "/v3/console/core/namespace", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", rec.Code)
	}

	// Delete
	body = "namespaceId=audit-ns2"
	req = httptest.NewRequest(http.MethodDelete, "/v3/console/core/namespace", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}

	events := audit.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if events[0].Action != AuditActionNamespaceUpdate {
		t.Errorf("event 0 action = %v, want namespace_update", events[0].Action)
	}
	if events[0].Resource != "audit-ns2" {
		t.Errorf("event 0 resource = %v, want audit-ns2", events[0].Resource)
	}
	if events[0].Result != AuditResultSuccess {
		t.Errorf("event 0 result = %v, want success", events[0].Result)
	}
	if events[1].Action != AuditActionNamespaceDelete {
		t.Errorf("event 1 action = %v, want namespace_delete", events[1].Action)
	}
	if events[1].Resource != "audit-ns2" {
		t.Errorf("event 1 resource = %v, want audit-ns2", events[1].Resource)
	}
	if events[1].Result != AuditResultSuccess {
		t.Errorf("event 1 result = %v, want success", events[1].Result)
	}
}

// TestAuditConfigPublishSuccess verifies that publishing a config emits an
// audit event with action=config_publish, result=success, and the
// namespace/group/dataId as resource.
func TestAuditConfigPublishSuccess(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "namespaceId=public&groupName=DEFAULT_GROUP&dataId=audit.yaml&content=hello&type=yaml"
	req := httptest.NewRequest(http.MethodPost, "/v3/console/cs/config", strings.NewReader(body))
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
	if e.Action != AuditActionConfigPublish {
		t.Errorf("action = %v, want config_publish", e.Action)
	}
	if e.Resource != "public/DEFAULT_GROUP/audit.yaml" {
		t.Errorf("resource = %v, want public/DEFAULT_GROUP/audit.yaml", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
	if e.User != "nacos" {
		t.Errorf("user = %v, want nacos (from token claims)", e.User)
	}
}

// TestAuditConfigPublishFailure verifies that publishing a config with a
// missing data ID emits a failure audit event.
func TestAuditConfigPublishFailure(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	// Missing dataId → service rejects.
	body := "namespaceId=public&groupName=DEFAULT_GROUP&dataId=&content=hello&type=yaml"
	req := httptest.NewRequest(http.MethodPost, "/v3/console/cs/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 for missing dataId")
	}
	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != AuditActionConfigPublish {
		t.Errorf("action = %v, want config_publish", e.Action)
	}
	if e.Result != AuditResultFailure {
		t.Errorf("result = %v, want failure", e.Result)
	}
}

// TestAuditConfigDelete verifies that deleting a config emits an audit event
// with action=config_delete and the namespace/group/dataId as resource.
func TestAuditConfigDelete(t *testing.T) {
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminToken, _ := loginAdmin(t, bundle.Auth)
	if err := bundle.Config.Publish(configsvc.PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "audit-delete.yaml",
		Content:     "hello",
		Type:        "yaml",
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	audit := &recordingAuditLogger{}
	bundle.AuditLogger = audit
	handler := NewHandlerWithServices("../..", bundle)

	body := "namespaceId=public&groupName=DEFAULT_GROUP&dataId=audit-delete.yaml"
	req := httptest.NewRequest(http.MethodDelete, "/v3/console/cs/config", strings.NewReader(body))
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
	if e.Action != AuditActionConfigDelete {
		t.Errorf("action = %v, want config_delete", e.Action)
	}
	if e.Resource != "public/DEFAULT_GROUP/audit-delete.yaml" {
		t.Errorf("resource = %v, want public/DEFAULT_GROUP/audit-delete.yaml", e.Resource)
	}
	if e.Result != AuditResultSuccess {
		t.Errorf("result = %v, want success", e.Result)
	}
}
