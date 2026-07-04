package app

import (
	"sync"
	"testing"

	configsvc "github.com/godeps/gonacos/internal/config"
	namingsvc "github.com/godeps/gonacos/internal/naming"
	"github.com/godeps/gonacos/internal/protocol/grpc"
)

// TestPushService_ConfigPush verifies that a config change triggers a
// ConfigChangeNotifyRequest push to the subscribed client IP.
func TestPushService_ConfigPush(t *testing.T) {
	t.Parallel()
	registry := grpc.NewConnectionRegistry()
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	push := NewPushService(registry, configSvc, namingSvc)
	push.InstallCallbacks()

	var mu sync.Mutex
	var got []grpc.Payload
	registry.Register("10.0.0.1", func(p grpc.Payload) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		return nil
	})

	push.TrackConfigSubscription("10.0.0.1", "public", "G", "D", true)

	if err := configSvc.Publish(configsvc.PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "D",
		Content:     `{"v":1}`,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("want 1 push, got %d", len(got))
	}
	if got[0].Metadata.Type != "ConfigChangeNotifyRequest" {
		t.Fatalf("push type = %q, want ConfigChangeNotifyRequest", got[0].Metadata.Type)
	}
}

// TestPushService_ConfigPushNoSubscribers verifies that a config change
// with no subscribers does not panic or push.
func TestPushService_ConfigPushNoSubscribers(t *testing.T) {
	t.Parallel()
	registry := grpc.NewConnectionRegistry()
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	push := NewPushService(registry, configSvc, namingSvc)
	push.InstallCallbacks()

	if err := configSvc.Publish(configsvc.PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "D",
		Content:     `{"v":1}`,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// TestPushService_ConfigPushUnsubscribe verifies that unsubscribing stops
// further pushes.
func TestPushService_ConfigPushUnsubscribe(t *testing.T) {
	t.Parallel()
	registry := grpc.NewConnectionRegistry()
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	push := NewPushService(registry, configSvc, namingSvc)
	push.InstallCallbacks()

	var mu sync.Mutex
	var got []grpc.Payload
	registry.Register("10.0.0.2", func(p grpc.Payload) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		return nil
	})

	push.TrackConfigSubscription("10.0.0.2", "public", "G", "D", true)
	push.TrackConfigSubscription("10.0.0.2", "public", "G", "D", false)

	if err := configSvc.Publish(configsvc.PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "D",
		Content:     `{"v":2}`,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("want 0 pushes after unsubscribe, got %d", len(got))
	}
}

// TestPushService_NamingPush verifies that an instance register triggers a
// NotifySubscriberRequest push to the subscribed client IP.
func TestPushService_NamingPush(t *testing.T) {
	t.Parallel()
	registry := grpc.NewConnectionRegistry()
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	push := NewPushService(registry, configSvc, namingSvc)
	push.InstallCallbacks()

	var mu sync.Mutex
	var got []grpc.Payload
	registry.Register("10.0.0.3", func(p grpc.Payload) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		return nil
	})

	push.TrackServiceSubscription("10.0.0.3", "public", "G", "svc", true)

	if _, err := namingSvc.RegisterInstance(namingsvc.Instance{
		NamespaceID: "public",
		GroupName:   "G",
		ServiceName: "svc",
		IP:          "10.0.0.99",
		Port:        8080,
		Ephemeral:   true,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("want 1 push, got %d", len(got))
	}
	if got[0].Metadata.Type != "NotifySubscriberRequest" {
		t.Fatalf("push type = %q, want NotifySubscriberRequest", got[0].Metadata.Type)
	}
}

// TestPushService_UnregisterClient verifies that unregistering a client
// removes all its subscriptions.
func TestPushService_UnregisterClient(t *testing.T) {
	t.Parallel()
	registry := grpc.NewConnectionRegistry()
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	push := NewPushService(registry, configSvc, namingSvc)
	push.InstallCallbacks()

	var mu sync.Mutex
	var got []grpc.Payload
	registry.Register("10.0.0.4", func(p grpc.Payload) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		return nil
	})

	push.TrackConfigSubscription("10.0.0.4", "public", "G", "D1", true)
	push.TrackServiceSubscription("10.0.0.4", "public", "G", "svc1", true)
	push.UnregisterClient("10.0.0.4")

	if err := configSvc.Publish(configsvc.PublishRequest{
		NamespaceID: "public",
		GroupName:   "G",
		DataID:      "D1",
		Content:     `{"v":1}`,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := namingSvc.RegisterInstance(namingsvc.Instance{
		NamespaceID: "public",
		GroupName:   "G",
		ServiceName: "svc1",
		IP:          "10.0.0.98",
		Port:        8080,
		Ephemeral:   true,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("want 0 pushes after unregister, got %d", len(got))
	}
}
