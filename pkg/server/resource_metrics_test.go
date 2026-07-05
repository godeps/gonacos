package server

import (
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

	stop := startResourceCollector(registry, bundle, 0) // 0 = no background ticker
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
	} {
		if !strings.Contains(out, name) {
			t.Errorf("metrics output missing %q:\n%s", name, out)
		}
	}
}

// TestResourceCollectorNilSafe verifies that nil registry or nil bundle
// doesn't panic and returns a callable no-op stop function.
func TestResourceCollectorNilSafe(t *testing.T) {
	stop1 := startResourceCollector(nil, nil, time.Second)
	stop1() // must not panic

	registry := observability.NewRegistry()
	stop2 := startResourceCollector(registry, nil, time.Second)
	stop2() // must not panic
}
