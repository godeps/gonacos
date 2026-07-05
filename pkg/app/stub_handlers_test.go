package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStubHandlers_DerbyOps(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doGet(t, handler, "/v3/admin/cs/ops/derby", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "applicable") {
		t.Fatalf("derby ops body: %s", rec.Body.String())
	}
}

func TestStubHandlers_ImportDerby(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doPostForm(t, handler, "/v3/admin/cs/ops/derby/import", url.Values{}, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "applicable") {
		t.Fatalf("import derby body: %s", rec.Body.String())
	}
}

func TestStubHandlers_NamingMetrics(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doGet(t, handler, "/v3/admin/ns/ops/metrics", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "serviceCount") {
		t.Fatalf("metrics body: %s", rec.Body.String())
	}
}

func TestStubHandlers_GetSwitches(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doGet(t, handler, "/v3/admin/ns/ops/switches", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "distroEnabled") {
		t.Fatalf("switches body: %s", rec.Body.String())
	}
}

func TestStubHandlers_UpdateSwitches(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doPutForm(t, handler, "/v3/admin/ns/ops/switches", url.Values{
		"entry": {"autoDeregisterWhenInstanceDown"},
		"value": {"false"},
	}, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "accepted") {
		t.Fatalf("update switches body: %s", rec.Body.String())
	}
}

func TestStubHandlers_ClientDistro(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doGet(t, handler, "/v3/admin/ns/client/distro?clientId=client-1", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "responsible") {
		t.Fatalf("client distro body: %s", rec.Body.String())
	}
}

func TestStubHandlers_ConfigSearchByContent(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// No configs exist yet; search returns an empty page (not an error).
	rec := doGet(t, handler, "/v3/console/cs/config/searchDetail?namespaceId=public&content=test", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "totalCount") {
		t.Fatalf("searchDetail body: %s", rec.Body.String())
	}
}

func TestStubHandlers_SearchByContentMissingContent(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing content parameter → error response.
	doGet(t, handler, "/v3/console/cs/config/searchDetail?namespaceId=public", http.StatusBadRequest)
}

func TestStubHandlers_PublishConfigMetadataMissingFields(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing required fields → error.
	doPutForm(t, handler, "/v3/admin/cs/config/metadata", url.Values{}, http.StatusBadRequest)
}

func TestStubHandlers_PluginConfigMissingId(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing pluginId → error.
	doPutForm(t, handler, "/v3/console/plugin/config", url.Values{}, http.StatusBadRequest)
}

func TestStubHandlers_PluginStatusMissingId(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing pluginId → error. Use PUT.
	req := httptest.NewRequest(http.MethodPut, "/v3/console/plugin/status", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("plugin status without id: want 400, got %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestStubHandlers_ClientListEmpty(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	rec := doGet(t, handler, "/v3/admin/ns/client/list", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "clients") {
		t.Fatalf("client list body: %s", rec.Body.String())
	}
}

func TestStubHandlers_ClientDetailMissingId(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing clientId → error.
	doGet(t, handler, "/v3/admin/ns/client", http.StatusBadRequest)
}

func TestStubHandlers_AgentSpecVersionMetaMissingId(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t)

	// Missing id → error.
	doGet(t, handler, "/v3/admin/ai/agentspecs/version/meta", http.StatusBadRequest)
}

// doGet sends a GET request and checks the status. Returns the recorder.
func doGet(t *testing.T, handler http.Handler, path string, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("GET %s: want %d, got %d, body %s", path, wantStatus, rec.Code, rec.Body.String())
	}
	return rec
}

// doPostForm sends a POST request with form data and checks the status.
func doPostForm(t *testing.T, handler http.Handler, path string, form url.Values, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("POST %s: want %d, got %d, body %s", path, wantStatus, rec.Code, rec.Body.String())
	}
	return rec
}

// doPutForm sends a PUT request with form data and checks the status.
func doPutForm(t *testing.T, handler http.Handler, path string, form url.Values, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("PUT %s: want %d, got %d, body %s", path, wantStatus, rec.Code, rec.Body.String())
	}
	return rec
}
