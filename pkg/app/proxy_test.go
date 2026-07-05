package app

import (
	"net/http"
	"testing"
)

// TestClientIPNoTrustedProxyIgnoresXFF verifies that when no trusted
// proxy is configured (the secure default), X-Forwarded-For and
// X-Real-IP headers are IGNORED — the request's RemoteAddr is used
// directly.
//
// Without this gate, any client could forge X-Forwarded-For to:
//   - bypass per-IP rate limits (fresh bucket per forged IP)
//   - evade login throttling (throttle is per (ip, username))
//   - pollute the audit trail with a spoofed IP (brute-force from
//     "1.2.3.4" while the real attacker is elsewhere)
//
// The trusted-proxy gate is the standard mitigation: only honor
// proxy-set headers when the immediate peer is a configured proxy.
func TestClientIPNoTrustedProxyIgnoresXFF(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = nil // explicit: no proxy trusted

	req := &http.Request{
		RemoteAddr: "203.0.113.5:1234",
		Header: http.Header{
			"X-Forwarded-For": []string{"1.2.3.4"},
			"X-Real-Ip":       []string{"5.6.7.8"},
		},
	}
	got := clientIP(req)
	if got != "203.0.113.5" {
		t.Errorf("clientIP with no trusted proxy = %q, want %q (XFF must be ignored)", got, "203.0.113.5")
	}
}

// TestClientIPTrustedProxyHonorsXFF verifies that when the peer is a
// configured trusted proxy, X-Forwarded-For (first hop) is honored
// and X-Real-IP is used as a fallback.
func TestClientIPTrustedProxyHonorsXFF(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	req := &http.Request{
		RemoteAddr: "10.0.0.1:1234", // peer is in 10.0.0.0/8 — trusted
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.7"},
		},
	}
	got := clientIP(req)
	if got != "198.51.100.7" {
		t.Errorf("clientIP with trusted proxy = %q, want %q (XFF should be honored)", got, "198.51.100.7")
	}
}

// TestClientIPTrustedProxyHonorsXRealIPFallback verifies that when
// the peer is trusted and X-Forwarded-For is absent, X-Real-IP is
// used as a fallback.
func TestClientIPTrustedProxyHonorsXRealIPFallback(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	req := &http.Request{
		RemoteAddr: "10.0.0.1:1234",
		Header: http.Header{
			"X-Real-Ip": []string{"198.51.100.9"},
		},
	}
	got := clientIP(req)
	if got != "198.51.100.9" {
		t.Errorf("clientIP with trusted proxy (X-Real-IP fallback) = %q, want %q", got, "198.51.100.9")
	}
}

// TestClientIPTrustedProxyXFFChainTakesFirstHop verifies that when
// X-Forwarded-For is a comma-separated chain ("client, proxy1,
// proxy2"), the FIRST entry (the original client) is extracted —
// subsequent entries are the peer's own upstream chain, which we
// did not configure to trust.
func TestClientIPTrustedProxyXFFChainTakesFirstHop(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	req := &http.Request{
		RemoteAddr: "10.0.0.1:1234",
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.20, 10.0.0.2, 10.0.0.3"},
		},
	}
	got := clientIP(req)
	if got != "198.51.100.20" {
		t.Errorf("clientIP XFF chain = %q, want %q (first hop)", got, "198.51.100.20")
	}
}

// TestClientIPTrustedProxyUntrustedPeerIgnoresXFF verifies that when
// the trusted-proxy list is configured but the immediate peer is NOT
// in it, X-Forwarded-For is ignored — a non-trusted peer must not be
// able to forge the IP.
//
// This is the spoofing attack: an external client (not in 10.0.0.0/8)
// sends X-Forwarded-For: 1.2.3.4 hoping to bypass rate limits. The
// gate rejects the header and uses the peer's real RemoteAddr.
func TestClientIPTrustedProxyUntrustedPeerIgnoresXFF(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	req := &http.Request{
		RemoteAddr: "203.0.113.99:1234", // peer NOT in 10.0.0.0/8
		Header: http.Header{
			"X-Forwarded-For": []string{"1.2.3.4"}, // spoofed
		},
	}
	got := clientIP(req)
	if got != "203.0.113.99" {
		t.Errorf("clientIP untrusted peer = %q, want %q (XFF must be ignored)", got, "203.0.113.99")
	}
}

