package server

import (
	"testing"
	"time"
)

// TestResolveHTTPHardeningDefaults verifies the safe-default behavior of the
// HTTP rate-limit / max-body / timeout resolvers.
func TestResolveHTTPHardeningDefaults(t *testing.T) {
	o := options{}

	if got := o.resolveHTTPRateRPS(); got != 0 {
		t.Errorf("default resolveHTTPRateRPS = %v, want 0 (disabled)", got)
	}
	if got := o.resolveHTTPRateBurst(); got != 0 {
		t.Errorf("default resolveHTTPRateBurst = %v, want 0", got)
	}
	if got := o.resolveHTTPMaxBody(); got != 10*1024*1024 {
		t.Errorf("default resolveHTTPMaxBody = %d, want %d", got, 10*1024*1024)
	}
	if got := o.resolveHTTPWriteTimeout(); got != 30*time.Second {
		t.Errorf("default resolveHTTPWriteTimeout = %v, want 30s", got)
	}
	if got := o.resolveHTTPIdleTimeout(); got != 120*time.Second {
		t.Errorf("default resolveHTTPIdleTimeout = %v, want 120s", got)
	}
}

// TestResolveHTTPHardeningConfigured verifies that explicit options override
// the defaults, including negative values that disable a cap.
func TestResolveHTTPHardeningConfigured(t *testing.T) {
	o := options{
		HTTPRateRPS:      50,
		HTTPRateBurst:    100,
		HTTPMaxBodyBytes: 2048,
		HTTPWriteTimeout: 10 * time.Second,
		HTTPIdleTimeout:  60 * time.Second,
	}
	if got := o.resolveHTTPRateRPS(); got != 50 {
		t.Errorf("resolveHTTPRateRPS = %v, want 50", got)
	}
	if got := o.resolveHTTPRateBurst(); got != 100 {
		t.Errorf("resolveHTTPRateBurst = %v, want 100", got)
	}

	// Burst defaults to 2x rps when burst is unset but rps is set.
	o.HTTPRateBurst = 0
	if got := o.resolveHTTPRateBurst(); got != 100 {
		t.Errorf("default burst = %v, want 100 (2x rps)", got)
	}

	if got := o.resolveHTTPMaxBody(); got != 2048 {
		t.Errorf("resolveHTTPMaxBody = %d, want 2048", got)
	}
	if got := o.resolveHTTPWriteTimeout(); got != 10*time.Second {
		t.Errorf("resolveHTTPWriteTimeout = %v, want 10s", got)
	}
	if got := o.resolveHTTPIdleTimeout(); got != 60*time.Second {
		t.Errorf("resolveHTTPIdleTimeout = %v, want 60s", got)
	}

	// Negative max-body disables the cap.
	o.HTTPMaxBodyBytes = -1
	if got := o.resolveHTTPMaxBody(); got != -1 {
		t.Errorf("resolveHTTPMaxBody (disabled) = %d, want -1", got)
	}

	// Negative write timeout disables it.
	o.HTTPWriteTimeout = -1
	if got := o.resolveHTTPWriteTimeout(); got != -1 {
		t.Errorf("resolveHTTPWriteTimeout (disabled) = %v, want -1", got)
	}
}

// TestResolveHTTPHardeningEnv verifies that env vars are picked up when the
// explicit option is unset.
func TestResolveHTTPHardeningEnv(t *testing.T) {
	t.Setenv("GONACOS_HTTP_RATE_RPS", "75")
	t.Setenv("GONACOS_HTTP_MAX_BODY", "5242880")
	t.Setenv("GONACOS_HTTP_WRITE_TIMEOUT", "45s")
	t.Setenv("GONACOS_HTTP_IDLE_TIMEOUT", "90s")

	o := options{}
	if got := o.resolveHTTPRateRPS(); got != 75 {
		t.Errorf("env resolveHTTPRateRPS = %v, want 75", got)
	}
	if got := o.resolveHTTPRateBurst(); got != 150 {
		t.Errorf("env-derived resolveHTTPRateBurst = %v, want 150 (2x rps)", got)
	}
	if got := o.resolveHTTPMaxBody(); got != 5242880 {
		t.Errorf("env resolveHTTPMaxBody = %d, want 5242880", got)
	}
	if got := o.resolveHTTPWriteTimeout(); got != 45*time.Second {
		t.Errorf("env resolveHTTPWriteTimeout = %v, want 45s", got)
	}
	if got := o.resolveHTTPIdleTimeout(); got != 90*time.Second {
		t.Errorf("env resolveHTTPIdleTimeout = %v, want 90s", got)
	}
}
