package naming

import (
	"sort"
	"time"
)

// namingServiceSnapshot captures one service entry: its metadata, clusters,
// instances, and subscribers. Ephemeral lease state is not preserved; the
// lease tracker re-establishes itself from LastBeatAt on restore.
type namingServiceSnapshot struct {
	NamespaceID string           `json:"namespaceId"`
	GroupName   string           `json:"groupName"`
	ServiceName string           `json:"serviceName"`
	Service     serviceInfoSnap  `json:"service"`
	Clusters    []clusterSnap    `json:"clusters"`
	Instances   []instanceSnap   `json:"instances"`
	Subscribers []subscriberSnap `json:"subscribers"`
}

type serviceInfoSnap struct {
	ProtectThreshold float64           `json:"protectThreshold"`
	Ephemeral        bool              `json:"ephemeral"`
	Metadata         map[string]string `json:"metadata"`
	SelectorType     string            `json:"selectorType"`
	SelectorText     string            `json:"selectorText"`
}

type clusterSnap struct {
	Name                  string            `json:"clusterName"`
	CheckPort             int               `json:"checkPort"`
	UseInstancePort4Check bool              `json:"useInstancePort4Check"`
	HealthChecker         map[string]string `json:"healthChecker"`
	Metadata              map[string]string `json:"metadata"`
}

type instanceSnap struct {
	NamespaceID string            `json:"namespaceId"`
	GroupName   string            `json:"groupName"`
	ServiceName string            `json:"serviceName"`
	ClusterName string            `json:"clusterName"`
	IP          string            `json:"ip"`
	Port        int               `json:"port"`
	Weight      float64           `json:"weight"`
	Healthy     bool              `json:"healthy"`
	Enabled     bool              `json:"enabled"`
	Ephemeral   bool              `json:"ephemeral"`
	Metadata    map[string]string `json:"metadata"`
	InstanceID  string            `json:"instanceId"`
	LastBeatAt  int64             `json:"lastBeatAt"`
	Marked      bool              `json:"marked"`
	AppName     string            `json:"appName"`
}

type subscriberSnap struct {
	NamespaceID string            `json:"namespaceId"`
	GroupName   string            `json:"groupName"`
	ServiceName string            `json:"serviceName"`
	ClusterName string            `json:"clusterName"`
	Addr        string            `json:"addr"`
	Agent       string            `json:"agent"`
	App         string            `json:"app"`
	ClientID    string            `json:"clientId"`
	Metadata    map[string]string `json:"metadata"`
}

// SnapshotKey identifies the naming service in backup envelopes.
func (s *Service) SnapshotKey() string { return "naming" }

// Snapshot returns all services, clusters, instances, and subscribers.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]namingServiceSnapshot, 0, len(s.services))
	for _, entry := range s.services {
		out = append(out, snapshotEntry(entry))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NamespaceID != out[j].NamespaceID {
			return out[i].NamespaceID < out[j].NamespaceID
		}
		if out[i].GroupName != out[j].GroupName {
			return out[i].GroupName < out[j].GroupName
		}
		return out[i].ServiceName < out[j].ServiceName
	})
	return out, nil
}

func snapshotEntry(entry *serviceEntry) namingServiceSnapshot {
	snap := namingServiceSnapshot{
		NamespaceID: entry.service.NamespaceID,
		GroupName:   entry.service.GroupName,
		ServiceName: entry.service.Name,
		Service: serviceInfoSnap{
			ProtectThreshold: entry.service.ProtectThreshold,
			Ephemeral:        entry.service.Ephemeral,
			Metadata:         copyStringMap(entry.service.Metadata),
			SelectorType:     entry.service.Selector.Type,
			SelectorText:     entry.service.Selector.Selectors,
		},
	}
	for _, c := range entry.clusters {
		snap.Clusters = append(snap.Clusters, clusterSnap{
			Name:                  c.Name,
			CheckPort:             c.CheckPort,
			UseInstancePort4Check: c.UseInstancePort4Check,
			HealthChecker:         copyStringMap(c.HealthChecker),
			Metadata:              copyStringMap(c.Metadata),
		})
	}
	sort.Slice(snap.Clusters, func(i, j int) bool { return snap.Clusters[i].Name < snap.Clusters[j].Name })
	for _, inst := range entry.instances {
		lastBeat := int64(0)
		if !inst.LastBeatAt.IsZero() {
			lastBeat = inst.LastBeatAt.UnixMilli()
		}
		snap.Instances = append(snap.Instances, instanceSnap{
			NamespaceID: inst.NamespaceID,
			GroupName:   inst.GroupName,
			ServiceName: inst.ServiceName,
			ClusterName: inst.ClusterName,
			IP:          inst.IP,
			Port:        inst.Port,
			Weight:      inst.Weight,
			Healthy:     inst.Healthy,
			Enabled:     inst.Enabled,
			Ephemeral:   inst.Ephemeral,
			Metadata:    copyStringMap(inst.Metadata),
			InstanceID:  inst.InstanceID,
			LastBeatAt:  lastBeat,
			Marked:      inst.Marked,
			AppName:     inst.AppName,
		})
	}
	sort.Slice(snap.Instances, func(i, j int) bool {
		if snap.Instances[i].ClusterName != snap.Instances[j].ClusterName {
			return snap.Instances[i].ClusterName < snap.Instances[j].ClusterName
		}
		if snap.Instances[i].IP != snap.Instances[j].IP {
			return snap.Instances[i].IP < snap.Instances[j].IP
		}
		return snap.Instances[i].Port < snap.Instances[j].Port
	})
	for _, sub := range entry.subscribers {
		snap.Subscribers = append(snap.Subscribers, subscriberSnap{
			NamespaceID: sub.NamespaceID,
			GroupName:   sub.GroupName,
			ServiceName: sub.ServiceName,
			ClusterName: sub.ClusterName,
			Addr:        sub.Addr,
			Agent:       sub.Agent,
			App:         sub.App,
			ClientID:    sub.ClientID,
			Metadata:    copyStringMap(sub.Metadata),
		})
	}
	sort.Slice(snap.Subscribers, func(i, j int) bool {
		if snap.Subscribers[i].Addr != snap.Subscribers[j].Addr {
			return snap.Subscribers[i].Addr < snap.Subscribers[j].Addr
		}
		return snap.Subscribers[i].ClientID < snap.Subscribers[j].ClientID
	})
	return snap
}

