package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/redis/go-redis/v9"
)

// TestRedisMetricsHookProcess verifies the ProcessHook increments the
// per-command counter and observes the latency histogram for each processed
// command. Both the counter and the histogram's count must be 1 after a
// single command flows through.
//
// go-redis's redis.Cmd implements Cmder and computes Name() from the first
// arg in Args() — so NewCmd(ctx, "get", "key") reports Name()=="get". Use
// that instead of a hand-rolled fakeCmder (Cmder has unexported methods we
// can't satisfy from outside the package).
func TestRedisMetricsHookProcess(t *testing.T) {
	registry := observability.NewRegistry()
	hook := newRedisMetricsHook(registry)

	called := false
	inner := redis.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error {
		called = true
		return nil
	})
	wrapped := hook.ProcessHook(inner)

	cmd := redis.NewCmd(context.Background(), "get", "key")
	if err := wrapped(context.Background(), cmd); err != nil {
		t.Fatalf("wrapped process returned error: %v", err)
	}
	if !called {
		t.Fatal("inner process hook not called")
	}

	cmdCounter := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "get"}).Value()
	if cmdCounter != 1 {
		t.Errorf("gonacos_redis_commands_total{command=get} = %d, want 1", cmdCounter)
	}

	// The histogram's count is not exposed via the public API; read it
	// from the Prometheus text exposition instead.
	metricsOutput := readMetrics(t, registry)
	if !strings.Contains(metricsOutput, `gonacos_redis_command_duration_seconds_count{command="get"} 1`) {
		t.Errorf("expected histogram count=1 for command=get, got metrics:\n%s", metricsOutput)
	}
}

// TestRedisMetricsHookProcessWithFailure verifies the hook records metrics
// even when the inner command returns an error. The error must propagate
// unchanged — a metrics hook must not swallow command errors.
func TestRedisMetricsHookProcessWithFailure(t *testing.T) {
	registry := observability.NewRegistry()
	hook := newRedisMetricsHook(registry)

	wantErr := errors.New("redis: connection refused")
	inner := redis.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error {
		return wantErr
	})
	wrapped := hook.ProcessHook(inner)

	cmd := redis.NewCmd(context.Background(), "set", "key", "val")
	err := wrapped(context.Background(), cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("wrapped process error = %v, want %v", err, wantErr)
	}

	cmdCounter := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "set"}).Value()
	if cmdCounter != 1 {
		t.Errorf("counter should increment on error: got %d, want 1", cmdCounter)
	}
}

// TestRedisMetricsHookDial verifies the dial hook counts success and failure
// outcomes separately. A spike in result="failure" is the early-warning
// signal for Redis connectivity loss — this test asserts the label values
// operators will alert on.
func TestRedisMetricsHookDial(t *testing.T) {
	registry := observability.NewRegistry()
	hook := newRedisMetricsHook(registry)

	// Successful dial.
	successInner := redis.DialHook(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	})
	wrappedSuccess := hook.DialHook(successInner)
	if _, err := wrappedSuccess(context.Background(), "tcp", "127.0.0.1:6379"); err != nil {
		t.Fatalf("success dial returned error: %v", err)
	}

	// Failed dial.
	failureInner := redis.DialHook(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	wrappedFailure := hook.DialHook(failureInner)
	if _, err := wrappedFailure(context.Background(), "tcp", "127.0.0.1:6379"); err == nil {
		t.Fatal("failure dial should return error")
	}

	successCount := registry.Counter("gonacos_redis_dial_total", map[string]string{"result": "success"}).Value()
	failureCount := registry.Counter("gonacos_redis_dial_total", map[string]string{"result": "failure"}).Value()
	if successCount != 1 {
		t.Errorf("dial success counter = %d, want 1", successCount)
	}
	if failureCount != 1 {
		t.Errorf("dial failure counter = %d, want 1", failureCount)
	}
}

