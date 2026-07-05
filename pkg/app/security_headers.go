package app

import "net/http"

// securityHeadersMiddleware injects standard security response headers on
// every outbound response. The headers protect the embedded React console
// and the JSON API from common client-side attacks:
//
//   - X-Content-Type-Options: nosniff — blocks MIME sniffing that could
//     turn a JSON response into an executable.
//   - X-Frame-Options: SAMEORIGIN — allows the console to be framed by
//     itself ( SPA history navigation in an iframe) but blocks cross-origin
//     embedding (clickjacking).
//   - Referrer-Policy: strict-origin-when-cross-origin — leaks only the
//     origin (not the full URL or query) to cross-origin destinations.
//   - X-XSS-Protection: 0 — modern browsers ignore this; setting 0
//     disables the buggy legacy auditor that could introduce its own XSS.
//   - Strict-Transport-Security — only emitted when the request arrived
//     over TLS, so a plaintext deployment doesn't pin HSTS prematurely.
//
// Headers are set before the inner handler runs so the inner handler can
// override them (e.g., to allow a specific frame target for the console
// UI). The middleware does not set Content-Security-Policy because a
// correct CSP for the Monaco-based console requires hashing every inline
// script and worker — out of scope for this round.
type securityHeadersMiddleware struct {
	next http.Handler
	tls  bool
}

// NewSecurityHeadersMiddleware wraps next with standard security response
// headers. When tls is true, HSTS is emitted; pass false for plaintext
// deployments.
func NewSecurityHeadersMiddleware(tls bool, next http.Handler) http.Handler {
	return &securityHeadersMiddleware{next: next, tls: tls}
}

func (m *securityHeadersMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if h.Get("X-Content-Type-Options") == "" {
		h.Set("X-Content-Type-Options", "nosniff")
	}
	if h.Get("X-Frame-Options") == "" {
		h.Set("X-Frame-Options", "SAMEORIGIN")
	}
	if h.Get("Referrer-Policy") == "" {
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	}
	if h.Get("X-XSS-Protection") == "" {
		h.Set("X-XSS-Protection", "0")
	}
	if m.tls && h.Get("Strict-Transport-Security") == "" {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
	m.next.ServeHTTP(w, r)
}
