package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestTransportIdentitySeparatesClientsSharingIP(t *testing.T) {
	srv := NewServer()
	srv.RegisterUnary("Request/request", func(ctx context.Context, _ Payload) (Payload, error) {
		return buildResponse("IdentityResponse", map[string]string{
			"connectionID": ConnectionIDFromContext(ctx),
			"clientIP":     ClientIPFromContext(ctx),
		}), nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	clientA := &http.Client{Transport: &http.Transport{}}
	clientB := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(clientA.CloseIdleConnections)
	t.Cleanup(clientB.CloseIdleConnections)
	request := func(client *http.Client) map[string]string {
		t.Helper()
		var body bytes.Buffer
		requestPayload := Payload{Metadata: Metadata{Type: "IdentityRequest"}}
		if err := WriteFrame(&body, Frame{Payload: requestPayload.Encode()}); err != nil {
			t.Fatal(err)
		}
		resp, err := client.Post("http://"+ln.Addr().String()+"/Request/request", "application/grpc", &body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		frame, err := ReadFrame(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := DecodePayload(frame.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.Unmarshal(payload.Body.Value, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	a1, a2, b := request(clientA), request(clientA), request(clientB)
	if a1["clientIP"] != "127.0.0.1" || b["clientIP"] != "127.0.0.1" {
		t.Fatalf("clients should share loopback IP: A=%v B=%v", a1, b)
	}
	if a1["connectionID"] == "" || a1["connectionID"] != a2["connectionID"] {
		t.Fatalf("one transport must retain its identity: first=%v second=%v", a1, a2)
	}
	if a1["connectionID"] == b["connectionID"] {
		t.Fatalf("independent transports must have distinct identities: A=%v B=%v", a1, b)
	}
}
