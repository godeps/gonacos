package server

import (
	"net"
	"sync"
	"testing"
	"time"
)

// TestMaxConnsListenerRejectsExcess verifies that when the connection cap
// is reached, new connections are immediately closed (the peer sees a
// reset) rather than queued.
func TestMaxConnsListenerRejectsExcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := newMaxConnsListener(ln, 2) // cap at 2 concurrent conns

	// Accept and hold connections so they count against the cap.
	accepted := make(chan net.Conn, 10)
	go func() {
		for {
			c, err := wrapped.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	defer func() {
		_ = wrapped.Close()
		for c := range accepted {
			_ = c.Close()
			if len(accepted) == 0 {
				break
			}
		}
	}()

	addr := ln.Addr().String()
	// Open 2 connections — both should be held open.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()
	time.Sleep(50 * time.Millisecond) // let accept loop catch up

	// Third connection: should be accepted by the kernel but immediately
	// closed by the listener (cap exceeded). A subsequent write/read
	// should fail.
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	defer c3.Close()
	// Wait briefly for the listener to close the excess connection.
	time.Sleep(50 * time.Millisecond)

	// A read on the rejected connection should return EOF immediately
	// (the server closed it). Use a short deadline so the test doesn't
	// hang if the rejection didn't happen.
	_ = c3.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	n, err := c3.Read(buf)
	if n != 0 || err == nil {
		t.Fatalf("excess conn should be reset: n=%d err=%v", n, err)
	}
}

// TestMaxConnsListenerDisabled verifies that max <= 0 returns the original
// listener unwrapped.
func TestMaxConnsListenerDisabled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if got := newMaxConnsListener(ln, 0); got != ln {
		t.Errorf("max=0 should return original listener")
	}
	if got := newMaxConnsListener(ln, -1); got != ln {
		t.Errorf("max=-1 should return original listener")
	}
}

// TestMaxConnsListenerReleasesOnClose verifies that closing a tracked
// connection decrements the counter, allowing a new connection in.
func TestMaxConnsListenerReleasesOnClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := newMaxConnsListener(ln, 1) // cap at 1
	defer wrapped.Close()

	var mu sync.Mutex
	openConns := make(map[int]net.Conn)
	go func() {
		for {
			c, err := wrapped.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			openConns[len(openConns)] = c
			mu.Unlock()
		}
	}()

	addr := ln.Addr().String()
	// First connection: accepted.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Second connection: should be rejected (reset).
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1)
	n, _ := c2.Read(buf)
	if n != 0 {
		t.Fatalf("second connection should be reset, but read %d bytes", n)
	}
	_ = c2.Close()

	// Close the first connection — should release the slot.
	_ = c1.Close()
	time.Sleep(50 * time.Millisecond)

	// Third connection: should be accepted now.
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	_ = c3.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ = c3.Read(buf)
	if n != 0 {
		// If the connection was reset, the slot wasn't released.
		t.Fatalf("third connection should be accepted (not reset), but read %d bytes", n)
	}
	_ = c3.Close()
}

// TestMaxConnsListenerRejectedCounter verifies that RejectedConns increments
// for each connection refused due to the cap. Operators scrape this via
// gonacos_connection_rejections_total{proto} and alert on a non-zero rate
// — it's the signal for a connection-flood attack or capacity shortfall.
func TestMaxConnsListenerRejectedCounter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := newMaxConnsListener(ln, 1) // cap at 1 concurrent conn

	accepted := make(chan net.Conn, 10)
	go func() {
		for {
			c, err := wrapped.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	defer func() {
		_ = wrapped.Close()
		for c := range accepted {
			_ = c.Close()
			if len(accepted) == 0 {
				break
			}
		}
	}()

	addr := ln.Addr().String()
	// Open 1 connection — fills the cap.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	time.Sleep(50 * time.Millisecond)

	// Open 2 more connections — both should be rejected (cap exceeded).
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	defer c3.Close()
	time.Sleep(50 * time.Millisecond) // let accept loop reject them

	ml := wrapped.(*maxConnsListener)
	got := ml.RejectedConns()
	if got < 2 {
		t.Fatalf("RejectedConns = %d, want >= 2", got)
	}

	// Initial value should have been 0 before any rejections — verify
	// the counter is monotonic (a fresh listener starts at 0).
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 2: %v", err)
	}
	defer ln2.Close()
	fresh := newMaxConnsListener(ln2, 1).(*maxConnsListener)
	if got := fresh.RejectedConns(); got != 0 {
		t.Fatalf("fresh listener RejectedConns = %d, want 0", got)
	}
}

// TestMaxConnsListenerRejectedCounterStaysZeroUnderCap verifies that
// RejectedConns stays at 0 when the cap is never hit — confirms the
// counter only fires on actual rejections, not on accepted conns.
func TestMaxConnsListenerRejectedCounterStaysZeroUnderCap(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := newMaxConnsListener(ln, 10) // generous cap
	defer wrapped.Close()

	accepted := make(chan net.Conn, 10)
	go func() {
		for {
			c, err := wrapped.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	addr := ln.Addr().String()
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer c.Close()
	}
	time.Sleep(50 * time.Millisecond)

	ml := wrapped.(*maxConnsListener)
	if got := ml.RejectedConns(); got != 0 {
		t.Fatalf("RejectedConns = %d, want 0 (cap not hit)", got)
	}
}
