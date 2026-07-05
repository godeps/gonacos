package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	authsvc "github.com/godeps/gonacos/pkg/auth"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
)

type claimsKey struct{}

// ClaimsFromContext returns the verified claims injected by authMiddleware,
// or a zero Claims value when no token was presented.
func ClaimsFromContext(ctx context.Context) authsvc.Claims {
	if v, ok := ctx.Value(claimsKey{}).(authsvc.Claims); ok {
		return v
	}
	return authsvc.Claims{}
}

// authMiddleware verifies access tokens and enforces admin-only routes.
//
// Design (permissive for SDK compatibility):
//   - Open paths (health, login, bootstrap, UI) skip verification entirely.
//   - Admin-only paths (user/role/permission CRUD, ops backup/restore, pprof)
//     require a valid globalAdmin token — a missing or non-admin token is
//     rejected. pprof endpoints are matched by prefix because they expose
//     live process state (heap, goroutines, CPU profile) that an
//     unauthenticated caller could misuse for reconnaissance or secret
//     extraction.
//   - All other paths: a missing token is allowed (anonymous), a presented
//     token is verified and rejected if invalid or expired.
//
// The SDK sends accessToken as a query parameter (GET) or form parameter
// (POST), not as an Authorization header, so the middleware checks all three
// sources in order: Authorization header → accessToken query → accessToken
// form.
type authMiddleware struct {
	auth     *authsvc.Service
	next     http.Handler
	tokenOK  *observability.Counter
	tokenBad *observability.Counter
	// rejectionMissingToken counts admin-only requests that arrived
	// with no token at all — a missing-token on an open path is fine,
	// but a missing token on /v3/admin/* is a misconfigured client or
	// an unauthenticated probe. Distinct from rejectionInvalidToken
	// (token presented but failed verification) and rejectionAccessDenied
	// (valid token but insufficient permissions) so operators can
	// distinguish the three attack patterns in /metrics.
	rejectionMissingToken *observability.Counter
	rejectionInvalidToken *observability.Counter
	rejectionAccessDenied *observability.Counter
}

func newAuthMiddleware(auth *authsvc.Service, next http.Handler, registry *observability.Registry) http.Handler {
	mw := &authMiddleware{auth: auth, next: next}
	if registry != nil {
		mw.tokenOK = registry.Counter("gonacos_token_validations_total", map[string]string{"result": "valid"})
		mw.tokenBad = registry.Counter("gonacos_token_validations_total", map[string]string{"result": "invalid"})
		mw.rejectionMissingToken = registry.Counter("gonacos_auth_rejections_total", map[string]string{"reason": "missing_token"})
		mw.rejectionInvalidToken = registry.Counter("gonacos_auth_rejections_total", map[string]string{"reason": "invalid_token"})
		mw.rejectionAccessDenied = registry.Counter("gonacos_auth_rejections_total", map[string]string{"reason": "access_denied"})
	}
	return mw
}

// openPaths bypass auth entirely. Login and bootstrap must work without a
// token; health/state/UI are public.
var authMiddlewareOpenPaths = map[string]struct{}{
	"/v3/console/health/liveness":     {},
	"/v3/console/health/readiness":    {},
	"/v3/admin/core/state/liveness":   {},
	"/v3/admin/core/state/readiness":  {},
	"/v3/admin/core/state":            {},
	"/v3/console/server/state":        {},
	"/v3/console/server/announcement": {},
	"/v3/console/server/guide":        {},
	"/v3/auth/user/login":             {},
	"/v3/auth/user/admin":             {},
}

// adminOnlyExactPaths require a valid globalAdmin token. Exact match so that
// "/v3/auth/user" does not match "/v3/auth/user/login". These paths are
// under /v3/auth/, not /v3/admin/, so the adminOnlyPrefixes entry for
// /v3/admin/ does not cover them.
var adminOnlyExactPaths = map[string]struct{}{
	"/v3/auth/user":            {},
	"/v3/auth/user/list":       {},
	"/v3/auth/user/search":     {},
	"/v3/auth/role":            {},
	"/v3/auth/role/list":       {},
	"/v3/auth/role/search":     {},
	"/v3/auth/permission":      {},
	"/v3/auth/permission/list": {},
	"/v3/auth/permission/has":  {},
}