// Restore replaces all naming state. Ephemeral instances are re-loaded with
// their last beat timestamp so the lease tracker can re-evaluate expiry.
func (s *Service) Restore(data any) error {
	items, ok := data.([]any)
	if !ok {
		return errNamingSnapshotShape
	}
	services := make([]*serviceEntry, 0, len(items))
	for _, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			return errNamingSnapshotShape
		}
		entry, err := decodeNamingEntry(m)
		if err != nil {
			return err
		}
		services = append(services, entry)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = map[string]*serviceEntry{}
	for _, entry := range services {
		key := serviceKey(entry.service.NamespaceID, entry.service.GroupName, entry.service.Name)
		s.services[key] = entry
	}
	return nil
}

func decodeNamingEntry(m map[string]any) (*serviceEntry, error) {
	entry := &serviceEntry{
		service: ServiceInfo{
			NamespaceID: getString(m, "namespaceId"),
			GroupName:   getString(m, "groupName"),
			Name:        getString(m, "serviceName"),
		},
		clusters:    map[string]*Cluster{},
		instances:   map[string]*Instance{},
		subscribers: map[string]*Subscriber{},
	}
	if svc, ok := m["service"].(map[string]any); ok {
		entry.service.ProtectThreshold = getFloat(svc, "protectThreshold")
		entry.service.Ephemeral = getBool(svc, "ephemeral")
		entry.service.Metadata = getStringMap(svc, "metadata")
		entry.service.Selector = Selector{
			Type:      getString(svc, "selectorType"),
			Selectors: getString(svc, "selectorText"),
		}
	}
	if raw, ok := m["clusters"].([]any); ok {
		for _, c := range raw {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			cluster := &Cluster{
				Name:                  getString(cm, "clusterName"),
				CheckPort:             int(getFloat(cm, "checkPort")),
				UseInstancePort4Check: getBool(cm, "useInstancePort4Check"),
				HealthChecker:         getStringMap(cm, "healthChecker"),
				Metadata:              getStringMap(cm, "metadata"),
			}
			entry.clusters[cluster.Name] = cluster
		}
	}
	if raw, ok := m["instances"].([]any); ok {
		for _, i := range raw {
			im, ok := i.(map[string]any)
			if !ok {
				continue
			}
			inst := &Instance{
				NamespaceID: getString(im, "namespaceId"),
				GroupName:   getString(im, "groupName"),
				ServiceName: getString(im, "serviceName"),
				ClusterName: getString(im, "clusterName"),
				IP:          getString(im, "ip"),
				Port:        int(getFloat(im, "port")),
				Weight:      getFloat(im, "weight"),
				Healthy:     getBool(im, "healthy"),
				Enabled:     getBool(im, "enabled"),
				Ephemeral:   getBool(im, "ephemeral"),
				Metadata:    getStringMap(im, "metadata"),
				InstanceID:  getString(im, "instanceId"),
				Marked:      getBool(im, "marked"),
				AppName:     getString(im, "appName"),
			}
			if ts := getFloat(im, "lastBeatAt"); ts > 0 {
				inst.LastBeatAt = time.UnixMilli(int64(ts))
			}
			entry.instances[inst.InstanceID] = inst
		}
	}
	if raw, ok := m["subscribers"].([]any); ok {
		for _, su := range raw {
			sm, ok := su.(map[string]any)
			if !ok {
				continue
			}
			sub := &Subscriber{
				NamespaceID: getString(sm, "namespaceId"),
				GroupName:   getString(sm, "groupName"),
				ServiceName: getString(sm, "serviceName"),
				ClusterName: getString(sm, "clusterName"),
				Addr:        getString(sm, "addr"),
				Agent:       getString(sm, "agent"),
				App:         getString(sm, "app"),
				ClientID:    getString(sm, "clientId"),
				Metadata:    getStringMap(sm, "metadata"),
			}
			entry.subscribers[subscriberKey(*sub)] = sub
		}
	}
	return entry, nil
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func getStringMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

var errNamingSnapshotShape = snapshotShapeError("naming snapshot shape mismatch")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
