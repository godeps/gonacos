// Package grpc implements a minimal gRPC-over-HTTP/2 server using only the Go
// standard library. It parses the gRPC length-prefixed frame format, decodes
// the Nacos Payload protobuf message, dispatches to registered handlers based
// on the Metadata.type field, and encodes the response.
//
// The protobuf codec is hand-rolled to keep the zero-dependency contract. It
// only needs to handle the Nacos Payload shape:
//
//	message Metadata { string type = 3; string clientIp = 8; map<string,string> headers = 7; }
//	message Payload  { Metadata metadata = 2; google.protobuf.Any body = 3; }
//
// where google.protobuf.Any is a length-delimited message with string type_url
// (field 1) and bytes value (field 2). The body bytes are opaque to the
// dispatcher; handlers that need to decode the body register a typed decoder.
package grpc

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// wireType constants for the protobuf wire format.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// ErrProto is returned when the protobuf stream is malformed.
var ErrProto = errors.New("protobuf: malformed input")

// DefaultMaxFrameBytes is the default cap on a single gRPC frame's payload
// length. Matches the standard gRPC client default of 4 MiB. A frame whose
// declared length exceeds this is rejected before any allocation, so a
// malicious peer cannot drive the server into OOM by claiming a 4 GiB body.
// Override per-server via [Server.MaxFrameBytes] (or the package-level
// [SetDefaultMaxFrameBytes] for the embedded default server).
const DefaultMaxFrameBytes = 4 * 1024 * 1024

// ErrFrameTooLarge is returned by ReadFrame when the peer declares a frame
// payload larger than the configured limit. The connection is in an
// unrecoverable state after this — the reader has not consumed the body — so
// the server closes it.
var ErrFrameTooLarge = errors.New("grpc: frame too large")

// ProtoReader wraps a byte slice for sequential protobuf field reads.
type ProtoReader struct {
	buf []byte
	pos int
}

// NewReader wraps the given bytes.
func NewReader(b []byte) *ProtoReader { return &ProtoReader{buf: b} }

// Done reports whether the reader has consumed all input.
func (r *ProtoReader) Done() bool { return r.pos >= len(r.buf) }

// ReadTag returns the next field number and wire type.
func (r *ProtoReader) ReadTag() (field int, wire int, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	return int(v >> 3), int(v & 7), nil
}

// ReadVarint decodes a base-128 varint.
func (r *ProtoReader) ReadVarint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if r.pos >= len(r.buf) {
			return 0, ErrProto
		}
		b := r.buf[r.pos]
		r.pos++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, ErrProto
}

