package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/observability"
)

// TestResourceCollectorExposesGauges verifies that startResourceCollector
// registers the expected gauges and reports the initial counts from the
// service bundle.
func TestResourceCollectorExposesGauges(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()

	// Pass nil listeners — the connection gauges default to 0 when no
	// maxConnsListener is wired. The other gauges must still register.
	stop := startResourceCollector(registry, bundle, nil, nil, nil, nil, 0) // 0 = no background ticker
	defer stop()

	var buf strings.Builder
	registry.WritePrometheus(&buf)
	out := buf.String()

	for _, name := range []string{
		"gonacos_namespaces_total",
		"gonacos_configs_total",
		"gonacos_services_total",
		"gonacos_users_total",
		"gonacos_instances_total",
		"gonacos_grpc_connections",
		"gonacos_active_connections",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("metrics output missing %q:\n%s", name, out)
		}
	}
}

// TestResourceCollectorNilSafe verifies that nil registry or nil bundle
// doesn't panic and returns a callable no-op stop function.
func TestResourceCollectorNilSafe(t *testing.T) {
	stop1 := startResourceCollector(nil, nil, nil, nil, nil, nil, time.Second)
	stop1() // must not panic

	registry := observability.NewRegistry()
	stop2 := startResourceCollector(registry, nil, nil, nil, nil, nil, time.Second)
	stop2() // must not panic
}

// TestResourceCollectorReportsActiveConnections verifies that when
// maxConnsListener wraps the listeners, the active connection count is
// exposed as gonacos_active_connections{proto="http|grpc"}.
//
// The metric is the saturation signal: alert when count approaches
// maxConns (the cap). A connection flood pushing the gauge to the cap
// means new connections are being rejected — operators need to know
// before legitimate clients start failing.
func TestResourceCollectorReportsActiveConnections(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()

	// Two real listeners wrapped with maxConnsListener. Use :0 to get
	// kernel-assigned ports so the test doesn't collide on a fixed port.
	rawHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	defer rawHTTP.Close()
	rawGRPC, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("grpc listen: %v", err)
	}
	defer rawGRPC.Close()
	httpLn := newMaxConnsListener(rawHTTP, 100)
	grpcLn := newMaxConnsListener(rawGRPC, 100)

	stop := startResourceCollector(registry, bundle, nil, httpLn, grpcLn, nil, 0)
	defer stop()

	// Open 3 HTTP connections and 2 gRPC connections, then refresh the
	// collector manually by calling stop+restart (or just open a fresh
	// collector — the gauges are cached on the registry).
	httpConns := make([]net.Conn, 3)
	for i := range httpConns {
		c, err := net.Dial("tcp", rawHTTP.Addr().String())
		if err != nil {
			t.Fatalf("dial http: %v", err)
		}
		httpConns[i] = c
	}
	defer func() {
		for _, c := range httpConns {
			_ = c.Close()
		}
	}()

	grpcConns := make([]net.Conn, 2)
	for i := range grpcConns {
		c, err := net.Dial("tcp", rawGRPC.Addr().String())
		if err != nil {
			t.Fatalf("dial grpc: %v", err)
		}
		grpcConns[i] = c
	}
	defer func() {
		for _, c := range grpcConns {
			_ = c.Close()
		}
	}()

	// Drain the accepts so the wrapped listeners' current counter
	// advances. Each Accept returns a trackedConn whose Close decrements
	// the counter; we hold the dialed conns open, so the server-side
	// tracked conns also stay open until the test ends.
	acceptedHTTP := make([]net.Conn, 0, 3)
	for i := 0; i < 3; i++ {
		c, err := httpLn.Accept()
		if err != nil {
			t.Fatalf("accept http: %v", err)
		}
		acceptedHTTP = append(acceptedHTTP, c)
	}
	defer func() {
		for _, c := range acceptedHTTP {
			_ = c.Close()
		}
	}()
	acceptedGRPC := make([]net.Conn, 0, 2)
	for i := 0; i < 2; i++ {
		c, err := grpcLn.Accept()
		if err != nil {
			t.Fatalf("accept grpc: %v", err)
		}
		acceptedGRPC = append(acceptedGRPC, c)
	}
	defer func() {
		for _, c := range acceptedGRPC {
			_ = c.Close()
		}
	}()

	// Re-invoke the collector to refresh gauges against the now-open
	// connections. The first call (inside startResourceCollector) ran
	// before we opened connections, so its reading was 0.
	stop()
	stop = startResourceCollector(registry, bundle, nil, httpLn, grpcLn, nil, 0)
	defer stop()

	httpGauge := registry.Gauge("gonacos_active_connections",
		map[string]string{"proto": "http"}).Value()
	grpcGauge := registry.Gauge("gonacos_active_connections",
		map[string]string{"proto": "grpc"}).Value()
	if httpGauge != 3 {
		t.Errorf("http active connections = %d, want 3", httpGauge)
	}
	if grpcGauge != 2 {
		t.Errorf("grpc active connections = %d, want 2", grpcGauge)
	}
}

