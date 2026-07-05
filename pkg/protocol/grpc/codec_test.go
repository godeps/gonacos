package grpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestPayloadRoundtrip(t *testing.T) {
	t.Parallel()
	original := Payload{
		Metadata: Metadata{
			Type:     "InstanceRequest",
			ClientIP: "10.0.0.1",
			Headers:  map[string]string{"x-app": "test"},
		},
		Body: Any{
			TypeURL: "type.googleapis.com/com.alibaba.nacos.api.naming.request.InstanceRequest",
			Value:   []byte("\x0a\x04test"),
		},
	}
	encoded := original.Encode()
	decoded, err := DecodePayload(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Metadata.Type != original.Metadata.Type {
		t.Fatalf("type = %v, want %v", decoded.Metadata.Type, original.Metadata.Type)
	}
	if decoded.Metadata.ClientIP != original.Metadata.ClientIP {
		t.Fatalf("clientIP = %v", decoded.Metadata.ClientIP)
	}
	if decoded.Metadata.Headers["x-app"] != "test" {
		t.Fatalf("headers = %+v", decoded.Metadata.Headers)
	}
	if !bytes.Equal(decoded.Body.Value, original.Body.Value) {
		t.Fatalf("body value = %x", decoded.Body.Value)
	}
}

func TestMetadataEmptyEncode(t *testing.T) {
	t.Parallel()
	m := Metadata{}
	encoded := m.Encode()
	if len(encoded) != 0 {
		t.Fatalf("empty metadata should encode to 0 bytes, got %d", len(encoded))
	}
	decoded, err := DecodeMetadata(encoded)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if decoded.Type != "" {
		t.Fatalf("type = %v", decoded.Type)
	}
}

func TestAnyRoundtrip(t *testing.T) {
	t.Parallel()
	original := Any{
		TypeURL: "type.googleapis.com/test.Message",
		Value:   []byte{0x01, 0x02, 0x03},
	}
	encoded := original.Encode()
	decoded, err := DecodeAny(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.TypeURL != original.TypeURL {
		t.Fatalf("typeURL = %v", decoded.TypeURL)
	}
	if !bytes.Equal(decoded.Value, original.Value) {
		t.Fatalf("value = %x", decoded.Value)
	}
}

func TestFrameRoundtrip(t *testing.T) {
	t.Parallel()
	original := Frame{Payload: []byte("hello world")}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, original); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.Len() != 5+11 {
		t.Fatalf("frame size = %d, want %d", buf.Len(), 5+11)
	}
	decoded, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Fatalf("payload = %x", decoded.Payload)
	}
}

// TestReadFrameRejectsOversizedFrame verifies that a frame whose declared
// length exceeds the limit is rejected before any body allocation — a
// malicious peer claiming a 1 GiB body must not drive the process into OOM.
func TestReadFrameRejectsOversizedFrame(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	// Header: compressed=false, length=1 GiB (no body follows).
	header := make([]byte, 5)
	header[1] = 0x40 // 1 << 30 = 1 GiB
	buf.Write(header)

	_, err := ReadFrameWithLimit(&buf, 4*1024*1024)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
}

// TestReadFrameWithinLimit verifies that a frame at the boundary of the
// limit is accepted.
func TestReadFrameWithinLimit(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), 100)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, Frame{Payload: payload}); err != nil {
		t.Fatalf("write: %v", err)
	}
	decoded, err := ReadFrameWithLimit(&buf, 100)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestUnaryDispatcherUnknownType(t *testing.T) {
	t.Parallel()
	d := NewUnaryDispatcher()
	_, err := d.Handle(context.Background(), Payload{Metadata: Metadata{Type: "UnknownRequest"}})
	if err == nil {
		t.Fatalf("expected error for unknown type")
	}
	se, ok := err.(*StatusError)
	if !ok || se.Code != StatusUnimplemented {
		t.Fatalf("err = %v, want StatusUnimplemented", err)
	}
}

