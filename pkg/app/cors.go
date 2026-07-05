package app

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig controls cross-origin resource sharing for the HTTP API. It is
// relevant when the React console is served from a different origin than the
// API (e.g., a CDN-hosted frontend or a different subdomain). Same-origin
// deployments leave CORS disabled (the default) — the browser doesn't send
// preflight requests and the middleware is a no-op.
type CORSConfig struct {
	// Enabled gates the middleware. When false, requests pass through
	// untouched. Default false — opt in via WithCORS or
	// GONACOS_CORS_ENABLED=1.
	Enabled bool

	// AllowOrigins is the list of origin URLs permitted to make cross-origin
	// requests. A single "*" wildcard is allowed only when AllowCredentials
	// is false. When the list is empty and Enabled is true, the middleware
	// falls back to "*". Origins are matched exactly (scheme + host [+ port]);
	// "null" (file:// pages) is treated as a literal origin.
	AllowOrigins []string

	// AllowMethods is the list of HTTP methods exposed via
	// Access-Control-Allow-Methods. Defaults to the standard Nacos API set
	// (GET, POST, PUT, DELETE, OPTIONS) when empty.
	AllowMethods []string

	// AllowHeaders is the list of request headers exposed via
	// Access-Control-Allow-Headers. Defaults to Content-Type, Authorization,
	// X-CSRF-Token, accessToken — covering the SDK and React console needs.
	AllowHeaders []string

	// AllowCredentials, when true, sets Access-Control-Allow-Credentials:
	// true. Browsers refuse to send cookies/credentials cross-origin unless
	// this is true. Cannot be combined with AllowOrigins=["*"] — set
	// explicit origins instead.
	AllowCredentials bool

	// MaxAge is the number of seconds browsers may cache preflight results.
	// Defaults to 600 when zero. Higher values reduce preflight traffic but
	// delay policy propagation to existing clients.
	MaxAge int
}

// defaultCORSAllowMethods is the standard Nacos API method set.
var defaultCORSAllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}

// defaultCORSAllowHeaders covers the SDK (accessToken), the React console
// (Content-Type, Authorization), and CSRF-protected deployments (X-CSRF-Token).
var defaultCORSAllowHeaders = []string{"Content-Type", "Authorization", "X-CSRF-Token", "accessToken"}

// corsMiddleware injects CORS response headers and short-circuits OPTIONS
// preflight requests. It is a no-op when cfg.Enabled is false.
type corsMiddleware struct {
	next http.Handler
	cfg  CORSConfig
}

// NewCORSMiddleware wraps next with CORS handling. Pass a CORSConfig with
// Enabled=true to activate; otherwise the middleware passes requests through
// untouched.
func NewCORSMiddleware(cfg CORSConfig, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}
	if len(cfg.AllowMethods) == 0 {
		cfg.AllowMethods = defaultCORSAllowMethods
	}
	if len(cfg.AllowHeaders) == 0 {
		cfg.AllowHeaders = defaultCORSAllowHeaders
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 600
	}
	if len(cfg.AllowOrigins) == 0 {
		// Default to "*" only when credentials are off; otherwise leave empty
		// so no origin is echoed back (browser will reject the request).
		if !cfg.AllowCredentials {
			cfg.AllowOrigins = []string{"*"}
		}
	}
	return &corsMiddleware{next: next, cfg: cfg}
}

func (m *corsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowed := ""
	if origin != "" {
		allowed = m.allowedOrigin(origin)
		if allowed != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowed)
			if m.cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Add("Vary", "Origin")
			}
		}
	}
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		// Preflight: respond without delegating to the inner handler. The
		// inner handler would likely return 401/404 on an OPTIONS request,
		// which the browser would surface as a CORS error even though the
		// preflight policy is what matters.
		h := w.Header()
		h.Set("Access-Control-Allow-Methods", strings.Join(m.cfg.AllowMethods, ", "))
		h.Set("Access-Control-Allow-Headers", strings.Join(m.cfg.AllowHeaders, ", "))
		h.Set("Access-Control-Max-Age", strconv.Itoa(m.cfg.MaxAge))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.next.ServeHTTP(w, r)
}

// allowedOrigin returns the origin to echo back, or "" if the request origin
// is not permitted. A wildcard config returns "*" (or "" when credentials
// are on, since "*" is invalid in that mode).
func (m *corsMiddleware) allowedOrigin(origin string) string {
	for _, o := range m.cfg.AllowOrigins {
		if o == "*" {
			if m.cfg.AllowCredentials {
				return ""
			}
			return "*"
		}
		if o == origin {
			return origin
		}
	}
	return ""
}
