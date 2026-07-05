package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authsvc "github.com/godeps/gonacos/pkg/auth"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	req := httptest.NewRequest(http.MethodGet, "/v3/console/health/liveness", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("open path without token: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_OpenPathInvalidToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	// Console routes (not under /v3/admin/) remain permissive — a missing
	// token is allowed for SDK compatibility. Admin routes (/v3/admin/*)
	// require an admin token; see TestAuthMiddleware_AdminPathNoToken.
	req := httptest.NewRequest(http.MethodGet, "/v3/console/cs/config/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("standard path without token (permissive): want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPrefixNoToken(t *testing.T) {
	svc, _, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	// Admin routes under /v3/admin/ require a valid admin token — anonymous
	// access is rejected. This covers namespace, config, AI, cluster, plugin,
	// loader, and ops routes that fall under the /v3/admin/ prefix.
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin prefix path without token: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPrefixNonAdminToken(t *testing.T) {
	svc, _, nonAdminToken := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	// A non-admin token is rejected for admin prefix routes.
	req := httptest.NewRequest(http.MethodGet, "/v3/admin/core/namespace/list", nil)
	req.Header.Set(authsvc.AuthorizationHeader, authsvc.TokenPrefix+nonAdminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin prefix path with non-admin token: want 403, got %d", rec.Code)
	}
}

func TestAuthMiddleware_StandardPathValidToken(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	req := httptest.NewRequest(http.MethodPost, "/v3/auth/user", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin path without token: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AdminPathNonAdmin(t *testing.T) {
	svc, _, viewerToken := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

	req := httptest.NewRequest(http.MethodGet, "/nacos/v3/console/health/liveness", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/nacos-prefixed open path: want 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NacosPrefixAdminPath(t *testing.T) {
	svc, adminToken, _ := newAuthTestServices(t)
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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
	handler := newAuthMiddleware(svc, inner, nil)

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
	handler := newAuthMiddleware(svc, echoHandler(), nil)

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

// newServicesWithAuth returns a ServiceBundle whose auth service is the
// provided one, so tests can share a single auth instance with the mux.
func newServicesWithAuth(auth *authsvc.Service) *ServiceBundle {
	bundle := NewServiceBundle()
	bundle.Auth = auth
	return bundle
}

// TestAuthMiddlewareTokenValidationMetrics verifies that the auth middleware
// increments gonacos_token_validations_total{result="valid"} for a valid
// token and {result="invalid"} for an invalid or missing token on a
// protected route. These counters are the security monitoring signal for
// token-guessing attacks and expired-token storms — a spike in
// result="invalid" indicates either a misconfigured client or an attacker
// probing tokens.
func TestAuthMiddlewareTokenValidationMetrics(t *testing.T) {
	svc, _, viewerToken := newAuthTestServices(t)
	registry := observability.NewRegistry()
	inner := echoHandler()
	handler := newAuthMiddleware(svc, inner, registry)

	// Valid (non-admin) token on a protected, non-admin route.
	req := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	req.Header.Set("Authorization", "Bearer "+viewerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: got %d, want 200", rec.Code)
	}

	// Invalid token on the same route.
	req2 := httptest.NewRequest(http.MethodGet, "/v3/cs/configs", nil)
	req2.Header.Set("Authorization", "Bearer not-a-real-token")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	// Missing token on an admin route (adminOnlyExactPaths).
	req3 := httptest.NewRequest(http.MethodGet, "/v3/auth/user/list", nil)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	validCount := registry.Counter("gonacos_token_validations_total", map[string]string{"result": "valid"}).Value()
	invalidCount := registry.Counter("gonacos_token_validations_total", map[string]string{"result": "invalid"}).Value()
	if validCount != 1 {
		t.Fatalf("valid counter = %d, want 1", validCount)
	}
	if invalidCount != 2 {
		t.Fatalf("invalid counter = %d, want 2 (one bad token, one missing on admin route)", invalidCount)
	}
}
