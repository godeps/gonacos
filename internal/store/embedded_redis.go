package store

import (
	"fmt"
	"os"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// EmbeddedRedis runs an in-process miniredis server. It is used in standalone
// mode so operators get Redis-backed persistence without an external Redis
// dependency.
//
// miniredis is purely in-memory and does not provide its own disk persistence.
// Cross-restart durability is handled at the RedisPersistence layer, which
// writes the snapshot envelope to a disk file alongside the Redis key.
type EmbeddedRedis struct {
	mr   *miniredis.Miniredis
	addr string
}

// StartEmbedded starts an in-process miniredis server on an auto-assigned
// port. The server is empty; any prior state must be reloaded via
// RedisPersistence.Load (which repopulates the gonacos:snapshot key from the
// disk dump file).
func StartEmbedded() (*EmbeddedRedis, error) {
	mr := miniredis.NewMiniRedis()
	if err := mr.Start(); err != nil {
		return nil, fmt.Errorf("start embedded redis: %w", err)
	}
	return &EmbeddedRedis{
		mr:   mr,
		addr: mr.Addr(),
	}, nil
}

// Addr returns the "127.0.0.1:<port>" address of the embedded server.
func (e *EmbeddedRedis) Addr() string {
	return e.addr
}

// Client returns a redis.Client connected to the embedded server. The caller
// owns the returned client and is responsible for closing it.
func (e *EmbeddedRedis) Client() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: e.addr})
}

// Close stops the embedded server. It does NOT persist state; the caller
// should flush the snapshot to disk via RedisPersistence.Save first.
func (e *EmbeddedRedis) Close() error {
	e.mr.Close()
	return nil
}

// stat helper retained for callers that want to check dump file existence.
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
