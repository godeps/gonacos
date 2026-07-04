package server_test

import (
	"context"
	"testing"

	"github.com/godeps/gonacos/pkg/config"
	"github.com/godeps/gonacos/pkg/server"
)

// TestEmbedDirectCall exercises the embeddable API without a network hop:
// construct a Server, call Service methods directly, snapshot, and shut down.
// It does not call Start (sdkcompat and cluster integration tests cover the
// HTTP/gRPC serving path end-to-end via the CLI binary).
func TestEmbedDirectCall(t *testing.T) {
	srv, err := server.New(
		server.WithAddr("127.0.0.1:0"),
		server.WithGRPCAddr("127.0.0.1:0"),
		server.WithRoot(".."),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	bundle := srv.Services()
	if bundle == nil || bundle.Config == nil || bundle.Naming == nil {
		t.Fatal("Services() returned nil bundle or nil service")
	}
	if srv.Coordinator() == nil {
		t.Fatal("Coordinator() returned nil")
	}
	if srv.RedisClient() == nil {
		t.Fatal("RedisClient() returned nil")
	}

	// Direct config publish/get without going through HTTP/gRPC.
	req := config.PublishRequest{
		NamespaceID: "public",
		GroupName:   "DEFAULT_GROUP",
		DataID:      "embed-smoke",
		Content:     `{"k":"v"}`,
		Type:        "json",
	}
	if err := bundle.Config.Publish(req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	item, err := bundle.Config.Get("public", "DEFAULT_GROUP", "embed-smoke")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.Content != `{"k":"v"}` {
		t.Errorf("Get content = %q, want %q", item.Content, `{"k":"v"}`)
	}

	// Snapshot backup of all registered services.
	env, err := srv.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if env == nil {
		t.Fatal("Snapshot returned nil envelope")
	}
}
