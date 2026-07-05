package server

import (
	"crypto/tls"
	"testing"
)

// TestResolveTLSMinVersionDefaultIs12 verifies that a zero options struct
// resolves to TLS 1.2 — matching Go's crypto/tls recommendation and
// preserving backwards compatibility with existing clients that only
// support TLS 1.2.
func TestResolveTLSMinVersionDefaultIs12(t *testing.T) {
	o := options{}
	got := o.resolveTLSMinVersion()
	if got != tls.VersionTLS12 {
		t.Errorf("default resolveTLSMinVersion = %x, want %x (TLS 1.2)", got, tls.VersionTLS12)
	}
}

// TestResolveTLSMinVersionExplicit13 verifies that an explicit
// WithTLSMinVersion("1.3") option wins over env vars and the default.
// TLS 1.3 disables TLS 1.2 and its cipher suites — useful when
// compliance or policy requires forward secrecy by default.
func TestResolveTLSMinVersionExplicit13(t *testing.T) {
	t.Setenv("GONACOS_TLS_MIN_VERSION", "1.2")
	o := options{TLSMinVersion: "1.3"}
	got := o.resolveTLSMinVersion()
	if got != tls.VersionTLS13 {
		t.Errorf("explicit resolveTLSMinVersion = %x, want %x (TLS 1.3)", got, tls.VersionTLS13)
	}
}

// TestResolveTLSMinVersionEnv verifies that the env var is picked up
// when the explicit option is unset.
func TestResolveTLSMinVersionEnv(t *testing.T) {
	t.Setenv("GONACOS_TLS_MIN_VERSION", "1.3")
	o := options{}
	got := o.resolveTLSMinVersion()
	if got != tls.VersionTLS13 {
		t.Errorf("env resolveTLSMinVersion = %x, want %x (TLS 1.3)", got, tls.VersionTLS13)
	}
}

// TestResolveTLSMinVersionEnvCaseInsensitive verifies that the env var
// is matched case-insensitively — operators shouldn't have to remember
// the exact capitalization.
func TestResolveTLSMinVersionEnvCaseInsensitive(t *testing.T) {
	t.Setenv("GONACOS_TLS_MIN_VERSION", "1.3")
	o := options{}
	got := o.resolveTLSMinVersion()
	if got != tls.VersionTLS13 {
		t.Errorf("env resolveTLSMinVersion (uppercase) = %x, want %x (TLS 1.3)", got, tls.VersionTLS13)
	}
}

// TestResolveTLSMinVersionInvalidFallsBackTo12 verifies that an
// invalid value falls back to TLS 1.2 rather than failing startup —
// a typo should not lock operators out of the server.
func TestResolveTLSMinVersionInvalidFallsBackTo12(t *testing.T) {
	o := options{TLSMinVersion: "0.9"}
	got := o.resolveTLSMinVersion()
	if got != tls.VersionTLS12 {
		t.Errorf("invalid resolveTLSMinVersion = %x, want %x (TLS 1.2 fallback)", got, tls.VersionTLS12)
	}
}