// adminOnlyPrefixes require a valid globalAdmin token for any path that
// starts with the prefix. Used for subtrees that should be admin-only in
// their entirety.
var adminOnlyPrefixes = []string{
	// /v3/admin/ covers all admin routes — namespace CRUD, config CRUD,
	// AI CRUD (prompt/skill/agentspec/a2a/mcp/apitomcp/dify/mcp-router),
	// cluster ops, plugin ops, loader ops, and ops (backup/restore/metrics/
	// pprof). The public state/health endpoints under /v3/admin/core/state
	// are in authMiddlewareOpenPaths and allowed through before this check.
	// Fail-closed: any new admin route is admin-only by default; add the
	// route to authMiddlewareOpenPaths explicitly to make it public.
	"/v3/admin/",
}

func (m *authMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/nacos")

	if isAuthMiddlewareOpenPath(path) || strings.HasPrefix(path, "/v3/console/ui") {
		m.next.ServeHTTP(w, r)
		return
	}

	token := extractAccessToken(r)
	var claims authsvc.Claims
	if token != "" {
		c, err := m.auth.VerifyToken(token)
		if err != nil {
			if m.tokenBad != nil {
				m.tokenBad.Inc()
			}
			if m.rejectionInvalidToken != nil {
				m.rejectionInvalidToken.Inc()
			}
			writeAuthMiddlewareError(w, err)
			return
		}
		if m.tokenOK != nil {
			m.tokenOK.Inc()
		}
		claims = c
		r = r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims))
	}

	if _, exact := adminOnlyExactPaths[path]; exact {
		if token == "" {
			if m.tokenBad != nil {
				m.tokenBad.Inc()
			}
			if m.rejectionMissingToken != nil {
				m.rejectionMissingToken.Inc()
			}
			writeAuthMiddlewareError(w, authsvc.ErrInvalidToken)
			return
		}
		if !claims.GlobalAdmin {
			if m.rejectionAccessDenied != nil {
				m.rejectionAccessDenied.Inc()
			}
			writeAuthMiddlewareError(w, authsvc.ErrAccessDenied)
			return
		}
	} else {
		for _, prefix := range adminOnlyPrefixes {
			if strings.HasPrefix(path, prefix) {
				if token == "" {
					if m.tokenBad != nil {
						m.tokenBad.Inc()
					}
					if m.rejectionMissingToken != nil {
						m.rejectionMissingToken.Inc()
					}
					writeAuthMiddlewareError(w, authsvc.ErrInvalidToken)
					return
				}
				if !claims.GlobalAdmin {
					if m.rejectionAccessDenied != nil {
						m.rejectionAccessDenied.Inc()
					}
					writeAuthMiddlewareError(w, authsvc.ErrAccessDenied)
					return
				}
				break
			}
		}
	}

	m.next.ServeHTTP(w, r)
}

// extractAccessToken pulls the access token from the Authorization header
// (console/direct REST), the accessToken query parameter (SDK GET), or the
// accessToken form parameter (SDK POST/PUT/DELETE). Returns "" if absent.
func extractAccessToken(r *http.Request) string {
	if h := r.Header.Get(authsvc.AuthorizationHeader); h != "" {
		if t := authsvc.ParseAuthorization(h); t != "" {
			return t
		}
	}
	if t := r.URL.Query().Get("accessToken"); t != "" {
		return t
	}
	if r.Method != http.MethodGet {
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			if err := r.ParseForm(); err == nil {
				if t := r.PostForm.Get("accessToken"); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func isAuthMiddlewareOpenPath(path string) bool {
	_, ok := authMiddlewareOpenPaths[path]
	return ok
}

func writeAuthMiddlewareError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	code := protocol.CodeAccessDenied
	switch {
	case errors.Is(err, authsvc.ErrExpiredToken):
		code = protocol.CodeAccessDenied
	case errors.Is(err, authsvc.ErrInvalidToken):
		code = protocol.CodeAccessDenied
	case errors.Is(err, authsvc.ErrAccessDenied):
		status = http.StatusForbidden
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
