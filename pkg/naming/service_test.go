package naming

import (
	"testing"
	"time"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := NewService()
	t.Cleanup(s.Stop)
	return s
}

func TestServiceCreateServiceDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if err := s.CreateService("", "g", "svc", false, 0, nil, Selector{}); err != ErrMissingNamespaceID {
		t.Fatalf("missing namespace: %v", err)
	}
	if err := s.CreateService("ns", "", "svc", false, 0, nil, Selector{}); err != ErrMissingGroupName {
		t.Fatalf("missing group: %v", err)
	}
	if err := s.CreateService("ns", "g", "", false, 0, nil, Selector{}); err != ErrMissingServiceName {
		t.Fatalf("missing service: %v", err)
	}
	if err := s.CreateService("ns", "g", "svc", false, -0.1, nil, Selector{}); err != ErrInvalidThreshold {
		t.Fatalf("invalid threshold: %v", err)
	}
	if err := s.CreateService("ns", "g", "svc", false, 1.1, nil, Selector{}); err != ErrInvalidThreshold {
		t.Fatalf("invalid threshold: %v", err)
	}

	if err := s.CreateService("ns", "g", "svc", true, 0.5, map[string]string{"k": "v"}, Selector{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateService("ns", "g", "svc", true, 0.5, nil, Selector{}); err != ErrServiceExists {
		t.Fatalf("duplicate: %v", err)
	}

	info, err := s.GetService("ns", "g", "svc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !info.Ephemeral {
		t.Fatalf("ephemeral not set")
	}
	if info.ProtectThreshold != 0.5 {
		t.Fatalf("threshold = %v, want 0.5", info.ProtectThreshold)
	}
	if info.Selector.Type != SelectorRandom {
		t.Fatalf("selector = %v, want %v", info.Selector.Type, SelectorRandom)
	}
	if info.Metadata["k"] != "v" {
		t.Fatalf("metadata missing: %+v", info.Metadata)
	}
	if len(info.Clusters) != 1 || info.Clusters[0].Name != DefaultClusterName {
		t.Fatalf("default cluster not seeded: %+v", info.Clusters)
	}
}

func TestServiceUpdateDeleteService(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if err := s.CreateService("ns", "g", "svc", true, 0, nil, Selector{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpdateService("ns", "g", "svc", false, 0.3, map[string]string{"a": "b"}, Selector{Type: SelectorWeight}); err != nil {
		t.Fatalf("update: %v", err)
	}
	info, err := s.GetService("ns", "g", "svc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.Ephemeral || info.ProtectThreshold != 0.3 || info.Selector.Type != SelectorWeight || info.Metadata["a"] != "b" {
		t.Fatalf("update not applied: %+v", info)
	}
	if err := s.DeleteService("ns", "g", "svc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetService("ns", "g", "svc"); err != ErrServiceNotFound {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestServiceListPaginationAndFilters(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := s.CreateService("ns", "g", name, true, 0, nil, Selector{}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := s.CreateService("other", "g", "delta", true, 0, nil, Selector{}); err != nil {
		t.Fatalf("create delta: %v", err)
	}

	page, err := s.ListServices("ns", "", "", 1, 2, false, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Count != 3 || len(page.Services) != 2 || page.Services[0].Name != "alpha" {
		t.Fatalf("page1: count=%d len=%d first=%s", page.Count, len(page.Services), firstOr(page.Services))
	}
	page, err = s.ListServices("ns", "", "", 2, 2, false, false)
	if err != nil {
		t.Fatalf("list p2: %v", err)
	}
	if page.Count != 3 || len(page.Services) != 1 || page.Services[0].Name != "gamma" {
		t.Fatalf("page2: count=%d len=%d first=%s", page.Count, len(page.Services), firstOr(page.Services))
	}
	page, err = s.ListServices("ns", "", "alph", 1, 100, false, false)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if page.Count != 1 {
		t.Fatalf("filter count = %d, want 1", page.Count)
	}
}

func firstOr(services []ServiceInfo) string {
	if len(services) == 0 {
		return ""
	}
	return services[0].Name
}

func TestInstanceRegisterAutoCreatesService(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	inst := Instance{
		NamespaceID: "ns",
		GroupName:   "g",
		ServiceName: "svc",
		ClusterName: "DEFAULT",
		IP:          "10.0.0.1",
		Port:        8080,
		Weight:      1,
		Healthy:     true,
		Enabled:     true,
		Ephemeral:   true,
	}
	registered, err := s.RegisterInstance(inst)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.InstanceID == "" {
		t.Fatalf("instance id not set")
	}

	info, err := s.GetService("ns", "g", "svc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.InstanceCount != 1 || info.HealthyInstanceCount != 1 {
		t.Fatalf("counts: %+v", info)
	}
	if len(info.Clusters) != 1 || info.Clusters[0].Name != "DEFAULT" {
		t.Fatalf("clusters: %+v", info.Clusters)
	}
}

func TestInstanceRegisterValidationAndDefaults(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.RegisterInstance(Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "", Port: 0}); err != ErrMissingInstanceID {
		t.Fatalf("missing ip/port: %v", err)
	}
	if _, err := s.RegisterInstance(Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Weight: -1}); err != ErrInvalidWeight {
		t.Fatalf("invalid weight: %v", err)
	}

	registered, err := s.RegisterInstance(Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Healthy: true, Enabled: true})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.Weight != 1 {
		t.Fatalf("weight default = %v, want 1", registered.Weight)
	}
	if registered.ClusterName != DefaultClusterName {
		t.Fatalf("cluster default = %v, want %v", registered.ClusterName, DefaultClusterName)
	}
	if !registered.Healthy || !registered.Enabled {
		t.Fatalf("healthy/enabled not preserved: %+v", registered)
	}
}

func TestInstanceDeregisterByIdentifier(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	inst := Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Ephemeral: true}
	if _, err := s.RegisterInstance(inst); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.DeregisterInstance("ns", "g", "svc", "DEFAULT", "10.0.0.1", 8080, ""); err != nil {
		t.Fatalf("deregister: %v", err)
	}
	instances, err := s.ListInstances("ns", "g", "svc", "", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("instances after deregister: %d", len(instances))
	}
	if err := s.DeregisterInstance("ns", "g", "svc", "DEFAULT", "10.0.0.1", 8080, ""); err != ErrInstanceNotFound {
		t.Fatalf("deregister missing: %v", err)
	}
}

