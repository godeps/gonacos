package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestConsoleSPAFlowEndToEnd(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	// 1. Serve the console HTML.
	req := httptest.NewRequest(http.MethodGet, "/v3/console/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("console status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "GoNacos Console") {
		t.Fatal("console HTML missing title")
	}

	// 2. Bootstrap admin (first-start flow).
	bootForm := url.Values{"password": {"nacos"}}
	bootReq := httptest.NewRequest(http.MethodPost, "/v3/auth/user/admin", strings.NewReader(bootForm.Encode()))
	bootReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bootRec := httptest.NewRecorder()
	handler.ServeHTTP(bootRec, bootReq)
	if bootRec.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, body: %s", bootRec.Code, bootRec.Body.String())
	}

	// 3. Login.
	loginForm := url.Values{"username": {"nacos"}, "password": {"nacos"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginBody struct {
		Data struct {
			AccessToken string `json:"accessToken"`
			Username    string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	if loginBody.Data.AccessToken == "" {
		t.Fatal("missing access token")
	}
	token := loginBody.Data.AccessToken

	// 4. List namespaces with the token.
	listReq := httptest.NewRequest(http.MethodGet, "/v3/admin/core/namespace/list", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list namespaces status = %d", listRec.Code)
	}

	// 5. Create a namespace via the console.
	createForm := url.Values{
		"namespaceId":   {"spa-test"},
		"namespaceName": {"SPA Test"},
		"namespaceDesc": {"created via console flow"},
	}
	createReq := httptest.NewRequest(http.MethodPost, "/v3/admin/core/namespace", strings.NewReader(createForm.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create namespace status = %d, body: %s", createRec.Code, createRec.Body.String())
	}

	// 6. Backup state.
	backupReq := httptest.NewRequest(http.MethodGet, "/v3/admin/ops/backup", nil)
	backupReq.Header.Set("Authorization", "Bearer "+token)
	backupRec := httptest.NewRecorder()
	handler.ServeHTTP(backupRec, backupReq)
	if backupRec.Code != http.StatusOK {
		t.Fatalf("backup status = %d", backupRec.Code)
	}
	backupBytes := backupRec.Body.Bytes()

	// 7. Restore (should remove the spa-test namespace since the backup was
	//    taken before... actually we took it after. Let's delete then restore.)
	deleteReq := httptest.NewRequest(http.MethodDelete, "/v3/admin/core/namespace?namespaceId=spa-test", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body: %s", deleteRec.Code, deleteRec.Body.String())
	}

	// 8. Restore the backup (brings spa-test back).
	restoreReq := httptest.NewRequest(http.MethodPost, "/v3/admin/ops/restore", bytes.NewReader(backupBytes))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreReq.Header.Set("Authorization", "Bearer "+token)
	restoreRec := httptest.NewRecorder()
	handler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body: %s", restoreRec.Code, restoreRec.Body.String())
	}

	// 9. Verify spa-test namespace is back.
	listReq2 := httptest.NewRequest(http.MethodGet, "/v3/admin/core/namespace/list", nil)
	listReq2.Header.Set("Authorization", "Bearer "+token)
	listRec2 := httptest.NewRecorder()
	handler.ServeHTTP(listRec2, listReq2)
	var listBody struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listRec2.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	found := false
	for _, ns := range listBody.Data {
		if id, _ := ns["namespace"].(string); id == "spa-test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("spa-test namespace not found after restore; list = %v", listBody.Data)
	}
}
