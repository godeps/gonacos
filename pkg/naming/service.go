// Package naming implements Nacos v3 service discovery and instance health.
//
// The Service type owns an in-memory registry of services, clusters, instances,
// and subscribers. Ephemeral instances are tracked with heartbeat leases that
// expire when no heartbeat is received within the configured TTL window.
// Persistent instances survive across restarts only after the store adapter is
// wired in; until then they share the same in-memory map.
package naming

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultClusterName matches the Nacos default cluster name when none is
	// specified by the client.
	DefaultClusterName = "DEFAULT"
	// DefaultGroupName matches the Nacos default group name when none is
	// specified by the client.
	DefaultGroupName = "DEFAULT_GROUP"
	// EphemeralLeaseInterval is the default heartbeat TTL for ephemeral
	// instances. The Nacos default client heartbeats every 5 seconds; the
	// server marks an instance unhealthy after 15 seconds without a beat.
	EphemeralLeaseInterval = 15 * time.Second
	// MaxServiceNameLength mirrors the Nacos service name validation ceiling.
	MaxServiceNameLength = 512
	// MaxGroupNameLength mirrors the Nacos group name validation ceiling.
	MaxGroupNameLength = 128
	// DefaultProtectThreshold is the Nacos default protect threshold.
	DefaultProtectThreshold = 0.0
)

var (
	ErrMissingNamespaceID  = errors.New("namespaceId is required")
	ErrMissingGroupName    = errors.New("groupName is required")
	ErrMissingServiceName  = errors.New("serviceName is required")
	ErrMissingClusterName  = errors.New("clusterName is required")
	ErrMissingInstanceID   = errors.New("instance ip and port are required")
	ErrInvalidServiceName  = errors.New("invalid serviceName")
	ErrInvalidGroupName    = errors.New("invalid groupName")
	ErrInvalidClusterName  = errors.New("invalid clusterName")
	ErrInvalidInstancePort = errors.New("invalid instance port")
	ErrInvalidWeight       = errors.New("invalid weight")
	ErrInvalidThreshold    = errors.New("invalid protectThreshold")
	ErrServiceExists       = errors.New("service already exists")
	ErrServiceNotFound     = errors.New("service not found")
	ErrClusterNotFound     = errors.New("cluster not found")
	ErrInstanceNotFound    = errors.New("instance not found")
	ErrMetadataInvalid     = errors.New("metadata format invalid")
)

// Service owns the in-memory naming registry. All methods are safe for
// concurrent use.
type Service struct {
	mu        sync.RWMutex
	services  map[string]*serviceEntry
	heartbeat *leaseTracker
	syncFunc  func(action string, payload []byte) error
	pushFunc  func(namespaceID, groupName, serviceName string)
}

// SetSyncFunc installs a hook invoked after local writes. Remote applies
// bypass the hook to avoid infinite loops.
func (s *Service) SetSyncFunc(f func(action string, payload []byte) error) {
	s.syncFunc = f
}

// SetPushFunc installs a hook invoked after local writes to notify the push
// layer that a service's instance set has changed. The push layer uses this
// to push NotifySubscriberRequest frames to subscribed SDK clients. Remote
// applies call this too so subscribers on this node learn about changes
// originated elsewhere.
func (s *Service) SetPushFunc(f func(namespaceID, groupName, serviceName string)) {
	s.pushFunc = f
}

func (s *Service) notifySync(action string, payload any) {
	if s.syncFunc == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.syncFunc(action, data)
}

// notifyPush fires the push callback for a service change. Called after
// instance register/deregister so the push layer can fan out
// NotifySubscriberRequest frames to subscribed SDK clients.
func (s *Service) notifyPush(namespaceID, groupName, serviceName string) {
	if s.pushFunc == nil {
		return
	}
	s.pushFunc(namespaceID, groupName, serviceName)
}

type serviceEntry struct {
	service     ServiceInfo
	clusters    map[string]*Cluster
	instances   map[string]*Instance
	subscribers map[string]*Subscriber
}

