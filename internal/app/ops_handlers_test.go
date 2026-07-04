package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/saker-ai/gonacos/internal/store"
)

func TestOpsMetricsEndpoint(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/metrics", nil)
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
	handler := NewHandler("../..")
	body := doJSON(t, handler, http.MethodGet, "/v3/admin/ops/info", nil, http.StatusOK)
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
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/backup", nil)
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
	handler := NewHandler("../..")

	backupReq := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/backup", nil)
	backupRec := httptest.NewRecorder()
	handler.ServeHTTP(backupRec, backupReq)
	if backupRec.Code != http.StatusOK {
		t.Fatalf("backup status = %d", backupRec.Code)
	}
	originalBackup := backupRec.Body.Bytes()

	createBody := "namespaceId=test-restore&namespaceName=Test+Restore&namespaceDesc=desc"
	createReq := httptest.NewRequest(http.MethodPost, "/v3/admin/core/namespace", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	listBody := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, http.StatusOK)
	data, _ := json.Marshal(listBody.Data)
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("namespace list = %d, want >=2", len(list))
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/v3/admin/ops/restore", bytes.NewReader(originalBackup))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreRec := httptest.NewRecorder()
	handler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body: %s", restoreRec.Code, restoreRec.Body.String())
	}

	listBody = doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, http.StatusOK)
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
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/ops/restore", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOpsPprofIndexReachable(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/pprof/", nil)
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

func serviceKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
