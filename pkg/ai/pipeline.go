package ai

import (
	"strings"
	"sync"
	"time"
)

// pipelineStore owns the in-memory pipeline registry. It mirrors the
// mcpStore shape: a mutex-guarded map keyed by pipeline ID.
type pipelineStore struct {
	mu        sync.RWMutex
	pipelines map[string]*Pipeline
}

func newPipelineStore(initial []Pipeline) *pipelineStore {
	s := &pipelineStore{pipelines: map[string]*Pipeline{}}
	for i := range initial {
		p := initial[i]
		s.pipelines[p.ID] = &p
	}
	return s
}

func (s *pipelineStore) get(id string) (*Pipeline, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pipelines[id]
	return p, ok
}

func (s *pipelineStore) list() []*Pipeline {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Pipeline, 0, len(s.pipelines))
	for _, p := range s.pipelines {
		out = append(out, p)
	}
	return out
}

func (s *pipelineStore) put(p *Pipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines[p.ID] = p
}

func (s *pipelineStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pipelines[id]; !ok {
		return false
	}
	delete(s.pipelines, id)
	return true
}

func (s *pipelineStore) replace(all []Pipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines = map[string]*Pipeline{}
	for i := range all {
		p := all[i]
		s.pipelines[p.ID] = &p
	}
}

// ListPipelines returns all registered pipelines.
func (s *Service) ListPipelines() []Pipeline {
	list := s.pipelines.list()
	out := make([]Pipeline, len(list))
	for i, p := range list {
		out[i] = *p
	}
	return out
}

// GetPipeline returns the pipeline by ID.
func (s *Service) GetPipeline(id string) (Pipeline, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Pipeline{}, ErrMissingID
	}
	p, ok := s.pipelines.get(id)
	if !ok {
		return Pipeline{}, ErrResourceNotFound
	}
	return *p, nil
}

// GetPipelineDetail returns the pipeline with stage detail.
func (s *Service) GetPipelineDetail(id string) (Pipeline, error) {
	return s.GetPipeline(id)
}

// CreatePipeline registers a new pipeline. Returns ErrResourceExists if a
// pipeline with the same ID already exists.
func (s *Service) CreatePipeline(p Pipeline) (*Pipeline, error) {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	if p.ID == "" {
		return nil, ErrMissingID
	}
	if p.Name == "" {
		return nil, ErrMissingName
	}
	if _, exists := s.pipelines.get(p.ID); exists {
		return nil, ErrResourceExists
	}
	if p.Stages != nil {
		p.Stages = append([]PipelineStage(nil), p.Stages...)
	}
	if p.Metadata != nil {
		p.Metadata = cloneMetadata(p.Metadata)
	}
	now := time.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	s.pipelines.put(&p)
	return &p, nil
}

// UpdatePipeline mutates an existing pipeline. Empty fields are ignored.
func (s *Service) UpdatePipeline(p Pipeline) (*Pipeline, error) {
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return nil, ErrMissingID
	}
	existing, ok := s.pipelines.get(p.ID)
	if !ok {
		return nil, ErrResourceNotFound
	}
	s.pipelines.mu.Lock()
	defer s.pipelines.mu.Unlock()
	if p.Name != "" {
		existing.Name = p.Name
	}
	if p.Description != "" {
		existing.Description = p.Description
	}
	if p.Stages != nil {
		existing.Stages = append([]PipelineStage(nil), p.Stages...)
	}
	if p.Metadata != nil {
		existing.Metadata = cloneMetadata(p.Metadata)
	}
	existing.UpdatedAt = time.Now()
	return existing, nil
}

// DeletePipeline removes a pipeline by ID.
func (s *Service) DeletePipeline(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrMissingID
	}
	if !s.pipelines.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}