// ReadBytes decodes a length-delimited byte slice.
func (r *ProtoReader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if r.pos+int(n) > len(r.buf) {
		return nil, ErrProto
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

// ReadString decodes a length-delimited string.
func (r *ProtoReader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Skip skips a field of the given wire type.
func (r *ProtoReader) Skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := r.ReadVarint()
		return err
	case wireFixed64:
		if r.pos+8 > len(r.buf) {
			return ErrProto
		}
		r.pos += 8
		return nil
	case wireBytes:
		_, err := r.ReadBytes()
		return err
	case wireFixed32:
		if r.pos+4 > len(r.buf) {
			return ErrProto
		}
		r.pos += 4
		return nil
	default:
		return fmt.Errorf("%w: unknown wire type %d", ErrProto, wire)
	}
}

// ProtoWriter builds a protobuf byte stream.
type ProtoWriter struct {
	buf []byte
}

// NewWriter returns an empty writer.
func NewWriter() *ProtoWriter { return &ProtoWriter{} }

// Bytes returns the accumulated bytes.
func (w *ProtoWriter) Bytes() []byte { return w.buf }

// WriteVarint encodes a varint.
func (w *ProtoWriter) WriteVarint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

// WriteTag writes a field tag.
func (w *ProtoWriter) WriteTag(field, wire int) {
	w.WriteVarint(uint64(field<<3 | wire))
}

// WriteBytes writes a length-delimited field.
func (w *ProtoWriter) WriteBytes(field int, b []byte) {
	w.WriteTag(field, wireBytes)
	w.WriteVarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}

// WriteString writes a length-delimited string field.
func (w *ProtoWriter) WriteString(field int, s string) {
	w.WriteBytes(field, []byte(s))
}

// WriteMessage writes a nested message field.
func (w *ProtoWriter) WriteMessage(field int, b []byte) {
	w.WriteBytes(field, b)
}

// Metadata is the Nacos gRPC Metadata message.
type Metadata struct {
	Type     string            `json:"type"`
	ClientIP string            `json:"clientIp,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// Encode encodes Metadata to protobuf bytes.
func (m *Metadata) Encode() []byte {
	w := NewWriter()
	if m.Type != "" {
		w.WriteString(3, m.Type)
	}
	if m.ClientIP != "" {
		w.WriteString(8, m.ClientIP)
	}
	for k, v := range m.Headers {
		entry := NewWriter()
		entry.WriteString(1, k)
		entry.WriteString(2, v)
		w.WriteMessage(7, entry.Bytes())
	}
	return w.Bytes()
}

// DecodeMetadata decodes Metadata from protobuf bytes.
func DecodeMetadata(b []byte) (Metadata, error) {
	r := NewReader(b)
	m := Metadata{}
	for !r.Done() {
		field, wire, err := r.ReadTag()
		if err != nil {
			return m, err
		}
		switch field {
		case 3:
			m.Type, err = r.ReadString()
		case 8:
			m.ClientIP, err = r.ReadString()
		case 7:
			entry, err := r.ReadBytes()
			if err != nil {
				return m, err
			}
			k, v, err := decodeHeaderEntry(entry)
			if err != nil {
				return m, err
			}
			if m.Headers == nil {
				m.Headers = map[string]string{}
			}
			m.Headers[k] = v
			continue
		default:
			err = r.Skip(wire)
		}
		if err != nil {
			return m, err
		}
	}
	return m, nil
}

func decodeHeaderEntry(b []byte) (string, string, error) {
	r := NewReader(b)
	var key, val string
	for !r.Done() {
		field, wire, err := r.ReadTag()
		if err != nil {
			return "", "", err
		}
		switch field {
		case 1:
			key, err = r.ReadString()
		case 2:
			val, err = r.ReadString()
		default:
			err = r.Skip(wire)
		}
		if err != nil {
			return "", "", err
		}
	}
	return key, val, nil
}

// Any is the google.protobuf.Any message: type_url (field 1) + value (field 2).
type Any struct {
	TypeURL string
	Value   []byte
}

// Encode encodes Any to protobuf bytes.
func (a *Any) Encode() []byte {
	w := NewWriter()
	if a.TypeURL != "" {
		w.WriteString(1, a.TypeURL)
	}
	if len(a.Value) > 0 {
		w.WriteBytes(2, a.Value)
	}
	return w.Bytes()
}

// DecodeAny decodes Any from protobuf bytes.
func DecodeAny(b []byte) (Any, error) {
	r := NewReader(b)
	a := Any{}
	for !r.Done() {
		field, wire, err := r.ReadTag()
		if err != nil {
			return a, err
		}
		switch field {
		case 1:
			a.TypeURL, err = r.ReadString()
		case 2:
			a.Value, err = r.ReadBytes()
		default:
			err = r.Skip(wire)
		}
		if err != nil {
			return a, err
		}
	}
	return a, nil
}

// Payload is the Nacos gRPC Payload message.
type Payload struct {
	Metadata Metadata
	Body     Any
}

// Encode encodes Payload to protobuf bytes.
func (p *Payload) Encode() []byte {
	w := NewWriter()
	meta := p.Metadata.Encode()
	if len(meta) > 0 {
		w.WriteMessage(2, meta)
	}
	body := p.Body.Encode()
	if len(body) > 0 {
		w.WriteMessage(3, body)
	}
	return w.Bytes()
}

// DecodePayload decodes Payload from protobuf bytes.
func DecodePayload(b []byte) (Payload, error) {
	r := NewReader(b)
	p := Payload{}
	for !r.Done() {
		field, wire, err := r.ReadTag()
		if err != nil {
			return p, err
		}
		switch field {
		case 2:
			meta, err := r.ReadBytes()
			if err != nil {
				return p, err
			}
			p.Metadata, err = DecodeMetadata(meta)
			if err != nil {
				return p, err
			}
			continue
		case 3:
			body, err := r.ReadBytes()
			if err != nil {
				return p, err
			}
			p.Body, err = DecodeAny(body)
			if err != nil {
				return p, err
			}
			continue
		default:
			err = r.Skip(wire)
		}
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

// Frame is a single gRPC length-prefixed frame.
type Frame struct {
	Compressed bool
	Payload    []byte
}

// ReadFrame reads one gRPC frame from r. It enforces the per-frame size
// limit, rejecting oversized frames before allocating memory for the body.
// The limit defaults to [DefaultMaxFrameBytes]; callers wanting a different
// limit should use [ReadFrameWithLimit].
func ReadFrame(r io.Reader) (Frame, error) {
	return ReadFrameWithLimit(r, DefaultMaxFrameBytes)
}

// ReadFrameWithLimit reads one gRPC frame from r, rejecting frames whose
// declared payload length exceeds maxBytes. A negative or zero maxBytes is
// treated as unlimited (use with caution — a malicious peer can drive the
// process into OOM by claiming a 4 GiB body).
//
// On error the reader is left in an unspecified state; callers should close
// the connection rather than trying to recover framing.
func ReadFrameWithLimit(r io.Reader, maxBytes int) (Frame, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}
	compressed := header[0] != 0
	length := int(header[1])<<24 | int(header[2])<<16 | int(header[3])<<8 | int(header[4])
	if length < 0 {
		return Frame{}, ErrProto
	}
	if maxBytes > 0 && length > maxBytes {
		return Frame{}, ErrFrameTooLarge
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	return Frame{Compressed: compressed, Payload: body}, nil
}

// ErrReadFrameTimeout is returned by ReadFrameWithLimitAndTimeout when
// the per-frame read deadline elapses before the frame is fully read.
// The caller should close the stream (return from the handler with an
// error status) so the underlying blocked goroutine unblocks — it
// exits when the HTTP server closes r.Body as part of stream cleanup.
var ErrReadFrameTimeout = errors.New("grpc: frame read timed out")

// ReadFrameWithLimitAndTimeout reads one gRPC frame from r with a
// per-frame read deadline. It behaves like [ReadFrameWithLimit] but
// aborts the read if the header or body cannot be read within
// timeout. A timeout <= 0 disables the cap (delegates to
// ReadFrameWithLimit).
//
// This is the slowloris-on-body protection for the gRPC path: without
// a per-frame deadline, a peer can send a frame body 1 byte at a time
// and hold the server's goroutine for up to MaxFrameBytes seconds (4
// MiB at 1 byte/sec ≈ 48 days). MaxConns caps the total connection
// count, but each held connection still holds a goroutine and a fd.
//
// Implementation: the read runs in a goroutine; the caller blocks on
// a select between the goroutine's result channel and a timer. On
// timeout, the caller returns ErrReadFrameTimeout and the caller's
// handler should return from the RPC so the HTTP server closes
// r.Body, which unblocks the leaked goroutine. The result channel is
// buffered (cap 1) so the goroutine's send does not block when the
// caller has already returned on timeout — the goroutine exits
// cleanly once r.Body is closed.
func ReadFrameWithLimitAndTimeout(r io.Reader, maxBytes int, timeout time.Duration) (Frame, error) {
	if timeout <= 0 {
		return ReadFrameWithLimit(r, maxBytes)
	}
	type result struct {
		frame Frame
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := ReadFrameWithLimit(r, maxBytes)
		ch <- result{f, err}
	}()
	select {
	case res := <-ch:
		return res.frame, res.err
	case <-time.After(timeout):
		return Frame{}, ErrReadFrameTimeout
	}
}

// WriteFrame writes one gRPC frame to w.
func WriteFrame(w io.Writer, f Frame) error {
	header := make([]byte, 5)
	if f.Compressed {
		header[0] = 1
	}
	length := len(f.Payload)
	header[1] = byte(length >> 24)
	header[2] = byte(length >> 16)
	header[3] = byte(length >> 8)
	header[4] = byte(length)
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(f.Payload); err != nil {
		return err
	}
	return nil
}
