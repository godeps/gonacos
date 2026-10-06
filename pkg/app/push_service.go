package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	configsvc "github.com/godeps/gonacos/pkg/config"
	namingsvc "github.com/godeps/gonacos/pkg/naming"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol/grpc"
)

// PushService wires config and naming change notifications to the gRPC
// connection registry so the server can push ConfigChangeNotifyRequest and
// NotifySubscriberRequest frames to subscribed SDK clients.
//
// The SDK opens a BiRequestStream and sends a ConnectionSetupRequest. The
// gRPC layer registers the connection's send function in the
// ConnectionRegistry, keyed by an identity assigned to the accepted socket.
// Unary requests on that same HTTP/2 connection use the same identity when
// recording subscriptions.
//
// When a config or service changes, the service layer calls the push
// callback. The PushService looks up the subscribed connections and pushes
// the notification payload on each registered connection.
//
// Transport identity is the correlation key because the Go SDK does not
// include the connection ID in unary request headers. Client IP cannot be
// used here: unrelated clients behind one proxy or NAT may share an IP.
type PushService struct {
	registry *grpc.ConnectionRegistry
	config   *configsvc.Service
	naming   *namingsvc.Service

	mu                    sync.RWMutex
	configSubs            map[string]map[string]bool // configKey -> set of connectionIDs
	serviceSubs           map[string]map[string]bool // serviceKey -> set of connectionIDs
	connectionConfigSubs  map[string]map[string]bool // connectionID -> set of configKeys
	connectionServiceSubs map[string]map[string]bool // connectionID -> set of serviceKeys

	metrics *pushMetrics
}

// pushMetrics holds the observability handles for the push path. Nil when no
// registry is configured (metrics are no-ops).
type pushMetrics struct {
	pushConfigTotal  *observability.Counter
	pushServiceTotal *observability.Counter
	configSubsGauge  *observability.Gauge
	serviceSubsGauge *observability.Gauge
}

// SetMetricsRegistry wires observability counters/gauges for the push path.
// Pass nil to disable metrics. Safe to call before or after InstallCallbacks.
func (p *PushService) SetMetricsRegistry(r *observability.Registry) {
	if p == nil || r == nil {
		return
	}
	p.metrics = &pushMetrics{
		pushConfigTotal:  r.Counter("gonacos_push_total", map[string]string{"type": "config"}),
		pushServiceTotal: r.Counter("gonacos_push_total", map[string]string{"type": "service"}),
		configSubsGauge:  r.Gauge("gonacos_config_subscriptions", nil),
		serviceSubsGauge: r.Gauge("gonacos_service_subscriptions", nil),
	}
}

// refreshSubGaugesLocked updates the subscription gauges. Caller must hold
// p.mu (either read or write lock).
func (p *PushService) refreshSubGaugesLocked() {
	if p == nil || p.metrics == nil {
		return
	}
	configTotal := 0
	for _, connections := range p.configSubs {
		configTotal += len(connections)
	}
	serviceTotal := 0
	for _, connections := range p.serviceSubs {
		serviceTotal += len(connections)
	}
	p.metrics.configSubsGauge.Set(int64(configTotal))
	p.metrics.serviceSubsGauge.Set(int64(serviceTotal))
}

// NewPushService creates a PushService wired to the given registry and
// services. Returns nil if registry is nil.
func NewPushService(registry *grpc.ConnectionRegistry, config *configsvc.Service, naming *namingsvc.Service) *PushService {
	if registry == nil {
		return nil
	}
	p := &PushService{
		registry:              registry,
		config:                config,
		naming:                naming,
		configSubs:            map[string]map[string]bool{},
		serviceSubs:           map[string]map[string]bool{},
		connectionConfigSubs:  map[string]map[string]bool{},
		connectionServiceSubs: map[string]map[string]bool{},
	}
	registry.SetOnUnregister(p.removeClientSubscriptions)
	return p
}

// ConnectionRegistry returns the underlying registry (for wiring into the
// gRPC server setup).
func (p *PushService) ConnectionRegistry() *grpc.ConnectionRegistry {
	return p.registry
}

// InstallCallbacks wires the push service into the config and naming
// services. After this call, any config or service change triggers a push
// to subscribed connections.
func (p *PushService) InstallCallbacks() {
	if p == nil {
		return
	}
	if p.config != nil {
		p.config.SetPushFunc(p.notifyConfigChange)
	}
	if p.naming != nil {
		p.naming.SetPushFunc(p.notifyServiceChange)
	}
}

