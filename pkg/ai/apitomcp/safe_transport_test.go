package apitomcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIsDeniedIP covers all the IP ranges the SSRF check rejects. Each
// entry is a documented production-attack vector — adding them here means
// a regression that allows one will fail the test, not silently ship.
func TestIsDeniedIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		// IPv4
		{"loopback v4", "127.0.0.1", true},
		{"loopback v4 high", "127.255.255.254", true},
		{"private 10/8", "10.0.0.1", true},
		{"private 172.16/12", "172.16.0.1", true},
		{"private 172.31/12", "172.31.255.254", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link-local (AWS metadata)", "169.254.169.254", true},
		{"link-local generic", "169.254.1.1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"multicast v4", "224.0.0.1", true},
		{"reserved v4", "240.0.0.1", true},

		// IPv6
		{"loopback v6", "::1", true},
		{"unspecified v6", "::", true},
		{"link-local v6", "fe80::1", true},
		{"unique-local v6", "fc00::1", true},
		{"multicast v6", "ff02::1", true},

		// Public IPs (should NOT be denied)
		{"public v4", "8.8.8.8", false},
		{"public v4 cloudflare", "1.1.1.1", false},
		{"public v6", "2001:4860:4860::8888", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) = nil", c.ip)
			}
			if got := isDeniedIP(ip); got != c.want {
				t.Fatalf("isDeniedIP(%s) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// TestSafeTransportCheckURLIPLiteral verifies that CheckURL rejects URLs
// containing a literal denied IP — the early, friendly error path that
// does not require a DNS lookup. This catches the case where a user
// pastes http://169.254.169.254/ directly into a tool template.
func TestSafeTransportCheckURLIPLiteral(t *testing.T) {
	tr := NewSafeTransport()
	cases := []struct {
		url      string
		wantErr  bool
		wantSubs string
	}{
		{"http://169.254.169.254/latest/meta-data/", true, "169.254.169.254"},
		{"http://127.0.0.1:8848/v3/admin/", true, "127.0.0.1"},
		{"http://10.0.0.1/", true, "10.0.0.1"},
		{"http://localhost/", false, ""}, // hostname — not checked here
		{"https://example.com/", false, ""},
		{"http://8.8.8.8/", false, ""},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			err := tr.CheckURL(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("CheckURL(%q): expected error, got nil", c.url)
			}
			if c.wantErr && c.wantSubs != "" && !strings.Contains(err.Error(), c.wantSubs) {
				t.Fatalf("CheckURL(%q): error %q does not mention %q", c.url, err, c.wantSubs)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("CheckURL(%q): unexpected error %v", c.url, err)
			}
		})
	}
}

// TestSafeTransportCheckURLAllowlist verifies that an allowlisted hostname
// bypasses the check. Operators use this to permit a specific internal MCP
// server whose DNS resolves to a private IP without opening the door to
// all private IPs. The allowlist is a bypass list, not a strict allowlist —
// non-listed hostnames still go through the normal SSRF check at dial time.
func TestSafeTransportCheckURLAllowlist(t *testing.T) {
	tr := NewSafeTransport()
	tr.Allowlist = []string{"internal-mcp.example.corp"}
	// Allowlisted hostname passes CheckURL.
	if err := tr.CheckURL("http://internal-mcp.example.corp/"); err != nil {
		t.Fatalf("allowlisted host: unexpected error %v", err)
	}
	// Non-allowlisted hostname also passes CheckURL because CheckURL only
	// rejects IP literals — the dial-time check handles DNS-resolved IPs.
	if err := tr.CheckURL("http://other-internal.example.corp/"); err != nil {
		t.Fatalf("non-allowlisted hostname: unexpected error %v", err)
	}
}

// TestSafeTransportCheckURLAllowPrivate verifies that AllowPrivate=true
// permits denied IPs. Use this only in trusted development environments.
func TestSafeTransportCheckURLAllowPrivate(t *testing.T) {
	tr := NewSafeTransport()
	tr.AllowPrivate = true
	if err := tr.CheckURL("http://127.0.0.1:8848/"); err != nil {
		t.Fatalf("CheckURL with AllowPrivate: unexpected error %v", err)
	}
	if err := tr.CheckURL("http://169.254.169.254/"); err != nil {
		t.Fatalf("CheckURL with AllowPrivate (metadata): unexpected error %v", err)
	}
}

// TestSafeTransportRejectsLoopbackDial verifies that an HTTP request to a
// loopback address fails at the dial layer. This is the runtime enforcement
// path — even if CheckURL is bypassed, the dial-time check still blocks
// the connection.
func TestSafeTransportRejectsLoopbackDial(t *testing.T) {
	tr := NewSafeTransport()
	client := tr.HTTPClient(5 * time.Second)

	// Start a real server on loopback so the dial is actually attempted
	// (and rejected) rather than failing on DNS or connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("loopback server should not be reached")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Replace the httptest server's host (which is 127.0.0.1) — the dial
	// must reject it before the request lands.
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected dial error for loopback, got nil")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got: %v", err)
	}
}