// TestRedisMetricsHookPipeline verifies the pipeline hook increments the
// counter for each command in the batch (not just once per batch). A
// pipeline of [get, set, get, del] must produce get=2, set=1, del=1.
func TestRedisMetricsHookPipeline(t *testing.T) {
	registry := observability.NewRegistry()
	hook := newRedisMetricsHook(registry)

	inner := redis.ProcessPipelineHook(func(ctx context.Context, cmds []redis.Cmder) error {
		return nil
	})
	wrapped := hook.ProcessPipelineHook(inner)

	ctx := context.Background()
	cmds := []redis.Cmder{
		redis.NewCmd(ctx, "get", "k1"),
		redis.NewCmd(ctx, "set", "k2", "v"),
		redis.NewCmd(ctx, "get", "k3"), // duplicate name — same counter should accumulate
		redis.NewCmd(ctx, "del", "k4"),
	}
	if err := wrapped(context.Background(), cmds); err != nil {
		t.Fatalf("pipeline returned error: %v", err)
	}

	getCount := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "get"}).Value()
	setCount := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "set"}).Value()
	delCount := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "del"}).Value()
	if getCount != 2 {
		t.Errorf("get counter = %d, want 2 (two get commands in pipeline)", getCount)
	}
	if setCount != 1 {
		t.Errorf("set counter = %d, want 1", setCount)
	}
	if delCount != 1 {
		t.Errorf("del counter = %d, want 1", delCount)
	}
}

// TestRedisMetricsHookNilRegistryNoOp verifies the hook degrades to a pure
// pass-through when the registry is nil. This is the safety contract: a
// caller that constructs a hook without a registry (e.g. in test paths)
// must still get correct Redis behavior, just without metrics.
func TestRedisMetricsHookNilRegistryNoOp(t *testing.T) {
	hook := newRedisMetricsHook(nil)

	called := false
	inner := redis.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error {
		called = true
		return nil
	})
	wrapped := hook.ProcessHook(inner)

	cmd := redis.NewCmd(context.Background(), "get", "key")
	if err := wrapped(context.Background(), cmd); err != nil {
		t.Fatalf("nil-registry hook returned error: %v", err)
	}
	if !called {
		t.Fatal("inner process hook not called through nil-registry hook")
	}

	// Dial too.
	dialCalled := false
	dialInner := redis.DialHook(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCalled = true
		return nil, nil
	})
	wrappedDial := hook.DialHook(dialInner)
	if _, err := wrappedDial(context.Background(), "tcp", "127.0.0.1:6379"); err != nil {
		t.Fatalf("nil-registry dial returned error: %v", err)
	}
	if !dialCalled {
		t.Fatal("inner dial hook not called through nil-registry hook")
	}
}

// TestRedisMetricsHookCommandNameNormalization verifies the command label
// is lowercased. The normalization layer is defensive — go-redis already
// lowercases, but a future client version or custom Cmder could regress.
// Empty command names (from internal/cmd Cmders that don't carry args)
// must map to "unknown" rather than producing a malformed label.
func TestRedisMetricsHookCommandNameNormalization(t *testing.T) {
	registry := observability.NewRegistry()
	hook := newRedisMetricsHook(registry)

	inner := redis.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error {
		return nil
	})
	wrapped := hook.ProcessHook(inner)

	// go-redis normalizes "GET" -> "get" internally, so the label is "get".
	_ = wrapped(context.Background(), redis.NewCmd(context.Background(), "GET", "k"))
	// An args-less Cmd reports Name() == "" — the hook must map this to
	// "unknown" so it doesn't produce a {command=""} label.
	_ = wrapped(context.Background(), redis.NewCmd(context.Background()))

	getCount := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "get"}).Value()
	unknownCount := registry.Counter("gonacos_redis_commands_total", map[string]string{"command": "unknown"}).Value()
	if getCount != 1 {
		t.Errorf("normalized get counter = %d, want 1", getCount)
	}
	if unknownCount != 1 {
		t.Errorf("unknown counter for empty name = %d, want 1", unknownCount)
	}
}

// readMetrics serializes the registry to Prometheus text format so tests
// can assert on histogram count/sum fields (which aren't exposed via the
// public API).
func readMetrics(t *testing.T, r *observability.Registry) string {
	t.Helper()
	var buf stringBuffer
	r.WritePrometheus(&buf)
	return buf.String()
}

type stringBuffer struct {
	data []byte
}

func (b *stringBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *stringBuffer) String() string { return string(b.data) }