// TrackConfigSubscription records that a connection identity is listening to a
// config. subscribe=false removes the subscription. Called from the gRPC
// ConfigBatchListenRequest handler.
func (p *PushService) TrackConfigSubscription(connectionID, namespaceID, groupName, dataID string, subscribe bool) {
	if p == nil || connectionID == "" {
		return
	}
	ck := configKey(namespaceID, groupName, dataID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if subscribe {
		if p.configSubs[ck] == nil {
			p.configSubs[ck] = map[string]bool{}
		}
		p.configSubs[ck][connectionID] = true
		if p.connectionConfigSubs[connectionID] == nil {
			p.connectionConfigSubs[connectionID] = map[string]bool{}
		}
		p.connectionConfigSubs[connectionID][ck] = true
	} else {
		p.removeSub(p.configSubs, ck, connectionID)
		p.removeSub(p.connectionConfigSubs, connectionID, ck)
	}
	p.refreshSubGaugesLocked()
}

// TrackServiceSubscription records that a connection identity is subscribed to a
// service. subscribe=false removes the subscription. Called from the gRPC
// SubscribeServiceRequest handler.
func (p *PushService) TrackServiceSubscription(connectionID, namespaceID, groupName, serviceName string, subscribe bool) {
	if p == nil || connectionID == "" {
		return
	}
	sk := serviceKey(namespaceID, groupName, serviceName)
	p.mu.Lock()
	defer p.mu.Unlock()
	if subscribe {
		if p.serviceSubs[sk] == nil {
			p.serviceSubs[sk] = map[string]bool{}
		}
		p.serviceSubs[sk][connectionID] = true
		if p.connectionServiceSubs[connectionID] == nil {
			p.connectionServiceSubs[connectionID] = map[string]bool{}
		}
		p.connectionServiceSubs[connectionID][sk] = true
	} else {
		p.removeSub(p.serviceSubs, sk, connectionID)
		p.removeSub(p.connectionServiceSubs, connectionID, sk)
	}
	p.refreshSubGaugesLocked()
}

// removeSub deletes a key from a two-level map. Cleans up empty inner maps.
func (p *PushService) removeSub(outer map[string]map[string]bool, outerKey, innerKey string) {
	if subs, ok := outer[outerKey]; ok {
		delete(subs, innerKey)
		if len(subs) == 0 {
			delete(outer, outerKey)
		}
	}
}

// UnregisterClient removes all subscriptions for a connection identity. Called when
// the BiRequestStream closes.
func (p *PushService) UnregisterClient(connectionID string) {
	if p == nil || connectionID == "" {
		return
	}
	p.registry.Unregister(connectionID)
	p.removeClientSubscriptions(connectionID)
}

func (p *PushService) removeClientSubscriptions(connectionID string) {
	p.mu.Lock()
	for ck := range p.connectionConfigSubs[connectionID] {
		p.removeSub(p.configSubs, ck, connectionID)
	}
	delete(p.connectionConfigSubs, connectionID)
	for sk := range p.connectionServiceSubs[connectionID] {
		p.removeSub(p.serviceSubs, sk, connectionID)
	}
	delete(p.connectionServiceSubs, connectionID)
	p.refreshSubGaugesLocked()
	p.mu.Unlock()
}

// notifyConfigChange is the callback installed on the config service. It
// builds a ConfigChangeNotifyRequest and pushes it to all connections
// subscribed to the changed config.
func (p *PushService) notifyConfigChange(namespaceID, groupName, dataID string) {
	if p == nil {
		return
	}
	ck := configKey(namespaceID, groupName, dataID)
	p.mu.RLock()
	connections := make([]string, 0, len(p.configSubs[ck]))
	for connectionID := range p.configSubs[ck] {
		connections = append(connections, connectionID)
	}
	p.mu.RUnlock()
	if len(connections) == 0 {
		return
	}
	payload := buildConfigChangeNotify(namespaceID, groupName, dataID)
	for _, connectionID := range connections {
		p.registry.Push(connectionID, payload)
	}
	if p.metrics != nil {
		p.metrics.pushConfigTotal.Add(int64(len(connections)))
	}
}

// notifyServiceChange is the callback installed on the naming service. It
// builds a NotifySubscriberRequest with the current instance list and
// pushes it to all connections subscribed to the changed service.
func (p *PushService) notifyServiceChange(namespaceID, groupName, serviceName string) {
	if p == nil || p.naming == nil {
		return
	}
	sk := serviceKey(namespaceID, groupName, serviceName)
	p.mu.RLock()
	connections := make([]string, 0, len(p.serviceSubs[sk]))
	for connectionID := range p.serviceSubs[sk] {
		connections = append(connections, connectionID)
	}
	p.mu.RUnlock()
	if len(connections) == 0 {
		return
	}
	payload, err := buildNotifySubscriber(p.naming, namespaceID, groupName, serviceName)
	if err != nil {
		return
	}
	for _, connectionID := range connections {
		p.registry.Push(connectionID, payload)
	}
	if p.metrics != nil {
		p.metrics.pushServiceTotal.Add(int64(len(connections)))
	}
}

// configKey returns a stable string key for a config subscription.
func configKey(namespaceID, groupName, dataID string) string {
	return strings.Join([]string{normalizeNamespace(namespaceID), groupName, dataID}, ":")
}

// serviceKey returns a stable string key for a service subscription.
func serviceKey(namespaceID, groupName, serviceName string) string {
	return strings.Join([]string{normalizeNamespace(namespaceID), groupName, serviceName}, ":")
}

func normalizeNamespace(n string) string {
	if strings.TrimSpace(n) == "" {
		return "public"
	}
	return strings.TrimSpace(n)
}

// buildConfigChangeNotify constructs the ConfigChangeNotifyRequest payload
// that the SDK expects on the BiRequestStream.
func buildConfigChangeNotify(namespaceID, groupName, dataID string) grpc.Payload {
	body := map[string]any{
		"group":     groupName,
		"dataId":    dataID,
		"tenant":    normalizeNamespace(namespaceID),
		"module":    "config",
		"requestId": nextPushID(),
	}
	bodyBytes, _ := json.Marshal(body)
	return grpc.Payload{
		Metadata: grpc.Metadata{
			Type:    "ConfigChangeNotifyRequest",
			Headers: map[string]string{},
		},
		Body: grpc.Any{Value: bodyBytes},
	}
}

// buildNotifySubscriber constructs the NotifySubscriberRequest payload with
// the current instance list for the service.
func buildNotifySubscriber(naming *namingsvc.Service, namespaceID, groupName, serviceName string) (grpc.Payload, error) {
	instances, err := naming.ListInstances(namespaceID, groupName, serviceName, "", false)
	if err != nil {
		return grpc.Payload{}, fmt.Errorf("list instances for push: %w", err)
	}
	hosts := make([]map[string]any, 0, len(instances))
	for _, inst := range instances {
		hosts = append(hosts, map[string]any{
			"instanceId":  inst.InstanceID,
			"ip":          inst.IP,
			"port":        inst.Port,
			"weight":      inst.Weight,
			"healthy":     inst.Healthy,
			"enabled":     inst.Enabled,
			"ephemeral":   inst.Ephemeral,
			"clusterName": inst.ClusterName,
			"serviceName": inst.ServiceName,
			"metadata":    inst.Metadata,
		})
	}
	body := map[string]any{
		"namespace":   normalizeNamespace(namespaceID),
		"serviceName": serviceName,
		"groupName":   groupName,
		"module":      "naming",
		"requestId":   nextPushID(),
		"serviceInfo": map[string]any{
			"name":        serviceName,
			"groupName":   groupName,
			"clusters":    "",
			"hosts":       hosts,
			"valid":       true,
			"allIPs":      false,
			"lastRefTime": currentMillis(),
		},
	}
	bodyBytes, _ := json.Marshal(body)
	return grpc.Payload{
		Metadata: grpc.Metadata{
			Type:    "NotifySubscriberRequest",
			Headers: map[string]string{},
		},
		Body: grpc.Any{Value: bodyBytes},
	}, nil
}

// pushIDSeq is a monotonic counter used to generate unique request IDs for
// server-pushed ConfigChangeNotifyRequest and NotifySubscriberRequest frames.
// The SDK's NotifySubscriberRequest handler registers a zero-value
// NamingRequest whose embedded *Request is nil; including a requestId in the
// JSON body makes json.Unmarshal allocate the embedded *Request so the SDK's
// subsequent PutAllHeaders call does not dereference nil.
var pushIDSeq uint64

func nextPushID() string {
	return fmt.Sprintf("push-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&pushIDSeq, 1))
}