// ServiceInfo is the public Nacos-compatible service representation.
type ServiceInfo struct {
	NamespaceID     string            `json:"namespaceId"`
	GroupName       string            `json:"groupName"`
	Name            string            `json:"name"`
	ProtectThreshold float64          `json:"protectThreshold"`
	Ephemeral       bool              `json:"ephemeral"`
	Metadata        map[string]string `json:"metadata"`
	Selector        Selector          `json:"selector"`
	Clusters        []Cluster         `json:"clusters,omitempty"`
	Instances       []Instance        `json:"instances,omitempty"`
	InstanceCount   int               `json:"instanceCount"`
	HealthyInstanceCount int          `json:"healthyInstanceCount"`
	TriggerFlag     bool              `json:"triggerFlag"`
}

// Cluster is the Nacos-compatible cluster representation.
type Cluster struct {
	ServiceRef       ServiceRef        `json:"-"`
	Name             string            `json:"clusterName"`
	CheckPort        int               `json:"checkPort"`
	UseInstancePort4Check bool         `json:"useInstancePort4Check"`
	HealthChecker    map[string]string `json:"healthChecker"`
	Metadata         map[string]string `json:"metadata"`
}

// ServiceRef identifies a service by namespace, group, and name.
type ServiceRef struct {
	NamespaceID string
	GroupName   string
	Name        string
}

// Instance is the Nacos-compatible instance representation.
type Instance struct {
	NamespaceID   string            `json:"namespaceId"`
	GroupName     string            `json:"groupName"`
	ServiceName   string            `json:"serviceName"`
	ClusterName   string            `json:"clusterName"`
	IP            string            `json:"ip"`
	Port          int               `json:"port"`
	Weight        float64           `json:"weight"`
	Healthy       bool              `json:"healthy"`
	Enabled       bool              `json:"enabled"`
	Ephemeral     bool              `json:"ephemeral"`
	Metadata      map[string]string `json:"metadata"`
	InstanceID    string            `json:"instanceId"`
	LastBeatAt    time.Time         `json:"-"`
	Marked        bool              `json:"marked"`
	AppName       string            `json:"appName,omitempty"`
}