// TestSafeTransportAllowsPublicDial verifies that public IPs are not
// rejected by the SSRF check. We verify via isDeniedIP directly (rather
// than an actual dial) because a real dial to a public IP would require
// network access and a reachable server, making the test flaky in offline
// environments.
func TestSafeTransportAllowsPublicDial(t *testing.T) {
	tr := NewSafeTransport()

	// CheckURL passes for public IP literals.
	if err := tr.CheckURL("http://8.8.8.8/"); err != nil {
		t.Fatalf("CheckURL public IP: %v", err)
	}
	// isDeniedIP returns false for public IPs.
	publicIPs := []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"}
	for _, ipStr := range publicIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", ipStr)
		}
		if isDeniedIP(ip) {
			t.Fatalf("isDeniedIP(%s) = true, want false", ipStr)
		}
	}
}

// TestSafeTransportHTTPClientTimeout verifies that the HTTP client inherits
// the configured timeout so a slow remote doesn't hold the apitomcp call
// forever.
func TestSafeTransportHTTPClientTimeout(t *testing.T) {
	tr := NewSafeTransport()
	client := tr.HTTPClient(100 * time.Millisecond)
	if client.Timeout != 100*time.Millisecond {
		t.Fatalf("timeout = %v, want 100ms", client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("transport not set on client")
	}
}

// TestSafeTransportDialContextRejectsAWSMetadata verifies that dialing the
// AWS metadata endpoint is blocked. This is the highest-value SSRF vector
// — a successful request returns IAM credentials that grant the attacker
// access to the AWS account.
func TestSafeTransportDialContextRejectsAWSMetadata(t *testing.T) {
	tr := NewSafeTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := tr.dialContext(ctx, "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected ssrf error for metadata endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("error should mention the denied IP, got: %v", err)
	}
}

// TestSafeTransportDialContextAllowPrivatePermitsMetadata verifies that
// AllowPrivate=true permits the metadata endpoint. This is the escape
// hatch for development environments — the test documents that the
// setting actually opens the door, so operators know what they're
// enabling.
func TestSafeTransportDialContextAllowPrivatePermitsMetadata(t *testing.T) {
	tr := NewSafeTransport()
	tr.AllowPrivate = true
	// We can't actually dial 169.254.169.254 in a test, but we can verify
	// the check passes by calling CheckURL.
	if err := tr.CheckURL("http://169.254.169.254/"); err != nil {
		t.Fatalf("AllowPrivate should permit metadata: %v", err)
	}
}

// TestSafeTransportHTTPClientEndToEnd verifies the full request path: a
// public test server responds normally when the SSRF check passes.
func TestSafeTransportHTTPClientEndToEnd(t *testing.T) {
	// Start a server on a public IP is not feasible in tests, so we use
	// httptest's loopback server with AllowPrivate=true to verify the
	// transport wires up correctly and the request completes end-to-end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	}))
	defer srv.Close()

	tr := NewSafeTransport()
	tr.AllowPrivate = true
	client := tr.HTTPClient(5 * time.Second)

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}
