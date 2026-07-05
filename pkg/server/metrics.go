package server

import (
	"github.com/godeps/gonacos/pkg/observability"
	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

// grpcMetricsAdapter bridges *observability.Registry to the
// grpc.MetricsRegistry interface. The adapter exists because Go's interface
// satisfaction requires exact method-signature matches: Registry.Counter
// returns *observability.Counter, but the gRPC server's interface wants a
// return type of grpc.CounterMetric. *observability.Counter has the right
// Inc() method, but the signatures don't line up — so we adapt.
//
// The adapter is stateless apart from the wrapped registry pointer; create
// one per server and reuse it for the lifetime of that server.
type grpcMetricsAdapter struct {
	r *observability.Registry
}

// Counter delegates to the underlying registry, returning the same
// *observability.Counter pointer (which satisfies grpc.CounterMetric via
// its Inc() method). The same counter instance is returned for the same
// (name, labels) pair across calls, so increments accumulate correctly.
func (a *grpcMetricsAdapter) Counter(name string, labels map[string]string) grpcsrv.CounterMetric {
	return a.r.Counter(name, labels)
}
