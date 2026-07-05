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

// TestResolveGRPCMaxConcurrentStreamsDefaultIs100 verifies that a zero
// options struct resolves to 100 — matching Go's http2.Server default
// and the gRPC client's advertised limit. Without this default, a
// single malicious connection could open many in-flight streams each
// holding a server goroutine + frame-buffer headroom, driving the
// process toward goroutine exhaustion.
func TestResolveGRPCMaxConcurrentStreamsDefaultIs100(t *testing.T) {
	o := options{}
	got := o.resolveGRPCMaxConcurrentStreams()
	if got != 100 {
		t.Errorf("default resolveGRPCMaxConcurrentStreams = %d, want 100", got)
	}
}

// TestResolveGRPCMaxConcurrentStreamsExplicit verifies that an explicit
// WithGRPCMaxConcurrentStreams option wins over env vars.
func TestResolveGRPCMaxConcurrentStreamsExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_CONCURRENT_STREAMS", "99")
	o := options{GRPCMaxConcurrentStreams: 32}
	got := o.resolveGRPCMaxConcurrentStreams()
	if got != 32 {
		t.Errorf("explicit resolveGRPCMaxConcurrentStreams = %d, want 32", got)
	}
}

// TestResolveGRPCMaxConcurrentStreamsEnv verifies that the env var is
// picked up when the explicit option is unset.
func TestResolveGRPCMaxConcurrentStreamsEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_CONCURRENT_STREAMS", "64")
	o := options{}
	got := o.resolveGRPCMaxConcurrentStreams()
	if got != 64 {
		t.Errorf("env resolveGRPCMaxConcurrentStreams = %d, want 64", got)
	}
}

// TestResolveGRPCMaxConcurrentStreamsNegativeDisables verifies that a
// negative value disables the cap (returns 0 — http2.Server then
// applies its own 100 default). Not recommended — re-opens the
// per-connection goroutine-exhaustion vector.
func TestResolveGRPCMaxConcurrentStreamsNegativeDisables(t *testing.T) {
	o := options{GRPCMaxConcurrentStreams: -1}
	got := o.resolveGRPCMaxConcurrentStreams()
	if got != -1 {
		t.Errorf("negative resolveGRPCMaxConcurrentStreams = %d, want -1", got)
	}
}

// TestResolveGRPCWriteByteTimeoutDefaultIsZero verifies that a zero
// options struct resolves to 0 (disabled) — the legacy behavior that
// relies on IdleTimeout and TCP write deadlines to eventually fail.
// Operators opt in by setting a positive duration.
func TestResolveGRPCWriteByteTimeoutDefaultIsZero(t *testing.T) {
	o := options{}
	got := o.resolveGRPCWriteByteTimeout()
	if got != 0 {
		t.Errorf("default resolveGRPCWriteByteTimeout = %v, want 0", got)
	}
}

// TestResolveGRPCWriteByteTimeoutExplicit verifies that an explicit
// WithGRPCWriteByteTimeout option wins over env vars.
func TestResolveGRPCWriteByteTimeoutExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_WRITE_BYTE_TIMEOUT", "99s")
	o := options{GRPCWriteByteTimeout: 15 * time.Second}
	got := o.resolveGRPCWriteByteTimeout()
	if got != 15*time.Second {
		t.Errorf("explicit resolveGRPCWriteByteTimeout = %v, want 15s", got)
	}
}

// TestResolveGRPCWriteByteTimeoutEnv verifies that the env var is
// picked up when the explicit option is unset.
func TestResolveGRPCWriteByteTimeoutEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_WRITE_BYTE_TIMEOUT", "30s")
	o := options{}
	got := o.resolveGRPCWriteByteTimeout()
	if got != 30*time.Second {
		t.Errorf("env resolveGRPCWriteByteTimeout = %v, want 30s", got)
	}
}

// TestResolveGRPCMaxHeaderBytesDefault verifies that a zero options
// struct resolves to 1 MiB — the header-bomb defense default. Without
// this cap, a peer can exploit HPACK compression to decompress a 4 KB
// frame into 1 GiB of decoded header data, driving the process into
// OOM before the handler runs. Operators who don't tune the limit get
// a sane default matching Go's net/http DefaultMaxHeaderBytes and
// Envoy's max_request_headers_kb.
func TestResolveGRPCMaxHeaderBytesDefault(t *testing.T) {
	o := options{}
	got := o.resolveGRPCMaxHeaderBytes()
	if got != 1<<20 {
		t.Errorf("default resolveGRPCMaxHeaderBytes = %d, want %d (1 MiB)", got, 1<<20)
	}
}