// TestClientIPEmptyXFFFallsBackToRemoteAddr verifies that when the
// peer is trusted but X-Forwarded-For and X-Real-IP are both empty,
// the function falls back to RemoteAddr — a misconfigured proxy
// that doesn't set the headers should not produce an empty audit IP.
func TestClientIPEmptyXFFFallsBackToRemoteAddr(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})

	req := &http.Request{
		RemoteAddr: "10.0.0.1:1234",
		Header:     http.Header{}, // no XFF, no X-Real-IP
	}
	got := clientIP(req)
	if got != "10.0.0.1" {
		t.Errorf("clientIP with empty XFF = %q, want %q (fallback to RemoteAddr)", got, "10.0.0.1")
	}
}

// TestNewCIDRProxyCheckerBareIPIsTreatedAsHost verifies that a bare
// IP (no /) is treated as a /32 (v4) or /128 (v6) — common shorthand
// for "trust exactly this host".
func TestNewCIDRProxyCheckerBareIPIsTreatedAsHost(t *testing.T) {
	c := NewCIDRProxyChecker([]string{"192.168.1.5"}).(*cidrProxyChecker)
	if len(c.cidrs) != 1 {
		t.Fatalf("expected 1 CIDR, got %d", len(c.cidrs))
	}
	ones, _ := c.cidrs[0].Mask.Size()
	if ones != 32 {
		t.Errorf("bare IPv4 should be /32, got /%d", ones)
	}
	if !c.IsTrusted("192.168.1.5:1234") {
		t.Errorf("192.168.1.5 should be trusted")
	}
	if c.IsTrusted("192.168.1.6:1234") {
		t.Errorf("192.168.1.6 should NOT be trusted (only .5 is)")
	}
}

// TestNewCIDRProxyCheckerInvalidCIDRsAreSkipped verifies that invalid
// CIDR entries are silently skipped — a malformed entry should not
// crash the server, and the operator will notice the missing trust
// when X-Forwarded-For does not flow through.
func TestNewCIDRProxyCheckerInvalidCIDRsAreSkipped(t *testing.T) {
	c := NewCIDRProxyChecker([]string{"10.0.0.0/8", "not-a-cidr", "192.168.1.0/24", "256.256.256.256"})
	if c == nil {
		t.Fatal("expected non-nil checker with valid entries")
	}
	cc := c.(*cidrProxyChecker)
	if len(cc.cidrs) != 2 {
		t.Errorf("expected 2 valid CIDRs (invalid ones skipped), got %d", len(cc.cidrs))
	}
}

// TestNewCIDRProxyCheckerEmptyReturnsNil verifies that an empty list
// returns nil — the secure default (no proxy trusted). This is the
// backward-compatible path for embedders that haven't configured
// proxies yet.
func TestNewCIDRProxyCheckerEmptyReturnsNil(t *testing.T) {
	if got := NewCIDRProxyChecker(nil); got != nil {
		t.Errorf("NewCIDRProxyChecker(nil) = %v, want nil", got)
	}
	if got := NewCIDRProxyChecker([]string{}); got != nil {
		t.Errorf("NewCIDRProxyChecker([]) = %v, want nil", got)
	}
}

// TestSetTrustedProxiesEnvOverride verifies that
// SetTrustedProxies(nil) clears the package-level gate — re-enabling
// the secure default where X-Forwarded-For is ignored. This matters
// for tests that mutate the package state.
func TestSetTrustedProxiesNilClearsGate(t *testing.T) {
	TrustedProxyChecker = NewCIDRProxyChecker([]string{"10.0.0.0/8"})
	SetTrustedProxies(nil)
	if TrustedProxyChecker != nil {
		t.Errorf("SetTrustedProxies(nil) did not clear the gate")
	}
}

// TestClientIPForLimitRespectsTrustedProxyGate verifies that the
// rate-limit IP extraction goes through the same trusted-proxy gate
// as the audit IP — a spoofed X-Forwarded-For must not yield a
// fresh rate-limit bucket.
func TestClientIPForLimitRespectsTrustedProxyGate(t *testing.T) {
	defer func() { TrustedProxyChecker = nil }()
	TrustedProxyChecker = nil // no proxy trusted

	req := &http.Request{
		RemoteAddr: "203.0.113.7:1234",
		Header: http.Header{
			"X-Forwarded-For": []string{"1.2.3.4"}, // spoofed
		},
	}
	got := clientIPForLimit(req)
	if got != "203.0.113.7" {
		t.Errorf("clientIPForLimit with no trusted proxy = %q, want %q (XFF must be ignored — spoofing would bypass rate limit)", got, "203.0.113.7")
	}
}
