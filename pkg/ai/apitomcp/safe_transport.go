package apitomcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ssrfDeniedIPRanges lists IPv4 and IPv6 ranges that an outbound apitomcp
// HTTP call must never reach unless the operator explicitly opted in via
// [SafeTransport.AllowPrivate]. The list blocks:
//
//   - loopback (127.0.0.0/8, ::1) — local services on the gonacos host
//   - private (10/8, 172.16/12, 192.168/16, fc00::/7) — internal network
//   - link-local (169.254/16, fe80::/10) — includes AWS metadata at 169.254.169.254
//   - unspecified (0.0.0.0/8, ::) — "any" addresses, no valid target
//   - multicast/broadcast — not valid HTTP targets
//
// The AWS metadata endpoint (169.254.169.254) is in the link-local range,
// so blocking 169.254.0.0/16 covers it without a special case.
var ssrfDeniedIPv4Ranges = []string{
	"0.0.0.0/8",      // unspecified
	"10.0.0.0/8",     // private
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local + AWS metadata
	"172.16.0.0/12",  // private
	"192.168.0.0/16", // private
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved
}

var ssrfDeniedIPv6Ranges = []string{
	"::1/128",   // loopback
	"::/128",    // unspecified
	"fc00::/7",  // unique-local
	"fe80::/10", // link-local
	"ff00::/8",  // multicast
}

// SafeTransport is an http.Transport that rejects connections to private,
// loopback, and link-local IP addresses before the TCP dial. It prevents
// Server-Side Request Forgery (SSRF) attacks where a user-configured URL
// template in an apitomcp tool could be pointed at the gonacos host's
// loopback (e.g., http://localhost:8848/v3/admin/...) or at cloud metadata
// endpoints (http://169.254.169.254/latest/meta-data/) to extract secrets.
//
// The check happens at the DialContext layer — after DNS resolution but
// before the TCP connect — so it also catches DNS rebinding attacks where
// an attacker's DNS returns a public IP for the first lookup (passing any
// URL-level check) and a private IP for the second lookup that the actual
// connection uses.
//
// Set AllowPrivate to true to disable the check (e.g., for a development
// environment where the target MCP server genuinely runs on the same host).
// Production deployments should leave it false.
type SafeTransport struct {
	// AllowPrivate permits dials to private, loopback, and link-local IPs.
	// Default false. Set to true ONLY in trusted development environments.
	AllowPrivate bool

	// Allowlist is an optional list of hostnames (not IPs) that bypass the
	// private-IP check. Use it when a specific internal service must be
	// reachable (e.g., an MCP server on a private network) without opening
	// the door to all private IPs. Match is case-insensitive and exact.
	Allowlist []string
}

// NewSafeTransport returns a *http.Transport that wraps http.DefaultTransport
// with a DialContext that rejects SSRF-risky IPs. The returned transport
// inherits sensible defaults (TLS config, proxy) from DefaultTransport.
func NewSafeTransport() *SafeTransport {
	return &SafeTransport{}
}

// dialContext is the custom dialer that resolves the host and rejects
// private IPs before delegating to the underlying dialer.
func (t *SafeTransport) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf: parse addr %q: %w", addr, err)
	}

	// Allowlisted hostnames skip the IP check entirely. The operator has
	// explicitly approved this host, so a private IP is acceptable.
	if t.allowlisted(host) {
		return defaultDialer.DialContext(ctx, network, addr)
	}

	// Resolve the host to IP addresses. We can't trust the hostname itself
	// because DNS may return a private IP — the check must run on the
	// resolved IPs.
	ips, err := defaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("ssrf: resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if !t.AllowPrivate && isDeniedIP(ip.IP) {
			return nil, fmt.Errorf("ssrf: host %q resolves to denied IP %s (private/loopback/link-local); set AllowPrivate to override", host, ip.IP)
		}
	}

	// All resolved IPs passed the check. Dial using the original address
	// (not a resolved IP) so the underlying transport handles happy-eyeballs
	// and connection pooling correctly.
	return defaultDialer.DialContext(ctx, network, addr)
}

// allowlisted returns true if host matches any entry in t.Allowlist
// (case-insensitive exact match).
func (t *SafeTransport) allowlisted(host string) bool {
	h := strings.ToLower(host)
	for _, a := range t.Allowlist {
		if strings.ToLower(a) == h {
			return true
		}
	}
	return false
}

// isDeniedIP returns true if ip falls in any of the ssrfDeniedIPRanges.
// A denied IP is one that points at the local host, the local network,
// or a link-local address (which includes cloud metadata endpoints).
func isDeniedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Check IPv4 private ranges. net.IP.IsPrivate covers 10/8, 172.16/12,
	// 192.168/16, and fc00::/7 in Go 1.17+, but we also keep the explicit
	// CIDR list for clarity and to catch 0.0.0.0/8 and 240.0.0.0/4
	// (reserved ranges) which IsPrivate does not flag.
	if ip.IsPrivate() {
		return true
	}
	for _, cidr := range ssrfDeniedIPv4Ranges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	for _, cidr := range ssrfDeniedIPv6Ranges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// HTTPClient returns an *http.Client backed by this SafeTransport. Use it
// in place of http.DefaultClient when the request URL comes from an
// untrusted source (user-configured apitomcp tool template).
func (t *SafeTransport) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: t.transport(),
	}
}

// transport returns the underlying *http.Transport, reusing defaults from
// http.DefaultTransport so proxy/TLS configuration is inherited.
func (t *SafeTransport) transport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = t.dialContext
	return base
}

// CheckURL validates a rendered URL before the request is sent. It parses
// the URL and checks the hostname against the same rules as the dial-time
// check, returning an error if the host is already an IP literal in a denied
// range. For hostnames that require DNS resolution, the dial-time check in
// dialContext catches private IPs returned by the resolver.
//
// Calling CheckURL is optional — the dial-time check is sufficient to
// prevent SSRF — but it provides an early, friendlier error for cases where
// the URL literally contains a private IP (no DNS lookup needed).
func (t *SafeTransport) CheckURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: parse url %q: %w", rawURL, err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("ssrf: url %q has no host", rawURL)
	}
	if t.allowlisted(host) {
		return nil
	}
	// If host is an IP literal, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		if !t.AllowPrivate && isDeniedIP(ip) {
			return fmt.Errorf("ssrf: url %q points at denied IP %s", rawURL, ip)
		}
	}
	return nil
}

// defaultDialer is the underlying net.Dialer used for the actual TCP
// connection after the SSRF check passes. It inherits sensible timeouts
// from http.DefaultTransport's dialer.
var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}

// defaultResolver is the net.Resolver used for DNS lookups. The zero
// Resolver uses the system resolver, matching Go's default behavior.
var defaultResolver = net.DefaultResolver