func TestInstanceListFiltersByClusterAndHealth(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	for _, inst := range []Instance{
		{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", ClusterName: "c1", IP: "10.0.0.1", Port: 8080, Healthy: true, Enabled: true, Ephemeral: true},
		{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", ClusterName: "c1", IP: "10.0.0.2", Port: 8080, Healthy: false, Enabled: true, Ephemeral: true},
		{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", ClusterName: "c2", IP: "10.0.0.3", Port: 8080, Healthy: true, Enabled: true, Ephemeral: true},
	} {
		if _, err := s.RegisterInstance(inst); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	all, _ := s.ListInstances("ns", "g", "svc", "", false)
	if len(all) != 3 {
		t.Fatalf("all = %d, want 3", len(all))
	}
	healthy, _ := s.ListInstances("ns", "g", "svc", "", true)
	if len(healthy) != 2 {
		t.Fatalf("healthy = %d, want 2", len(healthy))
	}
	c1, _ := s.ListInstances("ns", "g", "svc", "c1", false)
	if len(c1) != 2 {
		t.Fatalf("c1 = %d, want 2", len(c1))
	}
	page, _ := s.ListInstancesPaginated("ns", "g", "svc", "", 1, 2, false)
	if page.Count != 3 || len(page.Instances) != 2 {
		t.Fatalf("page = count=%d len=%d", page.Count, len(page.Instances))
	}
}

func TestInstanceUpdateMetadataBatch(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	inst := Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Ephemeral: true}
	registered, err := s.RegisterInstance(inst)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	result, err := s.BatchUpdateInstanceMetadata("ns", "g", "svc", []InstanceMetadataUpdate{
		{InstanceID: registered.InstanceID, Metadata: map[string]string{"zone": "a"}},
		{InstanceID: "missing", Metadata: map[string]string{"zone": "b"}},
	})
	if err != nil {
		t.Fatalf("batch update: %v", err)
	}
	if len(result.Updated) != 1 || len(result.Failed) != 1 || result.Failed[0] != "missing" {
		t.Fatalf("result: %+v", result)
	}
	instances, _ := s.ListInstances("ns", "g", "svc", "", false)
	if instances[0].Metadata["zone"] != "a" {
		t.Fatalf("metadata not applied: %+v", instances[0].Metadata)
	}

	del, err := s.BatchDeleteInstanceMetadata("ns", "g", "svc", []string{registered.InstanceID}, []string{"zone"})
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if len(del.Updated) != 1 {
		t.Fatalf("delete result: %+v", del)
	}
	instances, _ = s.ListInstances("ns", "g", "svc", "", false)
	if _, ok := instances[0].Metadata["zone"]; ok {
		t.Fatalf("metadata not deleted: %+v", instances[0].Metadata)
	}
}

func TestClusterUpdateCreatesAndMutates(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if err := s.CreateService("ns", "g", "svc", true, 0, nil, Selector{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpdateCluster("ns", "g", "svc", "edge", 9090, true, map[string]string{"type": "tcp"}, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("update cluster: %v", err)
	}
	clusters, err := s.ListClusters("ns", "g", "svc")
	if err != nil {
		t.Fatalf("list clusters: %v", err)
	}
	var edge *Cluster
	for i := range clusters {
		if clusters[i].Name == "edge" {
			edge = &clusters[i]
		}
	}
	if edge == nil {
		t.Fatalf("edge cluster not found: %+v", clusters)
	}
	if edge.CheckPort != 9090 || edge.HealthChecker["type"] != "tcp" || edge.Metadata["k"] != "v" {
		t.Fatalf("cluster not mutated: %+v", edge)
	}
}

func TestSubscriberAddRemoveList(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if err := s.CreateService("ns", "g", "svc", true, 0, nil, Selector{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AddSubscriber(Subscriber{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", Addr: "1.2.3.4:5678", App: "app1"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddSubscriber(Subscriber{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", Addr: "1.2.3.5:5678", App: "app2"}); err != nil {
		t.Fatalf("add 2: %v", err)
	}
	page, err := s.ListSubscribers("ns", "g", "svc", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Count != 2 || len(page.Subscribers) != 2 {
		t.Fatalf("page: %+v", page)
	}
	if err := s.RemoveSubscriber("ns", "g", "svc", "1.2.3.4:5678"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	page, _ = s.ListSubscribers("ns", "g", "svc", 1, 10)
	if page.Count != 1 || page.Subscribers[0].Addr != "1.2.3.5:5678" {
		t.Fatalf("after remove: %+v", page)
	}
}

func TestHeartbeatKeepsInstanceHealthy(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	inst := Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Ephemeral: true}
	if _, err := s.RegisterInstance(inst); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Heartbeat("ns", "g", "svc", "DEFAULT", "10.0.0.1", 8080, ""); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := s.Heartbeat("ns", "g", "svc", "DEFAULT", "10.0.0.1", 8080, "ns:g:svc:DEFAULT:10.0.0.1:9999"); err != ErrInstanceNotFound {
		t.Fatalf("heartbeat missing: %v", err)
	}
}

func TestLeaseExpiryMarksInstanceUnhealthy(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	// Use a short-interval tracker to keep the test fast.
	s.heartbeat.stop()
	s.heartbeat = newLeaseTracker(100 * time.Millisecond)
	s.heartbeat.start(s.expireLeases)

	inst := Instance{NamespaceID: "ns", GroupName: "g", ServiceName: "svc", IP: "10.0.0.1", Port: 8080, Ephemeral: true, Healthy: true}
	if _, err := s.RegisterInstance(inst); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Wait for the lease to expire and the sweep to run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		instances, _ := s.ListInstances("ns", "g", "svc", "", false)
		if len(instances) == 1 && !instances[0].Healthy {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	instances, _ := s.ListInstances("ns", "g", "svc", "", false)
	if len(instances) != 1 || instances[0].Healthy {
		t.Fatalf("instance not marked unhealthy: %+v", instances)
	}
}

func TestParseMetadataAcceptsKVAndJSON(t *testing.T) {
	t.Parallel()
	m, err := ParseMetadata("k1=v1,k2=v2")
	if err != nil || m["k1"] != "v1" || m["k2"] != "v2" {
		t.Fatalf("kv: %v %v", m, err)
	}
	m, err = ParseMetadata(`{"k":"v"}`)
	if err != nil || m["k"] != "v" {
		t.Fatalf("json: %v %v", m, err)
	}
	if _, err := ParseMetadata("invalid"); err == nil {
		t.Fatalf("invalid: want error")
	}
	if m, _ := ParseMetadata(""); len(m) != 0 {
		t.Fatalf("empty: %v", m)
	}
}

func TestFormatMetadataRoundtrip(t *testing.T) {
	t.Parallel()
	original := map[string]string{"b": "2", "a": "1"}
	formatted := FormatMetadata(original)
	parsed, err := ParseMetadata(formatted)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 2 || parsed["a"] != "1" || parsed["b"] != "2" {
		t.Fatalf("roundtrip: %v", parsed)
	}
}

func TestCountAllInstancesAndServices(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	// Empty: zero counts.
	if got := s.CountAllInstances(); got != 0 {
		t.Fatalf("CountAllInstances empty = %d, want 0", got)
	}
	if got := s.CountAllServices(); got != 0 {
		t.Fatalf("CountAllServices empty = %d, want 0", got)
	}

	// Register 3 instances across 2 services.
	for _, svc := range []string{"svc1", "svc2"} {
		for i := 0; i < 2; i++ {
			inst := Instance{
				NamespaceID: "ns",
				GroupName:   "g",
				ServiceName: svc,
				ClusterName: "DEFAULT",
				IP:          "10.0.0." + string(rune('1'+i)),
				Port:        8080,
				Weight:      1,
				Healthy:     true,
				Enabled:     true,
				Ephemeral:   true,
			}
			if _, err := s.RegisterInstance(inst); err != nil {
				t.Fatalf("register %s/%d: %v", svc, i, err)
			}
		}
	}

	if got := s.CountAllServices(); got != 2 {
		t.Fatalf("CountAllServices = %d, want 2", got)
	}
	if got := s.CountAllInstances(); got != 4 {
		t.Fatalf("CountAllInstances = %d, want 4", got)
	}
}
