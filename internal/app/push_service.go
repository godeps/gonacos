package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	configsvc "github.com/godeps/gonacos/internal/config"
	namingsvc "github.com/godeps/gonacos/internal/naming"
	"github.com/godeps/gonacos/internal/protocol/grpc"
)

// PushService wires config and naming change notifications to the gRPC
// connection registry so the server can push ConfigChangeNotifyRequest and
// NotifySubscriberRequest frames to subscribed SDK clients.
//
// The SDK opens a BiRequestStream and sends a ConnectionSetupRequest. The
// gRPC layer registers the connection's send function in the
// ConnectionRegistry, keyed by the client's IP address (extracted from the
// HTTP request). When the SDK sends a ConfigBatchListenRequest or
// SubscribeServiceRequest via the unary Request RPC, the handler reads the
// client IP from the request metadata and calls TrackConfigSubscription /
// TrackServiceSubscription to associate the IP with the subscribed keys.
//
// When a config or service changes, the service layer calls the push
// callback. The PushService looks up the subscribed client IPs and pushes
// the notification payload on each registered connection.
//
// Client IP is the correlation key because the Go SDK does not include the
// connection ID in unary request headers. The SDK's BiRequestStream and
// unary requests originate from the same process, so they share an IP.
type PushService struct {
	registry *grpc.ConnectionRegistry
	config   *configsvc.Service
	naming   *namingsvc.Service

	mu             sync.RWMutex
	configSubs     map[string]map[string]bool // configKey -> set of clientIPs
	serviceSubs    map[string]map[string]bool // serviceKey -> set of clientIPs
	ipConfigSubs   map[string]map[string]bool // clientIP -> set of configKeys
	ipServiceSubs  map[string]map[string]bool // clientIP -> set of serviceKeys
}

// NewPushService creates a PushService wired to the given registry and
// services. Returns nil if registry is nil.
func NewPushService(registry *grpc.ConnectionRegistry, config *configsvc.Service, naming *namingsvc.Service) *PushService {
	if registry == nil {
		return nil
	}
	return &PushService{
		registry:      registry,
		config:        config,
		naming:        naming,
		configSubs:    map[string]map[string]bool{},
		serviceSubs:   map[string]map[string]bool{},
		ipConfigSubs:  map[string]map[string]bool{},
		ipServiceSubs: map[string]map[string]bool{},
	}
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

// TrackConfigSubscription records that a client IP is listening to a
// config. subscribe=false removes the subscription. Called from the gRPC
// ConfigBatchListenRequest handler and the HTTP listener endpoints.
func (p *PushService) TrackConfigSubscription(clientIP, namespaceID, groupName, dataID string, subscribe bool) {
	if p == nil || clientIP == "" {
		return
	}
	ck := configKey(namespaceID, groupName, dataID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if subscribe {
		if p.configSubs[ck] == nil {
			p.configSubs[ck] = map[string]bool{}
		}
		p.configSubs[ck][clientIP] = true
		if p.ipConfigSubs[clientIP] == nil {
			p.ipConfigSubs[clientIP] = map[string]bool{}
		}
		p.ipConfigSubs[clientIP][ck] = true
	} else {
		p.removeSub(p.configSubs, ck, clientIP)
		p.removeSub(p.ipConfigSubs, clientIP, ck)
	}
}

// TrackServiceSubscription records that a client IP is subscribed to a
// service. subscribe=false removes the subscription. Called from the gRPC
// SubscribeServiceRequest handler.
func (p *PushService) TrackServiceSubscription(clientIP, namespaceID, groupName, serviceName string, subscribe bool) {
	if p == nil || clientIP == "" {
		return
	}
	sk := serviceKey(namespaceID, groupName, serviceName)
	p.mu.Lock()
	defer p.mu.Unlock()
	if subscribe {
		if p.serviceSubs[sk] == nil {
			p.serviceSubs[sk] = map[string]bool{}
		}
		p.serviceSubs[sk][clientIP] = true
		if p.ipServiceSubs[clientIP] == nil {
			p.ipServiceSubs[clientIP] = map[string]bool{}
		}
		p.ipServiceSubs[clientIP][sk] = true
	} else {
		p.removeSub(p.serviceSubs, sk, clientIP)
		p.removeSub(p.ipServiceSubs, clientIP, sk)
	}
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

// UnregisterClient removes all subscriptions for a client IP. Called when
// the BiRequestStream closes.
func (p *PushService) UnregisterClient(clientIP string) {
	if p == nil || clientIP == "" {
		return
	}
	p.mu.Lock()
	for ck := range p.ipConfigSubs[clientIP] {
		p.removeSub(p.configSubs, ck, clientIP)
	}
	delete(p.ipConfigSubs, clientIP)
	for sk := range p.ipServiceSubs[clientIP] {
		p.removeSub(p.serviceSubs, sk, clientIP)
	}
	delete(p.ipServiceSubs, clientIP)
	p.mu.Unlock()
	p.registry.Unregister(clientIP)
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
	ips := make([]string, 0, len(p.configSubs[ck]))
	for ip := range p.configSubs[ck] {
		ips = append(ips, ip)
	}
	p.mu.RUnlock()
	if len(ips) == 0 {
		return
	}
	payload := buildConfigChangeNotify(namespaceID, groupName, dataID)
	for _, ip := range ips {
		p.registry.Push(ip, payload)
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
	ips := make([]string, 0, len(p.serviceSubs[sk]))
	for ip := range p.serviceSubs[sk] {
		ips = append(ips, ip)
	}
	p.mu.RUnlock()
	if len(ips) == 0 {
		return
	}
	payload, err := buildNotifySubscriber(p.naming, namespaceID, groupName, serviceName)
	if err != nil {
		return
	}
	for _, ip := range ips {
		p.registry.Push(ip, payload)
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
		"group":  groupName,
		"dataId": dataID,
		"tenant": normalizeNamespace(namespaceID),
		"module": "config",
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
		"serviceInfo": map[string]any{
			"name":      serviceName,
			"groupName": groupName,
			"clusters":  "",
			"hosts":     hosts,
			"valid":     true,
			"allIPs":    false,
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