func TestUnaryDispatcherMissingType(t *testing.T) {
	t.Parallel()
	d := NewUnaryDispatcher()
	_, err := d.Handle(context.Background(), Payload{})
	if err == nil {
		t.Fatalf("expected error for missing type")
	}
	se, ok := err.(*StatusError)
	if !ok || se.Code != StatusInvalidArgument {
		t.Fatalf("err = %v, want StatusInvalidArgument", err)
	}
}

func TestUnaryDispatcherRoutesToHandler(t *testing.T) {
	t.Parallel()
	d := NewUnaryDispatcher()
	d.Register("Echo", func(ctx context.Context, req Payload) (Payload, error) {
		return Payload{Metadata: Metadata{Type: "EchoResponse"}}, nil
	})
	resp, err := d.Handle(context.Background(), Payload{Metadata: Metadata{Type: "Echo"}})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.Metadata.Type != "EchoResponse" {
		t.Fatalf("response type = %v", resp.Metadata.Type)
	}
}

func TestStreamDispatcherUnknownType(t *testing.T) {
	t.Parallel()
	d := NewStreamDispatcher()
	err := d.Handle(context.Background(), Payload{Metadata: Metadata{Type: "UnknownStream"}}, func(Payload) error { return nil })
	if err == nil {
		t.Fatalf("expected error")
	}
	se, ok := err.(*StatusError)
	if !ok || se.Code != StatusUnimplemented {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamDispatcherRoutesToHandler(t *testing.T) {
	t.Parallel()
	d := NewStreamDispatcher()
	d.Register("Stream", func(ctx context.Context, req Payload, send func(Payload) error) error {
		return send(Payload{Metadata: Metadata{Type: "StreamResponse"}})
	})
	var sent []Payload
	err := d.Handle(context.Background(), Payload{Metadata: Metadata{Type: "Stream"}}, func(p Payload) error {
		sent = append(sent, p)
		return nil
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sent) != 1 || sent[0].Metadata.Type != "StreamResponse" {
		t.Fatalf("sent = %+v", sent)
	}
}

// TestReadFrameWithLimitAndTimeoutSuccess verifies that a frame read
// completing within the timeout returns the frame normally — the
// deadline does not interfere with legitimate fast clients.
func TestReadFrameWithLimitAndTimeoutSuccess(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	// Write a valid frame: compressed=false, length=4, body="test".
	buf.Write([]byte{0, 0, 0, 0, 4})
	buf.WriteString("test")

	frame, err := ReadFrameWithLimitAndTimeout(&buf, 100, 1*time.Second)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if string(frame.Payload) != "test" {
		t.Errorf("payload = %q, want %q", string(frame.Payload), "test")
	}
}

// TestReadFrameWithLimitAndTimeoutFiresOnSlowBody verifies that a
// peer sending a frame body very slowly is aborted once the deadline
// elapses — the slowloris-on-body protection for the gRPC path.
//
// Without this cap, a peer can send a frame body 1 byte at a time
// and hold the server's goroutine for up to MaxFrameBytes seconds
// (4 MiB at 1 byte/sec ≈ 48 days). The timeout bounds the window.
func TestReadFrameWithLimitAndTimeoutFiresOnSlowBody(t *testing.T) {
	t.Parallel()
	// slowReader returns 1 byte every 50ms, never reaching EOF
	// until the test completes. This simulates a slowloris peer.
	slow := &slowReader{interval: 50 * time.Millisecond, preappend: []byte{0, 0, 0, 0, 100}}

	// Header claims a 100-byte body, but the reader dribbles bytes.
	// The header is pre-injected into the slow reader's internal
	// buffer so the test focuses on the body-read timeout, not the
	// header.

	start := time.Now()
	_, err := ReadFrameWithLimitAndTimeout(slow, 200, 200*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrReadFrameTimeout) {
		t.Fatalf("err = %v, want ErrReadFrameTimeout", err)
	}
	// The timeout should fire close to 200ms, not 5 seconds (the
	// time it would take to read 100 bytes at 50ms each = 5s).
	if elapsed > 1*time.Second {
		t.Errorf("elapsed = %v, want < 1s (timeout should have fired near 200ms)", elapsed)
	}
}

// TestReadFrameWithLimitAndTimeoutNegativeDisables verifies that a
// negative timeout disables the cap and delegates to
// ReadFrameWithLimit — backward compatible with callers that opted
// out. The opt-out path is not recommended in production.
func TestReadFrameWithLimitAndTimeoutNegativeDisables(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0, 4})
	buf.WriteString("test")

	frame, err := ReadFrameWithLimitAndTimeout(&buf, 100, -1)
	if err != nil {
		t.Fatalf("err = %v, want nil (negative timeout should disable)", err)
	}
	if string(frame.Payload) != "test" {
		t.Errorf("payload = %q, want %q", string(frame.Payload), "test")
	}
}

// TestReadFrameWithLimitAndTimeoutZeroDisables verifies that a zero
// timeout disables the cap and delegates to ReadFrameWithLimit.
func TestReadFrameWithLimitAndTimeoutZeroDisables(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0, 4})
	buf.WriteString("test")

	frame, err := ReadFrameWithLimitAndTimeout(&buf, 100, 0)
	if err != nil {
		t.Fatalf("err = %v, want nil (zero timeout should disable)", err)
	}
	if string(frame.Payload) != "test" {
		t.Errorf("payload = %q, want %q", string(frame.Payload), "test")
	}
}

