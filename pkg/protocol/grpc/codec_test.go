package grpc

import (
	"bytes"
	"context"
	"testing"
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