// TestResolveGRPCMaxHeaderBytesExplicit verifies that an explicit
// WithGRPCMaxHeaderBytes option wins over env vars and the default.
func TestResolveGRPCMaxHeaderBytesExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_HEADER_BYTES", "524288")
	o := options{GRPCMaxHeaderBytes: 64 * 1024}
	got := o.resolveGRPCMaxHeaderBytes()
	if got != 64*1024 {
		t.Errorf("explicit resolveGRPCMaxHeaderBytes = %d, want %d", got, 64*1024)
	}
}

// TestResolveGRPCMaxHeaderBytesEnv verifies that the env var is
// picked up when the explicit option is unset.
func TestResolveGRPCMaxHeaderBytesEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_HEADER_BYTES", "524288")
	o := options{}
	got := o.resolveGRPCMaxHeaderBytes()
	if got != 524288 {
		t.Errorf("env resolveGRPCMaxHeaderBytes = %d, want 524288", got)
	}
}

// TestResolveGRPCMaxHeaderBytesNegativeDisablesCap verifies that a
// negative value is propagated as-is to grpcSrv.MaxHeaderBytes, where
// grpc.Server.maxHeaderBytes() translates it to 0 (disabled). This
// matches the resolveGRPCMaxConcurrentStreams pattern: options-layer
// pass-through, grpc.Server-layer normalization.
func TestResolveGRPCMaxHeaderBytesNegativeDisablesCap(t *testing.T) {
	o := options{GRPCMaxHeaderBytes: -1}
	got := o.resolveGRPCMaxHeaderBytes()
	if got != -1 {
		t.Errorf("negative resolveGRPCMaxHeaderBytes = %d, want -1 (pass-through)", got)
	}
}

// TestResolveGRPCMaxReadFrameSizeDefault verifies that a zero options
// struct resolves to 1 MiB — the frame-bomb defense default. Without
// this cap, a peer sending a 16 MiB DATA frame forces the server to
// allocate a buffer that size before the handler runs; stacked across
// MaxConcurrentStreams connections, a single malicious peer exhausts
// memory before any handler executes. The default matches Go's
// http2.Server default and grpc-go's DefaultMaxReadFrameSize.
func TestResolveGRPCMaxReadFrameSizeDefault(t *testing.T) {
	o := options{}
	got := o.resolveGRPCMaxReadFrameSize()
	if got != 1<<20 {
		t.Errorf("default resolveGRPCMaxReadFrameSize = %d, want %d (1 MiB)", got, 1<<20)
	}
}

// TestResolveGRPCMaxReadFrameSizeExplicit verifies that an explicit
// WithGRPCMaxReadFrameSize option wins over env vars and the default.
func TestResolveGRPCMaxReadFrameSizeExplicit(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_READ_FRAME_SIZE", "524288")
	o := options{GRPCMaxReadFrameSize: 256 * 1024}
	got := o.resolveGRPCMaxReadFrameSize()
	if got != 256*1024 {
		t.Errorf("explicit resolveGRPCMaxReadFrameSize = %d, want %d", got, 256*1024)
	}
}

// TestResolveGRPCMaxReadFrameSizeEnv verifies that the env var is
// picked up when the explicit option is unset.
func TestResolveGRPCMaxReadFrameSizeEnv(t *testing.T) {
	t.Setenv("GONACOS_GRPC_MAX_READ_FRAME_SIZE", "524288")
	o := options{}
	got := o.resolveGRPCMaxReadFrameSize()
	if got != 524288 {
		t.Errorf("env resolveGRPCMaxReadFrameSize = %d, want 524288", got)
	}
}

// TestResolveGRPCMaxReadFrameSizeNegativeDisablesCap verifies that a
// negative value is propagated as-is to grpcSrv.MaxReadFrameSize, where
// grpc.Server.maxReadFrameSize() translates it to 0 (disabled). Mirrors
// the resolveGRPCMaxHeaderBytes pattern: options-layer pass-through,
// grpc.Server-layer normalization.
func TestResolveGRPCMaxReadFrameSizeNegativeDisablesCap(t *testing.T) {
	o := options{GRPCMaxReadFrameSize: -1}
	got := o.resolveGRPCMaxReadFrameSize()
	if got != -1 {
		t.Errorf("negative resolveGRPCMaxReadFrameSize = %d, want -1 (pass-through)", got)
	}
}
