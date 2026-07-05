package app

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// TrustedProxyChecker is the package-level gate for proxy-set headers.
// Set once by [SetTrustedProxies] from server.New before any request
// is served. Nil means no proxy is trusted — X-Forwarded-For and
// X-Real-IP are ignored entirely and RemoteAddr is used directly.
//
// Package-level rather than passed through every IP-extraction call
// site (clientIP, clientIPForLimit, auditFromRequest) because those
// are called from HTTP handlers that don't have a server reference.
// A package-level setter keeps the call sites unchanged.
var TrustedProxyChecker ProxyChecker

// ProxyChecker decides whether a request's immediate peer is a
// trusted proxy and, if so, extracts the real client IP from the
// proxy-set headers. Implementations must be safe for concurrent use.
type ProxyChecker interface {
	// IsTrusted reports whether the peer's RemoteAddr (host:port) is
	// in the configured trusted-proxy CIDR list. When false, the
	// caller MUST ignore X-Forwarded-For and X-Real-IP — they could
	// be forged by the peer.
	IsTrusted(remoteAddr string) bool

	// ClientIP returns the real client IP for the request, honoring
	// proxy-set headers only when the peer is trusted. When the peer
	// is not trusted, returns the host part of RemoteAddr.
	ClientIP(r *http.Request) string
}

// cidrProxyChecker is the default ProxyChecker, backed by a list of
// parsed CIDR ranges. A peer is trusted when its host matches any of
// the ranges. The CIDRs are parsed once at construction; IsTrusted
// takes a read lock on the mutex so concurrent requests don't
// contend on the parse.
type cidrProxyChecker struct {
	mu    sync.RWMutex
	cidrs []*net.IPNet
}

// NewCIDRProxyChecker parses cidrs into a ProxyChecker. Invalid CIDR
// entries are silently skipped — a malformed entry should not crash
// the server, and the operator will notice the missing trust when
// X-Forwarded-For does not flow through. Returns nil when cidrs is
// empty (the secure default — no proxy trusted).
func NewCIDRProxyChecker(cidrs []string) ProxyChecker {
	if len(cidrs) == 0 {
		return nil
	}
	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// A bare IP (no /) is treated as a /32 (v4) or /128 (v6)
		// — common shorthand for "trust exactly this host".
		if !strings.Contains(c, "/") {
			ip := net.ParseIP(c)
			if ip == nil {
				continue
			}
			if ip.To4() != nil {
				c += "/32"
			} else {
				c += "/128"
			}
		}
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		parsed = append(parsed, ipnet)
	}
	if len(parsed) == 0 {
		return nil
	}
	return &cidrProxyChecker{cidrs: parsed}
}

// IsTrusted reports whether remoteAddr's host is in any of the
// configured CIDR ranges. remoteAddr is the request's RemoteAddr,
// which is host:port. The port is stripped before the CIDR match;
// an unparseable RemoteAddr (no port) is treated as not trusted —
// the kernel always sends host:port for TCP connections, so a
// missing port signals something unusual (in-memory test, Unix
// socket adapter) where trusting XFF would be wrong.
func (c *cidrProxyChecker) IsTrusted(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.cidrs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the real client IP for the request. When the
// peer is a trusted proxy, the IP is taken from X-Forwarded-For
// (first hop) or X-Real-IP. When the peer is not trusted, those
// headers are ignored and the host part of RemoteAddr is returned.
//
// X-Forwarded-For is a comma-separated chain: "client, proxy1,
// proxy2". The first entry is the original client; subsequent
// entries are proxies that appended themselves. We take the first
// entry only — when the immediate peer is trusted, that peer is
// expected to have set the header correctly, and any entries after
// the first are the peer's own upstream chain (which we did not
// configure to trust).
//
// An empty X-Forwarded-For or X-Real-IP falls back to RemoteAddr —
// a misconfigured proxy that doesn't set the headers should not
// produce an empty audit IP.
func (c *cidrProxyChecker) ClientIP(r *http.Request) string {
	if c.IsTrusted(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if idx := strings.IndexByte(xff, ','); idx >= 0 {
				if first := strings.TrimSpace(xff[:idx]); first != "" {
					return first
				}
			}
			if v := strings.TrimSpace(xff); v != "" {
				return v
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if v := strings.TrimSpace(xri); v != "" {
				return v
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SetTrustedProxies wires the trusted-proxy list into the
// package-level gate. Called once from server.New after parsing the
// configured CIDRs. Nil or empty cidrs clears the gate (no proxy
// trusted) — backward compatible with embedders that haven't
// configured proxies yet (they just get the secure default).
func SetTrustedProxies(cidrs []string) {
	TrustedProxyChecker = NewCIDRProxyChecker(cidrs)
}

// clientIPFromRequest returns the real client IP for the request,
// honoring the trusted-proxy gate. When no gate is configured (nil
// TrustedProxyChecker — the default), X-Forwarded-For and X-Real-IP
// are ignored entirely and RemoteAddr is used directly. This is the
// secure default for a direct deployment: a peer that is not a
// configured proxy must not be able to forge the IP that lands in
// audit logs and per-IP rate-limit buckets.
func clientIPFromRequest(r *http.Request) string {
	if TrustedProxyChecker != nil {
		return TrustedProxyChecker.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
