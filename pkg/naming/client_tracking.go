package naming

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// ClientInfo is the summary of a connected SDK client derived from
// registered instances and subscribers.
type ClientInfo struct {
	ClientID  string `json:"clientId"`
	Addr      string `json:"addr"`
	Agent     string `json:"agent"`
	App       string `json:"app"`
	Namespace string `json:"namespace,omitempty"`
	Heartbeat int64  `json:"heartbeat,omitempty"`
}

// ClientDetail is the full detail for a single client, including the
// services it publishes (registers instances for) and subscribes to.
type ClientDetail struct {
	ClientInfo
	PublishedServices []ServiceRef `json:"publishedServices"`
	SubscribedServices []ServiceRef `json:"subscribedServices"`
}

// ClientMetric summarizes naming registry counts for the ops/metrics endpoint.
type ClientMetric struct {
	ServiceCount   int `json:"serviceCount"`
	InstanceCount  int `json:"instanceCount"`
	SubscriberCount int `json:"subscriberCount"`
	ClientCount    int `json:"clientCount"`
}

// ListClients returns a deduplicated list of clients derived from
// subscribers (by ClientID) and instances (by IP). The clientID parameter
// filters to a single client when non-empty.
func (s *Service) ListClients(clientID string) []ClientInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	out := make([]ClientInfo, 0)
	for _, entry := range s.services {
		for _, sub := range entry.subscribers {
			id := sub.ClientID
			if id == "" {
				id = sub.Addr
			}
			if clientID != "" && id != clientID {
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, ClientInfo{
				ClientID:  id,
				Addr:      sub.Addr,
				Agent:     sub.Agent,
				App:       sub.App,
				Namespace: sub.NamespaceID,
			})
		}
		for _, inst := range entry.instances {
			id := inst.IP
			if clientID != "" && id != clientID {
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, ClientInfo{
				ClientID: id,
				Addr:     inst.IP,
				App:      inst.AppName,
				Namespace: inst.NamespaceID,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out
}

// GetClient returns the detail for a single client: the services it
// publishes instances for and the services it subscribes to.
func (s *Service) GetClient(clientID string) (ClientDetail, error) {
	if clientID = strings.TrimSpace(clientID); clientID == "" {
		return ClientDetail{}, ErrMissingClientID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	detail := ClientDetail{ClientInfo: ClientInfo{ClientID: clientID}}
	for _, entry := range s.services {
		for _, sub := range entry.subscribers {
			id := sub.ClientID
			if id == "" {
				id = sub.Addr
			}
			if id == clientID {
				detail.Addr = sub.Addr
				detail.Agent = sub.Agent
				detail.App = sub.App
				detail.Namespace = sub.NamespaceID
				detail.SubscribedServices = append(detail.SubscribedServices, ServiceRef{
					NamespaceID: sub.NamespaceID,
					GroupName:   sub.GroupName,
					Name:        sub.ServiceName,
				})
			}
		}
		for _, inst := range entry.instances {
			if inst.IP == clientID || inst.InstanceID == clientID {
				detail.PublishedServices = append(detail.PublishedServices, ServiceRef{
					NamespaceID: inst.NamespaceID,
					GroupName:   inst.GroupName,
					Name:        inst.ServiceName,
				})
			}
		}
	}
	detail.Heartbeat = time.Now().Unix()
	return detail, nil
}

// PublishedServiceList returns the services a client publishes instances for.
func (s *Service) PublishedServiceList(clientID string) []ServiceRef {
	if clientID = strings.TrimSpace(clientID); clientID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ServiceRef
	for _, entry := range s.services {
		for _, inst := range entry.instances {
			if inst.IP == clientID || inst.InstanceID == clientID {
				out = append(out, ServiceRef{
					NamespaceID: inst.NamespaceID,
					GroupName:   inst.GroupName,
					Name:        inst.ServiceName,
				})
			}
		}
	}
	return out
}

// SubscribedServiceList returns the services a client subscribes to.
func (s *Service) SubscribedServiceList(clientID string) []ServiceRef {
	if clientID = strings.TrimSpace(clientID); clientID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ServiceRef
	for _, entry := range s.services {
		for _, sub := range entry.subscribers {
			id := sub.ClientID
			if id == "" {
				id = sub.Addr
			}
			if id == clientID {
				out = append(out, ServiceRef{
					NamespaceID: sub.NamespaceID,
					GroupName:   sub.GroupName,
					Name:        sub.ServiceName,
				})
			}
		}
	}
	return out
}

// PublisherClients returns the client IDs that have published instances for
// the given service.
func (s *Service) PublisherClients(namespaceID, groupName, serviceName string) []string {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, inst := range entry.instances {
		id := inst.IP
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SubscriberClients returns the client IDs that subscribe to the given
// service.
func (s *Service) SubscriberClients(namespaceID, groupName, serviceName string) []string {
	namespaceID, groupName, serviceName = trim(namespaceID), trim(groupName), trim(serviceName)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.services[serviceKey(namespaceID, groupName, serviceName)]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, sub := range entry.subscribers {
		id := sub.ClientID
		if id == "" {
			id = sub.Addr
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Metrics returns summary counts for the naming registry.
func (s *Service) Metrics() ClientMetric {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := ClientMetric{}
	clients := map[string]bool{}
	for _, entry := range s.services {
		m.ServiceCount++
		for _, inst := range entry.instances {
			m.InstanceCount++
			clients[inst.IP] = true
		}
		for _, sub := range entry.subscribers {
			m.SubscriberCount++
			id := sub.ClientID
			if id == "" {
				id = sub.Addr
			}
			clients[id] = true
		}
	}
	m.ClientCount = len(clients)
	return m
}

// ErrMissingClientID is returned when a client ID is required but absent.
var ErrMissingClientID = errors.New("clientId is required")
