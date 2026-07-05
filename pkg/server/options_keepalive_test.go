package server

import (
	"testing"
	"time"

	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

// TestResolveGRPCKeepAliveZeroIsDisabled verifies that a zero options struct
// (no explicit config, no env vars) resolves to keepalive disabled — the
// legacy behavior. PINGs must not be sent unless the operator opts in.
func TestResolveGRPCKeepAliveZeroIsDisabled(t *testing.T) {
	o := options{}
	got := o.resolveGRPCKeepAlive()
	if got.ReadIdleTimeout != 0 {
		t.Errorf("ReadIdleTimeout = %v, want 0 (disabled)", got.ReadIdleTimeout)
	}
	if got.PingTimeout != 0 {
		t.Errorf("PingTimeout = %v, want 0 (disabled)", got.PingTimeout)
	}
}

// TestResolveGRPCKeepAliveExplicit verifies that an explicit WithGRPCKeepAlive
// option wins over env vars.
func TestResolveGRPCKeepAliveExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_KEEPALIVE_READ_IDLE", "99s")
	t.Setenv("GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT", "99s")

	o := options{
		GRPCKeepAlive: GRPCKeepAliveConfig{
			ReadIdleTimeout: 15 * time.Second,
			PingTimeout:     15 * time.Second,
		},
	}
	got := o.resolveGRPCKeepAlive()
	if got.ReadIdleTimeout != 15*time.Second {
		t.Errorf("ReadIdleTimeout = %v, want 15s (explicit)", got.ReadIdleTimeout)
	}
	if got.PingTimeout != 15*time.Second {
		t.Errorf("PingTimeout = %v, want 15s (explicit)", got.PingTimeout)
	}
}

// TestResolveGRPCKeepAliveEnv verifies that env vars are picked up when the
// explicit option is unset.
func TestResolveGRPCKeepAliveEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_KEEPALIVE_READ_IDLE", "20s")
	t.Setenv("GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT", "10s")

	o := options{}
	got := o.resolveGRPCKeepAlive()
	if got.ReadIdleTimeout != 20*time.Second {
		t.Errorf("env ReadIdleTimeout = %v, want 20s", got.ReadIdleTimeout)
	}
	if got.PingTimeout != 10*time.Second {
		t.Errorf("env PingTimeout = %v, want 10s", got.PingTimeout)
	}
}

// TestResolveGRPCKeepAliveBadEnvIsIgnored verifies that an unparseable env
// var is silently ignored rather than causing a startup failure. Keepalive
// falls back to disabled in that case.
func TestResolveGRPCKeepAliveBadEnvIsIgnored(t *testing.T) {
	t.Setenv("GONACOS_GRPC_KEEPALIVE_READ_IDLE", "not-a-duration")
	t.Setenv("GONACOS_GRPC_KEEPALIVE_PING_TIMEOUT", "not-a-duration")

	o := options{}
	got := o.resolveGRPCKeepAlive()
	if got.ReadIdleTimeout != 0 {
		t.Errorf("bad-env ReadIdleTimeout = %v, want 0 (ignored)", got.ReadIdleTimeout)
	}
	if got.PingTimeout != 0 {
		t.Errorf("bad-env PingTimeout = %v, want 0 (ignored)", got.PingTimeout)
	}
}

// TestWithGRPCKeepAliveSetsFields verifies that the WithGRPCKeepAlive option
// populates the options struct correctly. Guards against the option
// function being wired to the wrong field.
func TestWithGRPCKeepAliveSetsFields(t *testing.T) {
	o := options{}
	WithGRPCKeepAlive(15*time.Second, 20*time.Second)(&o)
	if o.GRPCKeepAlive.ReadIdleTimeout != 15*time.Second {
		t.Errorf("ReadIdleTimeout = %v, want 15s", o.GRPCKeepAlive.ReadIdleTimeout)
	}
	if o.GRPCKeepAlive.PingTimeout != 20*time.Second {
		t.Errorf("PingTimeout = %v, want 20s", o.GRPCKeepAlive.PingTimeout)
	}
}

// TestResolveGRPCKeepAliveReturnsGRPCType verifies that the resolve method
// returns the grpc-package KeepAliveConfig type (not the options-local
// type). This catches a future refactor that accidentally returns the wrong
// type and breaks the [Server.New] wiring.
func TestResolveGRPCKeepAliveReturnsGRPCType(t *testing.T) {
	o := options{
		GRPCKeepAlive: GRPCKeepAliveConfig{
			ReadIdleTimeout: 15 * time.Second,
			PingTimeout:     15 * time.Second,
		},
	}
	got := o.resolveGRPCKeepAlive()
	var _ grpcsrv.KeepAliveConfig = got
}

// TestResolveGRPCReadFrameTimeoutDefaultIs30s verifies that a zero
// options struct resolves to 30s — generous for legitimate clients
// (a 4 MiB frame at ~133 KB/s) while bounding the slowloris-on-body
// window. Without a default, the gRPC path would be vulnerable to a
// peer sending a frame body 1 byte at a time and holding the server's
// goroutine for up to GRPCMaxFrameBytes seconds (4 MiB at 1 byte/sec
// ≈ 48 days).
func TestResolveGRPCReadFrameTimeoutDefaultIs30s(t *testing.T) {
	o := options{}
	got := o.resolveGRPCReadFrameTimeout()
	if got != 30*time.Second {
		t.Errorf("default resolveGRPCReadFrameTimeout = %v, want 30s", got)
	}
}

// TestResolveGRPCReadFrameTimeoutExplicit verifies that an explicit
// WithGRPCReadFrameTimeout option wins over env vars.
func TestResolveGRPCReadFrameTimeoutExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_READ_FRAME_TIMEOUT", "99s")
	o := options{GRPCReadFrameTimeout: 5 * time.Second}
	got := o.resolveGRPCReadFrameTimeout()
	if got != 5*time.Second {
		t.Errorf("explicit resolveGRPCReadFrameTimeout = %v, want 5s", got)
	}
}

// TestResolveGRPCReadFrameTimeoutEnv verifies that the env var is
// picked up when the explicit option is unset.
func TestResolveGRPCReadFrameTimeoutEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_READ_FRAME_TIMEOUT", "45s")
	o := options{}
	got := o.resolveGRPCReadFrameTimeout()
	if got != 45*time.Second {
		t.Errorf("env resolveGRPCReadFrameTimeout = %v, want 45s", got)
	}
}

// TestResolveGRPCReadFrameTimeoutNegativeDisables verifies that a
// negative value disables the cap (the underlying reader delegates to
// ReadFrameWithLimit without a deadline). Not recommended in
// production — re-opens the slowloris window.
func TestResolveGRPCReadFrameTimeoutNegativeDisables(t *testing.T) {
	o := options{GRPCReadFrameTimeout: -1}
	got := o.resolveGRPCReadFrameTimeout()
	if got != -1 {
		t.Errorf("negative resolveGRPCReadFrameTimeout = %v, want -1", got)
	}
}
