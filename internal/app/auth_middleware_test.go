package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authsvc "github.com/godeps/gonacos/internal/auth"
	"github.com/godeps/gonacos/internal/protocol"
)

// newAuthTestServices creates a fresh auth service with the default admin
// user (nacos/nacos) and a non-admin user (viewer/viewer) for RBAC tests.
func newAuthTestServices(t *testing.T) (*authsvc.Service, string, string) {
	t.Helper()
	svc := authsvc.NewService()
	if _, err := svc.BootstrapAdmin("nacos"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if _, err := svc.CreateUser("viewer", "viewer"); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	adminToken := loginToken(t, svc, "nacos", "nacos")
	viewerToken := loginToken(t, svc, "viewer", "viewer")
	return svc, adminToken, viewerToken
}

func loginToken(t *testing.T, svc *authsvc.Service, username, password string) string {
	t.Helper()
	result, err := svc.Login(username, password)
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return result.AccessToken
}

// echoHandler returns a 200 with the request path so tests can confirm the
// wrapped handler ran.
func echoHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteResult(w, http.StatusOK, r.URL.Path)
	}
}

func TestAuthMiddleware_OpenPathNoToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("open path without token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_OpenPathInvalidToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/auth/user/login", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+"garbage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("open path with invalid token should skip verification: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_StandardPathNoToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("standard path without token (permissive): want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_StandardPathValidToken(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("standard path with valid admin token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_StandardPathInvalidToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+"not-a-real-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("standard path with invalid token: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPathNoToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin path without token: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPathNonAdmin(t *testing.T) {
	svc, _, viewerToken := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/role", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+viewerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin path with non-admin token: want 403, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPathAdmin(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/auth/user/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin path with admin token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPathInvalidToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/permission", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+"bogus")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin path with invalid token: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_TokenFromQueryParameter(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	// SDK GET style: accessToken in query string, no Authorization header.
	req := httptest.NewRequest(http.MethodGet, "/v3/auth/user/list?accessToken="+url.QueryEscape(adminToken), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SDK GET-style token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_TokenFromFormParameter(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	form := url.Values{}
	form.Set("accessToken", adminToken)
	body := form.Encode()

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SDK POST-style token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NacosPrefixOpenPath(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/nacos/v3/console/health/liveness", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/nacos-prefixed open path: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NacosPrefixAdminPath(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/nacos/v3/auth/user/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/nacos-prefixed admin path with admin token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ClaimsInjectedIntoContext(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	var captured authsvc.Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = ClaimsFromContext(r.Context())
		protocol.WriteResult(w, http.StatusOK, "ok")
	})
	handler := newAuthMiddleware(svc, inner)

	req := httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !captured.GlobalAdmin {
		t.Fatalf("expected GlobalAdmin=true in injected claims, got %+v", captured)
	}
	if captured.Username != "nacos" {
		t.Fatalf("expected username nacos, got %q", captured.Username)
	}
}

func TestAuthMiddleware_UIPathBypassesAuth(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/v3/console/ui/index.html", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("UI path without token: want 200, got %d", rec.Code)
	}
}

// TestAuthMiddleware_IntegrationWithMux verifies the wired NewHandlerWithServices
// enforces auth on admin routes while leaving SDK routes permissive.
func TestAuthMiddleware_IntegrationWithMux(t *testing.T) {
	svc, adminToken, viewerToken := newAuthTestServices(t)
	services := newServicesWithAuth(svc)
	handler := NewHandlerWithServices("../..", services)

	// Admin route without token → 401.
	req := httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader("role=dev&username=viewer"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin route without token: want 401, got %d", rec.Code)
	}

	// Admin route with viewer token → 403.
	req = httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader("role=dev&username=viewer"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+viewerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin route with non-admin token: want 403, got %d", rec.Code)
	}

	// Admin route with admin token → 200.
	req = httptest.NewRequest(http.MethodPost, "/v3/auth/role", strings.NewReader("role=dev&username=viewer"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+adminToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin route with admin token: want 200, got %d", rec.Code)
	}

	// Login route without token → 200 (open path).
	req = httptest.NewRequest(http.MethodPost, "/v3/auth/user/login", strings.NewReader("username=nacos&password=nacos"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login route without token: want 200, got %d", rec.Code)
	}

	// Health route without token → 200 (open path).
	req = httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health route without token: want 200, got %d", rec.Code)
	}
}

// newServicesWithAuth returns a serviceBundle whose auth service is the
// provided one, so tests can share a single auth instance with the mux.
func newServicesWithAuth(auth *authsvc.Service) *serviceBundle {
	bundle := newServices()
	bundle.auth = auth
	return bundle
}
