package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/godeps/gonacos/pkg/auth"
	"github.com/godeps/gonacos/pkg/store"
)

// newOpsTestHandler builds a handler with a fresh service bundle and returns
// it alongside an admin access token. The admin token is needed because the
// ops endpoints (backup, restore, info, metrics, pprof) are admin-only — a
// missing or non-admin token is rejected with 401/403.
func newOpsTestHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	result, err := bundle.Auth.Login("nacos", "nacos")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return NewHandlerWithServices("../..", bundle), result.AccessToken
}

// authReq wraps httptest.NewRequest with the admin Bearer token. Used by
// ops endpoint tests that need to pass the auth middleware.
func authReq(method, target string, body io.Reader, token string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+token)
	return req
}

func TestOpsMetricsEndpoint(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)
	req := authReq(http.MethodGet, "/v3/admin/ops/metrics", nil, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %v", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "process_goroutines") {
		t.Fatalf("missing process_goroutines: %s", body)
	}
	if !strings.Contains(body, "# TYPE") {
		t.Fatalf("missing TYPE annotation: %s", body)
	}
}

func TestOpsInfoEndpoint(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)
	headers := map[string]string{authsvc.AuthorizationHeader: authsvc.TokenPrefix + token}
	body := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/admin/ops/info", nil, headers, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info["version"] != Version {
		t.Fatalf("version = %v", info["version"])
	}
	if info["goroutines"] == nil {
		t.Fatal("missing goroutines")
	}
}

func TestOpsBackupReturnsEnvelope(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)
	req := authReq(http.MethodGet, "/v3/admin/ops/backup", nil, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("content-disposition = %v", cd)
	}
	var env store.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nbody: %s", err, rec.Body.String())
	}
	if env.Version != store.EnvelopeVersion {
		t.Fatalf("version = %v", env.Version)
	}
	required := []string{"namespace", "config", "naming", "auth", "ai", "cluster"}
	for _, k := range required {
		if _, ok := env.Services[k]; !ok {
			t.Fatalf("missing service %q in envelope; have %v", k, serviceKeys(env.Services))
		}
	}
}

func TestOpsRestoreReplaysState(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)

	backupReq := authReq(http.MethodGet, "/v3/admin/ops/backup", nil, token)
	backupRec := httptest.NewRecorder()
	handler.ServeHTTP(backupRec, backupReq)
	if backupRec.Code != http.StatusOK {
		t.Fatalf("backup status = %d", backupRec.Code)
	}
	originalBackup := backupRec.Body.Bytes()

	createBody := "namespaceId=test-restore&namespaceName=Test+Restore&namespaceDesc=desc"
	createReq := authReq(http.MethodPost, "/v3/admin/core/namespace", strings.NewReader(createBody), token)
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	listBody := doJSONWithHeaders(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, map[string]string{authsvc.AuthorizationHeader: authsvc.TokenPrefix + token}, http.StatusOK)
	data, _ := json.Marshal(listBody.Data)
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("namespace list = %d, want >=2", len(list))
	}

	restoreReq := authReq(http.MethodPost, "/v3/admin/ops/restore", bytes.NewReader(originalBackup), token)
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreRec := httptest.NewRecorder()
	handler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body: %s", restoreRec.Code, restoreRec.Body.String())
	}

	listBody = doJSONWithHeaders(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, map[string]string{authsvc.AuthorizationHeader: authsvc.TokenPrefix + token}, http.StatusOK)
	data, _ = json.Marshal(listBody.Data)
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal list after restore: %v", err)
	}
	for _, ns := range list {
		if id, _ := ns["namespace"].(string); id == "test-restore" {
			t.Fatalf("test-restore namespace should not exist after restore; list = %v", list)
		}
	}
}

func TestOpsRestoreRejectsBadPayload(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)
	req := authReq(http.MethodPost, "/v3/admin/ops/restore", strings.NewReader("not json"), token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOpsPprofIndexReachable(t *testing.T) {
	t.Parallel()
	handler, token := newOpsTestHandler(t)
	req := authReq(http.MethodGet, "/v3/admin/ops/pprof/", nil, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "heap") {
		t.Fatalf("pprof index missing heap: %s", string(body))
	}
}

// TestOpsEndpointsRejectMissingToken verifies that the admin-only ops
// endpoints (backup, restore, info, metrics, pprof) reject a request with no
// Authorization header. Before the admin-only-prefix fix, these endpoints
// were permissive — a missing token was allowed through, leaking process
// state via pprof and the full snapshot via backup.
func TestOpsEndpointsRejectMissingToken(t *testing.T) {
	t.Parallel()
	handler, _ := newOpsTestHandler(t)
	for _, path := range []string{
		"/v3/admin/ops/backup",
		"/v3/admin/ops/restore",
		"/v3/admin/ops/info",
		"/v3/admin/ops/metrics",
		"/v3/admin/ops/pprof/",
		"/v3/admin/ops/pprof/heap",
		"/v3/admin/ops/pprof/goroutine",
	} {
		t.Run(path, func(t *testing.T) {
			method := http.MethodGet
			if path == "/v3/admin/ops/restore" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: status = %d, want 401", path, rec.Code)
			}
		})
	}
}

// TestOpsEndpointsRejectNonAdminToken verifies that the admin-only ops
// endpoints reject a valid non-admin token. A logged-in non-admin user must
// not be able to dump heap or restore state.
func TestOpsEndpointsRejectNonAdminToken(t *testing.T) {
	t.Parallel()
	bundle := NewServiceBundle()
	if _, err := bundle.Auth.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if _, err := bundle.Auth.CreateUser("viewer", "viewer"); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerResult, err := bundle.Auth.Login("viewer", "viewer")
	if err != nil {
		t.Fatalf("login viewer: %v", err)
	}
	viewerToken := viewerResult.AccessToken
	handler := NewHandlerWithServices("../..", bundle)
	for _, path := range []string{
		"/v3/admin/ops/backup",
		"/v3/admin/ops/info",
		"/v3/admin/ops/pprof/",
		"/v3/admin/ops/pprof/heap",
	} {
		t.Run(path, func(t *testing.T) {
			req := authReq(http.MethodGet, path, nil, viewerToken)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with non-admin token: status = %d, want 403", path, rec.Code)
			}
		})
	}
}

func serviceKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