// Subscriber is the Nacos-compatible subscriber representation.
type Subscriber struct {
	NamespaceID string            `json:"namespaceId"`
	GroupName   string            `json:"groupName"`
	ServiceName string            `json:"serviceName"`
	ClusterName string            `json:"clusterName,omitempty"`
	Addr        string            `json:"addr"`
	Agent       string            `json:"agent"`
	App         string            `json:"app"`
	ClientID    string            `json:"clientId,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Selector is the Nacos service selector. The default selector is "random".
type Selector struct {
	Type       string            `json:"type"`
	Selectors  string            `json:"selectors,omitempty"`
	Attributes map[string]string `json:"-"`
}

// NewService creates a naming Service with an in-memory registry and an active
// lease tracker. Close the service via Stop to release the lease tracker
// goroutine.
func NewService() *Service {
	s := &Service{
		services:  map[string]*serviceEntry{},
		heartbeat: newLeaseTracker(EphemeralLeaseInterval),
	}
	s.heartbeat.start(s.expireLeases)
	return s
}

// Stop releases the lease tracker goroutine. Safe to call once at shutdown.
func (s *Service) Stop() {
	s.heartbeat.stop()
}

// CreateService registers a new service. Returns ErrServiceExists if the
// service already exists.
func (s *Service) CreateService(namespaceID, groupName, serviceName string, ephemeral bool, protectThreshold float64, metadata map[string]string, selector Selector) error {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}
	if protectThreshold < 0 || protectThreshold > 1 {
		return ErrInvalidThreshold
	}
	selector = normalizeSelector(selector)

	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(namespaceID, groupName, serviceName)
	if _, ok := s.services[key]; ok {
		return ErrServiceExists
	}

	entry := &serviceEntry{
		service: ServiceInfo{
			NamespaceID:      namespaceID,
			GroupName:        groupName,
			Name:             serviceName,
			ProtectThreshold: protectThreshold,
			Ephemeral:        ephemeral,
			Metadata:         copyMetadata(metadata),
			Selector:         selector,
		},
		clusters:    map[string]*Cluster{DefaultClusterName: defaultCluster()},
		instances:   map[string]*Instance{},
		subscribers: map[string]*Subscriber{},
	}
	s.services[key] = entry
	return nil
}

// UpdateService mutates metadata, protectThreshold, and selector for an
// existing service.
func (s *Service) UpdateService(namespaceID, groupName, serviceName string, ephemeral bool, protectThreshold float64, metadata map[string]string, selector Selector) error {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}
	if protectThreshold < 0 || protectThreshold > 1 {
		return ErrInvalidThreshold
	}
	selector = normalizeSelector(selector)

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return ErrServiceNotFound
	}
	entry.service.ProtectThreshold = protectThreshold
	entry.service.Ephemeral = ephemeral
	entry.service.Metadata = copyMetadata(metadata)
	entry.service.Selector = selector
	return nil
}

// DeleteService removes a service and all of its clusters and instances.
func (s *Service) DeleteService(namespaceID, groupName, serviceName string) error {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(namespaceID, groupName, serviceName)
	entry, ok := s.services[key]
	if !ok {
		return ErrServiceNotFound
	}
	for _, inst := range entry.instances {
		s.heartbeat.remove(inst.InstanceID)
	}
	delete(s.services, key)
	return nil
}

// GetService returns the service detail with cluster and instance snapshots.
func (s *Service) GetService(namespaceID, groupName, serviceName string) (ServiceInfo, error) {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return ServiceInfo{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return ServiceInfo{}, ErrServiceNotFound
	}
	return snapshotService(entry), nil
}

// ListServices returns a paginated snapshot of services that match the
// namespace and optional group/service name filter substrings.
func (s *Service) ListServices(namespaceID, groupNameParam, serviceNameParam string, pageNo, pageSize int, ignoreEmpty, withInstances bool) (ServicePage, error) {
	namespaceID = trim(namespaceID)
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []*serviceEntry
	for _, entry := range s.services {
		if entry.service.NamespaceID != namespaceID {
			continue
		}
		if groupNameParam != "" && !strings.Contains(strings.ToLower(entry.service.GroupName), strings.ToLower(groupNameParam)) {
			continue
		}
		if serviceNameParam != "" && !strings.Contains(strings.ToLower(entry.service.Name), strings.ToLower(serviceNameParam)) {
			continue
		}
		if ignoreEmpty && len(entry.instances) == 0 {
			continue
		}
		matches = append(matches, entry)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].service.GroupName != matches[j].service.GroupName {
			return matches[i].service.GroupName < matches[j].service.GroupName
		}
		return matches[i].service.Name < matches[j].service.Name
	})

	total := len(matches)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	items := make([]ServiceInfo, 0, end-start)
	for _, entry := range matches[start:end] {
		info := snapshotService(entry)
		if !withInstances {
			info.Instances = nil
		}
		items = append(items, info)
	}
	return ServicePage{
		Count:    total,
		Services: items,
	}, nil
}

// ServicePage is the Nacos-compatible list response shape.
type ServicePage struct {
	Count    int           `json:"count"`
	Services []ServiceInfo `json:"serviceList"`
}

// UpdateCluster mutates the cluster metadata and health checker configuration.
func (s *Service) UpdateCluster(namespaceID, groupName, serviceName, clusterName string, checkPort int, useInstancePort4Check bool, healthChecker, metadata map[string]string) error {
	namespaceID, groupName, serviceName, clusterName = trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}
	if clusterName == "" {
		return ErrMissingClusterName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return ErrServiceNotFound
	}
	cluster, ok := entry.clusters[clusterName]
	if !ok {
		cluster = defaultCluster()
		cluster.Name = clusterName
		entry.clusters[clusterName] = cluster
	}
	if checkPort > 0 {
		cluster.CheckPort = checkPort
	}
	cluster.UseInstancePort4Check = useInstancePort4Check
	if healthChecker != nil {
		cluster.HealthChecker = copyMetadata(healthChecker)
	}
	if metadata != nil {
		cluster.Metadata = copyMetadata(metadata)
	}
	return nil
}

// ListClusters returns the clusters registered for a service.
func (s *Service) ListClusters(namespaceID, groupName, serviceName string) ([]Cluster, error) {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return nil, ErrServiceNotFound
	}
	clusters := make([]Cluster, 0, len(entry.clusters))
	for _, cluster := range entry.clusters {
		clusters = append(clusters, *cluster)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })
	return clusters, nil
}

// RegisterInstance adds or replaces an instance. Ephemeral instances are
// tracked by the lease tracker; persistent instances are stored without a lease.
func (s *Service) RegisterInstance(inst Instance) (Instance, error) {
	inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName = trim(inst.NamespaceID), trim(inst.GroupName), trim(inst.ServiceName), trim(inst.ClusterName)
	if err := validateInstance(inst); err != nil {
		return Instance{}, err
	}
	if inst.ClusterName == "" {
		inst.ClusterName = DefaultClusterName
	}
	if inst.Weight <= 0 {
		inst.Weight = 1
	}
	if inst.InstanceID == "" {
		inst.InstanceID = buildInstanceID(inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName, inst.IP, inst.Port)
	}
	inst.LastBeatAt = time.Now()

	s.mu.Lock()
	key := serviceKey(inst.NamespaceID, inst.GroupName, inst.ServiceName)
	entry, ok := s.services[key]
	if !ok {
		// Nacos auto-creates the service on instance registration.
		entry = &serviceEntry{
			service: ServiceInfo{
				NamespaceID: inst.NamespaceID,
				GroupName:   inst.GroupName,
				Name:        inst.ServiceName,
				Ephemeral:   inst.Ephemeral,
				Metadata:    map[string]string{},
				Selector:    Selector{Type: SelectorRandom},
			},
			clusters:    map[string]*Cluster{DefaultClusterName: defaultCluster()},
			instances:   map[string]*Instance{},
			subscribers: map[string]*Subscriber{},
		}
		s.services[key] = entry
	}
	if _, ok := entry.clusters[inst.ClusterName]; !ok {
		cluster := defaultCluster()
		cluster.Name = inst.ClusterName
		entry.clusters[inst.ClusterName] = cluster
	}

	existing, ok := entry.instances[inst.InstanceID]
	if ok {
		mergeInstanceFields(existing, &inst)
	}
	if inst.Ephemeral {
		s.heartbeat.track(inst.InstanceID, inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName)
	}
	entry.instances[inst.InstanceID] = &inst
	s.mu.Unlock()

	s.notifySync("register", inst)
	s.notifyPush(inst.NamespaceID, inst.GroupName, inst.ServiceName)
	return inst, nil
}

// ApplyRemoteRegister stores an instance received from another node without
// re-publishing the change. Ephemeral instances are not tracked by the
// heartbeat lease on the remote node to avoid premature expiry.
func (s *Service) ApplyRemoteRegister(inst Instance) (Instance, error) {
	inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName = trim(inst.NamespaceID), trim(inst.GroupName), trim(inst.ServiceName), trim(inst.ClusterName)
	if err := validateInstance(inst); err != nil {
		return Instance{}, err
	}
	if inst.ClusterName == "" {
		inst.ClusterName = DefaultClusterName
	}
	if inst.InstanceID == "" {
		inst.InstanceID = buildInstanceID(inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName, inst.IP, inst.Port)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceKey(inst.NamespaceID, inst.GroupName, inst.ServiceName)
	entry, ok := s.services[key]
	if !ok {
		entry = &serviceEntry{
			service:   ServiceInfo{NamespaceID: inst.NamespaceID, GroupName: inst.GroupName, Name: inst.ServiceName, Ephemeral: inst.Ephemeral, Metadata: map[string]string{}, Selector: Selector{Type: SelectorRandom}},
			clusters:  map[string]*Cluster{DefaultClusterName: defaultCluster()},
			instances: map[string]*Instance{},
		}
		s.services[key] = entry
	}
	if _, ok := entry.clusters[inst.ClusterName]; !ok {
		cluster := defaultCluster()
		cluster.Name = inst.ClusterName
		entry.clusters[inst.ClusterName] = cluster
	}
	if existing, ok := entry.instances[inst.InstanceID]; ok {
		mergeInstanceFields(existing, &inst)
	}
	entry.instances[inst.InstanceID] = &inst
	return inst, nil
}

// UpdateInstance mutates an existing instance's mutable fields.
func (s *Service) UpdateInstance(inst Instance) error {
	inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName = trim(inst.NamespaceID), trim(inst.GroupName), trim(inst.ServiceName), trim(inst.ClusterName)
	if err := validateInstance(inst); err != nil {
		return err
	}
	if inst.ClusterName == "" {
		inst.ClusterName = DefaultClusterName
	}
	if inst.InstanceID == "" {
		inst.InstanceID = buildInstanceID(inst.NamespaceID, inst.GroupName, inst.ServiceName, inst.ClusterName, inst.IP, inst.Port)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(inst.NamespaceID, inst.GroupName, inst.ServiceName)]
	if !ok {
		return ErrServiceNotFound
	}
	existing, ok := entry.instances[inst.InstanceID]
	if !ok {
		return ErrInstanceNotFound
	}
	mergeInstanceFields(existing, &inst)
	existing.LastBeatAt = time.Now()
	return nil
}

// UpdateInstanceHealth sets the healthy flag on an instance and records the
// current time as the last beat. Used by the admin health-update endpoint.
func (s *Service) UpdateInstanceHealth(namespaceID, groupName, serviceName, clusterName, ip string, port int, healthy bool) error {
	namespaceID, groupName, serviceName, clusterName = trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName)
	if clusterName == "" {
		clusterName = DefaultClusterName
	}
	if ip == "" || port == 0 {
		return ErrMissingInstanceID
	}
	instanceID := buildInstanceID(namespaceID, groupName, serviceName, clusterName, ip, port)

	s.mu.Lock()
	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		s.mu.Unlock()
		return ErrServiceNotFound
	}
	existing, ok := entry.instances[instanceID]
	if !ok {
		s.mu.Unlock()
		return ErrInstanceNotFound
	}
	existing.Healthy = healthy
	existing.LastBeatAt = time.Now()
	s.mu.Unlock()

	s.notifyPush(namespaceID, groupName, serviceName)
	return nil
}

// DeregisterInstance removes an instance. If the instance does not exist this
// returns ErrInstanceNotFound.
func (s *Service) DeregisterInstance(namespaceID, groupName, serviceName, clusterName, ip string, port int, instanceID string) error {
	namespaceID, groupName, serviceName, clusterName = trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName)
	if clusterName == "" {
		clusterName = DefaultClusterName
	}
	if instanceID == "" {
		if err := validateInstanceIdent(namespaceID, groupName, serviceName, clusterName, ip, port); err != nil {
			return err
		}
		instanceID = buildInstanceID(namespaceID, groupName, serviceName, clusterName, ip, port)
	}

	s.mu.Lock()
	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		s.mu.Unlock()
		return ErrInstanceNotFound
	}
	if _, ok := entry.instances[instanceID]; !ok {
		s.mu.Unlock()
		return ErrInstanceNotFound
	}
	delete(entry.instances, instanceID)
	s.heartbeat.remove(instanceID)
	s.mu.Unlock()

	s.notifySync("deregister", map[string]string{
		"namespaceId": namespaceID,
		"groupName":   groupName,
		"serviceName": serviceName,
		"clusterName": clusterName,
		"ip":          ip,
		"port":        strconv.Itoa(port),
		"instanceId":  instanceID,
	})
	s.notifyPush(namespaceID, groupName, serviceName)
	return nil
}

// ApplyRemoteDeregister removes an instance received from another node without
// re-publishing the change.
func (s *Service) ApplyRemoteDeregister(namespaceID, groupName, serviceName, clusterName, ip string, port int, instanceID string) error {
	namespaceID, groupName, serviceName, clusterName = trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName)
	if clusterName == "" {
		clusterName = DefaultClusterName
	}
	if instanceID == "" {
		instanceID = buildInstanceID(namespaceID, groupName, serviceName, clusterName, ip, port)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return nil
	}
	delete(entry.instances, instanceID)
	return nil
}

// ListInstances returns all instances for a service, optionally filtered by
// cluster. Healthy=false includes unhealthy instances.
func (s *Service) ListInstances(namespaceID, groupName, serviceName, clusterName string, healthyOnly bool) ([]Instance, error) {
	namespaceID, groupName, serviceName, clusterName = trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return nil, ErrServiceNotFound
	}
	instances := make([]Instance, 0, len(entry.instances))
	for _, inst := range entry.instances {
		if clusterName != "" && inst.ClusterName != clusterName {
			continue
		}
		if healthyOnly && !inst.Healthy {
			continue
		}
		instances = append(instances, *inst)
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].ClusterName != instances[j].ClusterName {
			return instances[i].ClusterName < instances[j].ClusterName
		}
		if instances[i].IP != instances[j].IP {
			return instances[i].IP < instances[j].IP
		}
		return instances[i].Port < instances[j].Port
	})
	return instances, nil
}

// ListInstancesPaginated returns a paginated instance list.
func (s *Service) ListInstancesPaginated(namespaceID, groupName, serviceName, clusterName string, pageNo, pageSize int, healthyOnly bool) (InstancePage, error) {
	instances, err := s.ListInstances(namespaceID, groupName, serviceName, clusterName, healthyOnly)
	if err != nil {
		return InstancePage{}, err
	}
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	total := len(instances)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return InstancePage{
		Count:     total,
		Instances: instances[start:end],
	}, nil
}

// InstancePage is the Nacos-compatible paginated instance list.
type InstancePage struct {
	Count     int        `json:"count"`
	Instances []Instance `json:"list"`
}

// BatchUpdateInstanceMetadata applies metadata to a batch of instances.
func (s *Service) BatchUpdateInstanceMetadata(namespaceID, groupName, serviceName string, updates []InstanceMetadataUpdate) (BatchResult, error) {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return BatchResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return BatchResult{}, ErrServiceNotFound
	}
	result := BatchResult{}
	for _, update := range updates {
		inst, ok := entry.instances[update.InstanceID]
		if !ok {
			result.Failed = append(result.Failed, update.InstanceID)
			continue
		}
		if inst.Metadata == nil {
			inst.Metadata = map[string]string{}
		}
		for k, v := range update.Metadata {
			inst.Metadata[k] = v
		}
		result.Updated = append(result.Updated, update.InstanceID)
	}
	return result, nil
}

// BatchDeleteInstanceMetadata removes metadata keys from a batch of instances.
func (s *Service) BatchDeleteInstanceMetadata(namespaceID, groupName, serviceName string, ids []string, keys []string) (BatchResult, error) {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return BatchResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return BatchResult{}, ErrServiceNotFound
	}
	result := BatchResult{}
	for _, id := range ids {
		inst, ok := entry.instances[id]
		if !ok {
			result.Failed = append(result.Failed, id)
			continue
		}
		for _, key := range keys {
			delete(inst.Metadata, key)
		}
		result.Updated = append(result.Updated, id)
	}
	return result, nil
}

// AddSubscriber records a subscriber against a service. Subscribers are
// ephemeral and live in memory.
func (s *Service) AddSubscriber(sub Subscriber) error {
	sub.NamespaceID, sub.GroupName, sub.ServiceName, sub.ClusterName = trim(sub.NamespaceID), trim(sub.GroupName), trim(sub.ServiceName), trim(sub.ClusterName)
	if err := validateServiceIdent(sub.NamespaceID, sub.GroupName, sub.ServiceName); err != nil {
		return err
	}
	if sub.Addr == "" {
		return errors.New("subscriber addr is required")
	}
	if sub.ClusterName == "" {
		sub.ClusterName = DefaultClusterName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(sub.NamespaceID, sub.GroupName, sub.ServiceName)]
	if !ok {
		// Nacos auto-creates the service on subscriber registration.
		entry = &serviceEntry{
			service: ServiceInfo{
				NamespaceID: sub.NamespaceID,
				GroupName:   sub.GroupName,
				Name:        sub.ServiceName,
				Metadata:    map[string]string{},
				Selector:    Selector{Type: SelectorRandom},
			},
			clusters:    map[string]*Cluster{DefaultClusterName: defaultCluster()},
			instances:   map[string]*Instance{},
			subscribers: map[string]*Subscriber{},
		}
		s.services[serviceKey(sub.NamespaceID, sub.GroupName, sub.ServiceName)] = entry
	}
	entry.subscribers[subscriberKey(sub)] = &sub
	return nil
}

// RemoveSubscriber removes a subscriber from a service.
func (s *Service) RemoveSubscriber(namespaceID, groupName, serviceName, addr string) error {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}
	if addr == "" {
		return errors.New("subscriber addr is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return ErrServiceNotFound
	}
	for key := range entry.subscribers {
		if entry.subscribers[key].Addr == addr {
			delete(entry.subscribers, key)
			return nil
		}
	}
	return nil
}

// ListSubscribers returns a paginated snapshot of subscribers for a service.
func (s *Service) ListSubscribers(namespaceID, groupName, serviceName string, pageNo, pageSize int) (SubscriberPage, error) {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return SubscriberPage{}, err
	}
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return SubscriberPage{}, ErrServiceNotFound
	}
	subs := make([]Subscriber, 0, len(entry.subscribers))
	for _, sub := range entry.subscribers {
		subs = append(subs, *sub)
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Addr < subs[j].Addr })

	total := len(subs)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return SubscriberPage{
		Count:      total,
		Subscribers: subs[start:end],
	}, nil
}

// SubscriberPage is the Nacos-compatible paginated subscriber list.
type SubscriberPage struct {
	Count       int          `json:"totalCount"`
	Subscribers []Subscriber `json:"subscribers"`
}

// Heartbeat refreshes an ephemeral instance lease. Returns ErrInstanceNotFound
// if the instance is not registered.
func (s *Service) Heartbeat(namespaceID, groupName, serviceName, clusterName, ip string, port int, instanceID string) error {
	if instanceID == "" {
		instanceID = buildInstanceID(trim(namespaceID), trim(groupName), trim(serviceName), trim(clusterName), ip, port)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.services[serviceKey(trim(namespaceID), trim(groupName), trim(serviceName))]
	if !ok {
		return ErrInstanceNotFound
	}
	inst, ok := entry.instances[instanceID]
	if !ok {
		return ErrInstanceNotFound
	}
	inst.LastBeatAt = time.Now()
	inst.Healthy = true
	s.heartbeat.refresh(instanceID)
	return nil
}

// expireLeases is called by the lease tracker when an instance lease expires.
func (s *Service) expireLeases(ids []string) {
	if len(ids) == 0 {
		return
	}
	type svcKey struct{ namespaceID, groupName, serviceName string }
	affected := map[svcKey]bool{}
	s.mu.Lock()
	for _, id := range ids {
		lease := s.heartbeat.lookup(id)
		if lease == nil {
			continue
		}
		entry, ok := s.services[serviceKey(lease.NamespaceID, lease.GroupName, lease.ServiceName)]
		if !ok {
			continue
		}
		inst, ok := entry.instances[id]
		if !ok {
			continue
		}
		inst.Healthy = false
		affected[svcKey{lease.NamespaceID, lease.GroupName, lease.ServiceName}] = true
	}
	s.mu.Unlock()
	for k := range affected {
		s.notifyPush(k.namespaceID, k.groupName, k.serviceName)
	}
}

// InstanceMetadataUpdate is a single batch metadata update entry.
type InstanceMetadataUpdate struct {
	InstanceID string            `json:"instanceId"`
	Metadata   map[string]string `json:"metadata"`
}

// BatchResult reports the success and failure of a batch operation.
type BatchResult struct {
	Updated []string `json:"updated"`
	Failed  []string `json:"failed"`
}

// snapshotService returns a deep copy of a service entry suitable for the API.
func snapshotService(entry *serviceEntry) ServiceInfo {
	info := entry.service
	info.Clusters = make([]Cluster, 0, len(entry.clusters))
	for _, cluster := range entry.clusters {
		info.Clusters = append(info.Clusters, *cluster)
	}
	sort.Slice(info.Clusters, func(i, j int) bool { return info.Clusters[i].Name < info.Clusters[j].Name })

	info.Instances = make([]Instance, 0, len(entry.instances))
	healthy := 0
	for _, inst := range entry.instances {
		info.Instances = append(info.Instances, *inst)
		if inst.Healthy && inst.Enabled {
			healthy++
		}
	}
	sort.Slice(info.Instances, func(i, j int) bool {
		if info.Instances[i].ClusterName != info.Instances[j].ClusterName {
			return info.Instances[i].ClusterName < info.Instances[j].ClusterName
		}
		if info.Instances[i].IP != info.Instances[j].IP {
			return info.Instances[i].IP < info.Instances[j].IP
		}
		return info.Instances[i].Port < info.Instances[j].Port
	})
	info.InstanceCount = len(info.Instances)
	info.HealthyInstanceCount = healthy
	return info
}

// mergeInstanceFields applies mutable fields from src to dst. The InstanceID,
// NamespaceID, GroupName, ServiceName, and ClusterName are preserved on dst.
func mergeInstanceFields(dst *Instance, src *Instance) {
	if src.Weight > 0 {
		dst.Weight = src.Weight
	}
	if src.AppName != "" {
		dst.AppName = src.AppName
	}
	dst.Healthy = src.Healthy
	dst.Enabled = src.Enabled
	dst.Ephemeral = src.Ephemeral
	dst.Marked = src.Marked
	if src.Metadata != nil {
		dst.Metadata = copyMetadata(src.Metadata)
	}
}

func defaultCluster() *Cluster {
	return &Cluster{
		Name:                  DefaultClusterName,
		CheckPort:             80,
		UseInstancePort4Check: true,
		HealthChecker:         map[string]string{"type": "none"},
		Metadata:              map[string]string{},
	}
}

func serviceKey(namespaceID, groupName, serviceName string) string {
	return namespaceID + "|" + groupName + "|" + serviceName
}

func subscriberKey(sub Subscriber) string {
	return sub.Addr + "|" + sub.ClusterName + "|" + sub.ClientID
}

func buildInstanceID(namespaceID, groupName, serviceName, clusterName, ip string, port int) string {
	return namespaceID + ":" + groupName + ":" + serviceName + ":" + clusterName + ":" + ip + ":" + strconv.Itoa(port)
}

func validateServiceIdent(namespaceID, groupName, serviceName string) error {
	if namespaceID == "" {
		return ErrMissingNamespaceID
	}
	if groupName == "" {
		return ErrMissingGroupName
	}
	if serviceName == "" {
		return ErrMissingServiceName
	}
	if len(serviceName) > MaxServiceNameLength {
		return ErrInvalidServiceName
	}
	if len(groupName) > MaxGroupNameLength {
		return ErrInvalidGroupName
	}
	return nil
}

func validateInstanceIdent(namespaceID, groupName, serviceName, clusterName, ip string, port int) error {
	if err := validateServiceIdent(namespaceID, groupName, serviceName); err != nil {
		return err
	}
	if clusterName == "" {
		return ErrMissingClusterName
	}
	if ip == "" || port <= 0 {
		return ErrMissingInstanceID
	}
	return nil
}

func validateInstance(inst Instance) error {
	if err := validateServiceIdent(inst.NamespaceID, inst.GroupName, inst.ServiceName); err != nil {
		return err
	}
	if inst.IP == "" || inst.Port <= 0 {
		return ErrMissingInstanceID
	}
	if inst.Weight < 0 {
		return ErrInvalidWeight
	}
	return nil
}

func copyMetadata(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func trim(s string) string { return strings.TrimSpace(s) }

// ParsePort converts a form port string to an int, returning 0 on error.
func ParsePort(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// ParseWeight converts a form weight string to a float64, returning 0 on error.
func ParseWeight(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// ParseThreshold converts a form threshold string to a float64.
func ParseThreshold(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// ParseBool converts a Nacos form bool ("true"/"false"/empty) to bool.
func ParseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// ParseMetadata parses a Nacos metadata form value. Nacos encodes metadata as
// "k1=v1,k2=v2" or JSON. Empty input returns an empty map.
func ParseMetadata(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	if strings.HasPrefix(s, "{") {
		if err := parseJSONMetadata(s, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMetadataInvalid, pair)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// FormatMetadata serializes a metadata map as "k1=v1,k2=v2".
func FormatMetadata(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+m[k])
	}
	return strings.Join(pairs, ",")
}

func parseJSONMetadata(s string, out map[string]string) error {
	var raw map[string]string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return err
	}
	for k, v := range raw {
		out[k] = v
	}
	return nil
}
