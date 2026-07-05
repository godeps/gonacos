package server

import (
	"net"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/observability"
)

// startResourceCollector registers resource-count gauges and launches a
// background goroutine that refreshes them on the given interval. Returns a
// stop function safe to call multiple times.
//
// Gauges exposed:
//
//	gonacos_namespaces_total          — number of namespaces
//	gonacos_configs_total             — total config items across all namespaces
//	gonacos_services_total            — total registered services across all namespaces
//	gonacos_users_total               — number of registered users
//	gonacos_instances_total           — total service instances across all services
//	gonacos_grpc_connections          — active long-lived gRPC push connections
//	gonacos_active_connections{proto} — active TCP connections per protocol
//	                                    (proto="http" or "grpc"); only populated
//	                                    when [WithMaxConns] is set, since the
//	                                    connection counter lives on the cap
//	                                    wrapper. Without the cap, the gauge
//	                                    stays at zero — operators who want
//	                                    the metric should set a high cap
//	                                    (e.g., 100000) to track without
//	                                    effectively limiting.
//
// The collector is a no-op when registry or bundle is nil. Sampling is O(n)
// on the in-memory store (bounded by the namespace quota), so at a 30s
// default interval the overhead is negligible against the request hot path.
func startResourceCollector(registry *observability.Registry, bundle *app.ServiceBundle, push *app.PushService, httpLn, grpcLn net.Listener, interval time.Duration) func() {
	if registry == nil || bundle == nil {
		return func() {}
	}
	gauges := struct {
		namespaces  *observability.Gauge
		configs     *observability.Gauge
		services    *observability.Gauge
		users       *observability.Gauge
		instances   *observability.Gauge
		connections *observability.Gauge
		httpConns   *observability.Gauge
		grpcConns   *observability.Gauge
	}{
		namespaces:  registry.Gauge("gonacos_namespaces_total", nil),
		configs:     registry.Gauge("gonacos_configs_total", nil),
		services:    registry.Gauge("gonacos_services_total", nil),
		users:       registry.Gauge("gonacos_users_total", nil),
		instances:   registry.Gauge("gonacos_instances_total", nil),
		connections: registry.Gauge("gonacos_grpc_connections", nil),
		httpConns:   registry.Gauge("gonacos_active_connections", map[string]string{"proto": "http"}),
		grpcConns:   registry.Gauge("gonacos_active_connections", map[string]string{"proto": "grpc"}),
	}

	refresh := func() {
		namespaces := bundle.Namespace.List()
		gauges.namespaces.Set(int64(len(namespaces)))

		// Configs: sum TotalCount across all namespaces. Each namespace's
		// List is O(configs-in-namespace); the namespace quota bounds this.
		var configs int64
		for _, ns := range namespaces {
			page, err := bundle.Config.List(ns.Namespace, "", "", "", 1, 1)
			if err != nil {
				continue
			}
			configs += int64(page.TotalCount)
		}
		gauges.configs.Set(configs)

		// Services and instances: O(services) on the naming store, no
		// per-namespace sweep needed.
		gauges.services.Set(int64(bundle.Naming.CountAllServices()))
		gauges.instances.Set(int64(bundle.Naming.CountAllInstances()))

		// Users: TotalCount from a size-1 page query.
		up, err := bundle.Auth.ListUsers(1, 1, "", "")
		if err == nil {
			gauges.users.Set(int64(up.TotalCount))
		}

		// Active gRPC push connections (long-lived BiRequestStream streams
		// registered with the push service's connection registry). Nil when
		// push is disabled — leave the gauge at 0 in that case.
		if push != nil {
			if reg := push.ConnectionRegistry(); reg != nil {
				gauges.connections.Set(int64(reg.Count()))
			}
		}

		// Active TCP connections per protocol. Only meaningful when
		// [WithMaxConns] is set — the count lives on the maxConnsListener
		// wrapper. Without the cap, the listeners are raw net.Listeners
		// and the gauges stay at 0 (their initial value).
		if ml, ok := httpLn.(*maxConnsListener); ok {
			gauges.httpConns.Set(int64(ml.CurrentConns()))
		}
		if ml, ok := grpcLn.(*maxConnsListener); ok {
			gauges.grpcConns.Set(int64(ml.CurrentConns()))
		}
	}

	refresh()
	if interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				refresh()
			case <-stop:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
