package ai

import (
	"strings"
)

// ListPipelines returns all registered pipelines.
func (s *Service) ListPipelines() []Pipeline {
	out := make([]Pipeline, len(s.pipelines))
	copy(out, s.pipelines)
	return out
}

// GetPipeline returns the pipeline by ID.
func (s *Service) GetPipeline(id string) (Pipeline, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Pipeline{}, ErrMissingID
	}
	for _, p := range s.pipelines {
		if p.ID == id {
			return p, nil
		}
	}
	return Pipeline{}, ErrResourceNotFound
}

// GetPipelineDetail returns the pipeline with stage detail.
func (s *Service) GetPipelineDetail(id string) (Pipeline, error) {
	return s.GetPipeline(id)
}