// TestResourceCollectorRawListenersNoOpForConnections verifies that when
// listeners are NOT wrapped with maxConnsListener (maxConns disabled), the
// connection gauges stay at 0 — we don't have the data, so we don't
// fabricate it. Operators who want the metric should set a high cap.
func TestResourceCollectorRawListenersNoOpForConnections(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()

	rawHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	defer rawHTTP.Close()

	stop := startResourceCollector(registry, bundle, nil, rawHTTP, nil, nil, 0)
	defer stop()

	httpGauge := registry.Gauge("gonacos_active_connections",
		map[string]string{"proto": "http"}).Value()
	if httpGauge != 0 {
		t.Errorf("http active connections with raw listener = %d, want 0 (no maxConnsListener)", httpGauge)
	}
}

// TestResourceCollectorExposesConnectionRejections verifies that the
// resource collector samples maxConnsListener.RejectedConns into
// gonacos_connection_rejections_total{proto}. Operators alert on a
// non-zero rate to catch a connection-flood attack or capacity shortfall
// — without this gauge, they can only infer from active_connections
// plateauing at max, which is indirect.
func TestResourceCollectorExposesConnectionRejections(t *testing.T) {
	registry := observability.NewRegistry()
	bundle := app.NewServiceBundle()

	rawHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	defer rawHTTP.Close()
	httpLn := newMaxConnsListener(rawHTTP, 1) // cap at 1 to force rejections

	stop := startResourceCollector(registry, bundle, nil, httpLn, nil, nil, 0)
	defer stop()

	// Hold one connection open to fill the cap, then dial two more —
	// both should be rejected.
	accepted := make(chan net.Conn, 10)
	go func() {
		for {
			c, err := httpLn.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	c1, err := net.Dial("tcp", rawHTTP.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	time.Sleep(50 * time.Millisecond) // let accept fill the cap

	c2, err := net.Dial("tcp", rawHTTP.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()
	c3, err := net.Dial("tcp", rawHTTP.Addr().String())
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	defer c3.Close()
	time.Sleep(50 * time.Millisecond) // let rejections register

	// Drain accepted conns so trackedConn.Close decrements the counter
	// and frees the slot. Without this, the listener's current counter
	// would stay at 1 and the test cleanup would leak goroutines.
	defer func() {
		for {
			select {
			case c := <-accepted:
				_ = c.Close()
			default:
				return
			}
		}
	}()

	// Re-invoke the collector to refresh gauges.
	stop()
	stop = startResourceCollector(registry, bundle, nil, httpLn, nil, nil, 0)
	defer stop()

	rejGauge := registry.Gauge("gonacos_connection_rejections_total",
		map[string]string{"proto": "http"}).Value()
	if rejGauge < 2 {
		t.Fatalf("http connection rejections = %d, want >= 2", rejGauge)
	}
}
