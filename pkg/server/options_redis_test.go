package server

import (
	"testing"
	"time"
)

// TestResolveRedisPoolDefaults verifies the safe-default behavior of the
// Redis pool resolvers. The defaults are tuned for a gonacos process
// serving hundreds of concurrent SDK clients — PoolSize 50, MinIdleConns
// 5, DialTimeout 5s, ReadTimeout 3s, WriteTimeout 3s, PoolTimeout 4s,
// ConnMaxLifetime 30m. Changing a default here silently changes every
// production deployment, so each value is asserted.
func TestResolveRedisPoolDefaults(t *testing.T) {
	o := options{}

	if got := o.resolveRedisPoolSize(); got != 50 {
		t.Errorf("default resolveRedisPoolSize = %d, want 50", got)
	}
	if got := o.resolveRedisMinIdleConns(); got != 5 {
		t.Errorf("default resolveRedisMinIdleConns = %d, want 5", got)
	}
	if got := o.resolveRedisDialTimeout(); got != 5*time.Second {
		t.Errorf("default resolveRedisDialTimeout = %v, want 5s", got)
	}
	if got := o.resolveRedisReadTimeout(); got != 3*time.Second {
		t.Errorf("default resolveRedisReadTimeout = %v, want 3s", got)
	}
	if got := o.resolveRedisWriteTimeout(); got != 3*time.Second {
		t.Errorf("default resolveRedisWriteTimeout = %v, want 3s", got)
	}
	if got := o.resolveRedisPoolTimeout(); got != 4*time.Second {
		t.Errorf("default resolveRedisPoolTimeout = %v, want 4s", got)
	}
	if got := o.resolveRedisMaxConnAge(); got != 30*time.Minute {
		t.Errorf("default resolveRedisMaxConnAge = %v, want 30m", got)
	}
}

// TestResolveRedisPoolFromOptions verifies that explicit Option values
// override the defaults. This is the API embedders use to tune the pool
// for their specific workload.
func TestResolveRedisPoolFromOptions(t *testing.T) {
	o := options{
		RedisPoolSize:     100,
		RedisMinIdleConns: 10,
		RedisDialTimeout:  10 * time.Second,
		RedisReadTimeout:  6 * time.Second,
		RedisWriteTimeout: 6 * time.Second,
		RedisPoolTimeout:  8 * time.Second,
		RedisMaxConnAge:   time.Hour,
	}
	if got := o.resolveRedisPoolSize(); got != 100 {
		t.Errorf("resolveRedisPoolSize = %d, want 100", got)
	}
	if got := o.resolveRedisMinIdleConns(); got != 10 {
		t.Errorf("resolveRedisMinIdleConns = %d, want 10", got)
	}
	if got := o.resolveRedisDialTimeout(); got != 10*time.Second {
		t.Errorf("resolveRedisDialTimeout = %v, want 10s", got)
	}
	if got := o.resolveRedisReadTimeout(); got != 6*time.Second {
		t.Errorf("resolveRedisReadTimeout = %v, want 6s", got)
	}
	if got := o.resolveRedisWriteTimeout(); got != 6*time.Second {
		t.Errorf("resolveRedisWriteTimeout = %v, want 6s", got)
	}
	if got := o.resolveRedisPoolTimeout(); got != 8*time.Second {
		t.Errorf("resolveRedisPoolTimeout = %v, want 8s", got)
	}
	if got := o.resolveRedisMaxConnAge(); got != time.Hour {
		t.Errorf("resolveRedisMaxConnAge = %v, want 1h", got)
	}
}

// TestResolveRedisPoolFromEnv verifies that GONACOS_REDIS_* env vars are
// honored when no explicit option is set. Operators use these to tune
// the pool without code changes.
func TestResolveRedisPoolFromEnv(t *testing.T) {
	t.Setenv("GONACOS_REDIS_POOL_SIZE", "200")
	t.Setenv("GONACOS_REDIS_MIN_IDLE_CONNS", "20")
	t.Setenv("GONACOS_REDIS_DIAL_TIMEOUT", "8s")
	t.Setenv("GONACOS_REDIS_READ_TIMEOUT", "4s")
	t.Setenv("GONACOS_REDIS_WRITE_TIMEOUT", "4s")
	t.Setenv("GONACOS_REDIS_POOL_TIMEOUT", "6s")
	t.Setenv("GONACOS_REDIS_MAX_CONN_AGE", "45m")

	o := options{}
	if got := o.resolveRedisPoolSize(); got != 200 {
		t.Errorf("env resolveRedisPoolSize = %d, want 200", got)
	}
	if got := o.resolveRedisMinIdleConns(); got != 20 {
		t.Errorf("env resolveRedisMinIdleConns = %d, want 20", got)
	}
	if got := o.resolveRedisDialTimeout(); got != 8*time.Second {
		t.Errorf("env resolveRedisDialTimeout = %v, want 8s", got)
	}
	if got := o.resolveRedisReadTimeout(); got != 4*time.Second {
		t.Errorf("env resolveRedisReadTimeout = %v, want 4s", got)
	}
	if got := o.resolveRedisWriteTimeout(); got != 4*time.Second {
		t.Errorf("env resolveRedisWriteTimeout = %v, want 4s", got)
	}
	if got := o.resolveRedisPoolTimeout(); got != 6*time.Second {
		t.Errorf("env resolveRedisPoolTimeout = %v, want 6s", got)
	}
	if got := o.resolveRedisMaxConnAge(); got != 45*time.Minute {
		t.Errorf("env resolveRedisMaxConnAge = %v, want 45m", got)
	}
}

// TestResolveRedisPoolOptionOverridesEnv verifies that explicit options
// take precedence over env vars. This is the standard gonacos precedence:
// Option > env var > default.
func TestResolveRedisPoolOptionOverridesEnv(t *testing.T) {
	t.Setenv("GONACOS_REDIS_POOL_SIZE", "200")
	o := options{RedisPoolSize: 75}
	if got := o.resolveRedisPoolSize(); got != 75 {
		t.Errorf("option should override env: got %d, want 75", got)
	}
}

// TestResolveRedisPoolInvalidEnvFallsBack verifies that an invalid env
// var value falls back to the default rather than panicking or producing
// a zero pool. A typo in GONACOS_REDIS_POOL_SIZE must not break the
// server.
func TestResolveRedisPoolInvalidEnvFallsBack(t *testing.T) {
	t.Setenv("GONACOS_REDIS_POOL_SIZE", "not-a-number")
	o := options{}
	if got := o.resolveRedisPoolSize(); got != 50 {
		t.Errorf("invalid env: got %d, want default 50", got)
	}

	t.Setenv("GONACOS_REDIS_DIAL_TIMEOUT", "garbage")
	if got := o.resolveRedisDialTimeout(); got != 5*time.Second {
		t.Errorf("invalid env: got %v, want default 5s", got)
	}
}