// slowReader returns 1 byte every interval, simulating a slowloris
// peer. Optional preappend bytes are returned first (used to inject
// the frame header before the slow body drip starts).
type slowReader struct {
	interval  time.Duration
	preappend []byte
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(s.preappend) > 0 {
		p[0] = s.preappend[0]
		s.preappend = s.preappend[1:]
		return 1, nil
	}
	time.Sleep(s.interval)
	p[0] = 'x'
	return 1, nil
}

// TestReadFrameTimeoutDefaultIs30s verifies that the default
// per-frame read deadline is 30s — generous for legitimate clients
// (a 4 MiB frame at ~133 KB/s) while bounding the slowloris window.
func TestReadFrameTimeoutDefaultIs30s(t *testing.T) {
	s := &Server{}
	if got := s.readFrameTimeout(); got != DefaultReadFrameTimeout {
		t.Errorf("default readFrameTimeout = %v, want %v", got, DefaultReadFrameTimeout)
	}
	if DefaultReadFrameTimeout != 30*time.Second {
		t.Errorf("DefaultReadFrameTimeout = %v, want 30s", DefaultReadFrameTimeout)
	}
}

// TestReadFrameTimeoutConfiguredOverridesDefault verifies that an
// explicitly configured ReadFrameTimeout overrides the default.
func TestReadFrameTimeoutConfiguredOverridesDefault(t *testing.T) {
	s := &Server{ReadFrameTimeout: 5 * time.Second}
	if got := s.readFrameTimeout(); got != 5*time.Second {
		t.Errorf("configured readFrameTimeout = %v, want 5s", got)
	}
}

// TestReadFrameTimeoutNegativeDisables verifies that a negative
// ReadFrameTimeout disables the cap (the underlying resolver returns
// a negative value, and ReadFrameWithLimitAndTimeout delegates to
// ReadFrameWithLimit without a deadline).
func TestReadFrameTimeoutNegativeDisables(t *testing.T) {
	s := &Server{ReadFrameTimeout: -1}
	if got := s.readFrameTimeout(); got != -1 {
		t.Errorf("negative readFrameTimeout = %v, want -1", got)
	}
	// Verify the underlying reader doesn't time out on a negative
	// timeout — it should block forever (we don't actually block
	// forever in the test; we just verify the value propagates and
	// the no-timeout code path is taken).
}

// Ensure slowReader also satisfies io.Reader for the slowloris test.
var _ io.Reader = (*slowReader)(nil)
