package server

import (
	"net"
	"sync"
	"sync/atomic"
)

// maxConnsListener wraps a net.Listener and caps the number of concurrent
// connections accepted. When the cap is reached, new connections are
// immediately closed (the peer sees a connection reset) rather than queued
// — this is deliberate: a queued connection still holds a file descriptor
// on the server, defeating the purpose of the cap. Rejecting the Accept
// outright frees the descriptor.
//
// Use cases:
//   - Capping HTTP/gRPC connections so a connection-flood attack cannot
//     exhaust the process's file descriptor limit.
//   - Bounding the goroutine count (each connection spawns at least one
//     read goroutine in net/http).
//
// The cap is on connections, not requests — a single connection can carry
// many requests (HTTP keep-alive, HTTP/2 multiplexing). Pair with the
// per-IP rate limiter for request-level protection.
type maxConnsListener struct {
	net.Listener
	max     int32
	current atomic.Int32
}

// newMaxConnsListener wraps ln with a concurrent-connection cap. A max of
// 0 or negative disables the cap (returns ln unchanged). The wrapper is
// transparent to the caller — Accept returns net.Conn as usual, and the
// connection is tracked until Close is called on the returned conn.
func newMaxConnsListener(ln net.Listener, max int) net.Listener {
	if max <= 0 {
		return ln
	}
	return &maxConnsListener{Listener: ln, max: int32(max)}
}

// Accept blocks until the next connection is available and the current
// count is below the cap. When the cap is reached, incoming connections
// are drained and immediately closed (the peer sees a reset).
func (l *maxConnsListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		// Atomically claim a slot. If the cap is exceeded, close the
		// connection and try the next one — we don't block on a semaphore
		// because the kernel has already accepted the SYN and handed us
		// the connection; holding it in a queue would defeat the cap.
		now := l.current.Add(1)
		if now > l.max {
			_ = c.Close()
			l.current.Add(-1)
			continue
		}
		return &trackedConn{Conn: c, listener: l}, nil
	}
}

// release decrements the counter. Called by trackedConn.Close.
func (l *maxConnsListener) release() {
	l.current.Add(-1)
}

// CurrentConns returns the current number of tracked connections. Useful
// for metrics — the resource collector can expose this as a gauge.
func (l *maxConnsListener) CurrentConns() int32 {
	return l.current.Load()
}

// trackedConn wraps a net.Conn and decrements the listener's counter when
// closed. Double-close is safe: the underlying conn's Close is idempotent
// (net.Conn implementations are safe to call multiple times), and the
// listener counter is only decremented once via sync.Once.
type trackedConn struct {
	net.Conn
	listener *maxConnsListener
	once     sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(c.listener.release)
	return c.Conn.Close()
}
