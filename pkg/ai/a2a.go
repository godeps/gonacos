package ai

import (
	"strings"
	"sync"
	"time"
)

// A2AAgent is the Nacos-compatible A2A agent representation. Concurrency
// safety is provided by the owning a2aStore mutex.
type A2AAgent struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Endpoint     string            `json:"endpoint"`
	Protocol     string            `json:"protocol"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Version      string            `json:"version"`
	Versions     []A2AAgentVersion `json:"versions,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

// A2AAgentVersion is a historical version of an A2A agent.
type A2AAgentVersion struct {
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
	Author    string    `json:"author,omitempty"`
}

// a2aStore owns the in-memory A2A agent registry.
type a2aStore struct {
	mu     sync.RWMutex
	agents map[string]*A2AAgent
}

func newA2AStore() *a2aStore {
	return &a2aStore{agents: map[string]*A2AAgent{}}
}

func (s *a2aStore) get(id string) (*A2AAgent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	return a, ok
}

func (s *a2aStore) list() []*A2AAgent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*A2AAgent, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, a)
	}
	return out
}

func (s *a2aStore) put(a *A2AAgent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[a.ID] = a
}

func (s *a2aStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[id]; !ok {
		return false
	}
	delete(s.agents, id)
	return true
}

// RegisterA2AAgent registers a new A2A agent.
func (s *Service) RegisterA2AAgent(agent A2AAgent) (*A2AAgent, error) {
	agent.ID = strings.TrimSpace(agent.ID)
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.ID == "" {
		return nil, ErrMissingID
	}
	if agent.Name == "" {
		return nil, ErrMissingName
	}
	if agent.Protocol == "" {
		agent.Protocol = "a2a"
	}
	if agent.Version == "" {
		agent.Version = "v1"
	}
	now := time.Now()
	agent.CreatedAt = now
	agent.UpdatedAt = now
	agent.Versions = []A2AAgentVersion{{Version: agent.Version, UpdatedAt: now}}
	s.a2a.put(&agent)
	return &agent, nil
}

// UpdateA2AAgent mutates an existing A2A agent and records a new version.
func (s *Service) UpdateA2AAgent(agent A2AAgent) (*A2AAgent, error) {
	agent.ID = strings.TrimSpace(agent.ID)
	if agent.ID == "" {
		return nil, ErrMissingID
	}
	s.a2a.mu.Lock()
	defer s.a2a.mu.Unlock()
	existing, ok := s.a2a.agents[agent.ID]
	if !ok {
		return nil, ErrResourceNotFound
	}
	if agent.Name != "" {
		existing.Name = agent.Name
	}
	if agent.Description != "" {
		existing.Description = agent.Description
	}
	if agent.Endpoint != "" {
		existing.Endpoint = agent.Endpoint
	}
	if agent.Protocol != "" {
		existing.Protocol = agent.Protocol
	}
	if agent.Capabilities != nil {
		existing.Capabilities = cloneStrings(agent.Capabilities)
	}
	if agent.Metadata != nil {
		existing.Metadata = cloneMetadata(agent.Metadata)
	}
	if agent.Version != "" && agent.Version != existing.Version {
		existing.Version = agent.Version
		existing.Versions = append(existing.Versions, A2AAgentVersion{
			Version:   agent.Version,
			UpdatedAt: time.Now(),
		})
	}
	existing.UpdatedAt = time.Now()
	return existing, nil
}

// GetA2AAgent returns the A2A agent by ID.
func (s *Service) GetA2AAgent(id string) (*A2AAgent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	a, ok := s.a2a.get(id)
	if !ok {
		return nil, ErrResourceNotFound
	}
	return a, nil
}

// ListA2AAgents returns all A2A agents.
func (s *Service) ListA2AAgents() []*A2AAgent { return s.a2a.list() }

// ListA2AAgentVersions returns all versions of an A2A agent.
func (s *Service) ListA2AAgentVersions(id string) ([]A2AAgentVersion, error) {
	a, err := s.GetA2AAgent(id)
	if err != nil {
		return nil, err
	}
	out := make([]A2AAgentVersion, len(a.Versions))
	copy(out, a.Versions)
	return out, nil
}

// DeleteA2AAgent removes an A2A agent.
func (s *Service) DeleteA2AAgent(id string) error {
	if !s.a2a.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}
