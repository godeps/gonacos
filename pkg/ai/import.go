package ai

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ImportSource is a registered source from which AI resources can be imported.
type ImportSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Endpoint    string `json:"endpoint,omitempty"`
	Description string `json:"description,omitempty"`
}

// ImportCandidate is a single importable item from a source.
type ImportCandidate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// SourceResolver looks up importable items from a remote source. Implementations
// live in sub-packages (e.g. Dify) and are registered on the Service via
// RegisterSourceResolver.
type SourceResolver interface {
	// SourceType returns the source type this resolver handles (e.g. "dify").
	SourceType() string
	// Search returns importable items matching the query.
	Search(ctx context.Context, source ImportSource, query string) ([]ImportCandidate, error)
	// Validate checks that the item is importable.
	Validate(ctx context.Context, source ImportSource, itemID string) error
	// Import fetches the item and returns it as a candidate.
	Import(ctx context.Context, source ImportSource, itemID string) (ImportCandidate, error)
}

// importStore owns the in-memory import source registry.
type importStore struct {
	mu        sync.RWMutex
	sources   map[string]ImportSource
	resolvers map[string]SourceResolver
}

func newImportStore() *importStore {
	return &importStore{
		sources: map[string]ImportSource{
			"builtin": {ID: "builtin", Name: "Built-in", Type: "builtin", Description: "Built-in resources"},
		},
		resolvers: map[string]SourceResolver{},
	}
}

// ListImportSources returns all registered import sources.
func (s *Service) ListImportSources() []ImportSource {
	s.imports.mu.RLock()
	defer s.imports.mu.RUnlock()
	out := make([]ImportSource, 0, len(s.imports.sources))
	for _, src := range s.imports.sources {
		out = append(out, src)
	}
	return out
}

// RegisterImportSource registers an import source. If a SourceResolver is
// available for the source type, search/validate/import will delegate to it.
func (s *Service) RegisterImportSource(src ImportSource) error {
	src.ID = strings.TrimSpace(src.ID)
	if src.ID == "" {
		return ErrMissingID
	}
	if strings.TrimSpace(src.Name) == "" {
		return ErrMissingName
	}
	s.imports.mu.Lock()
	defer s.imports.mu.Unlock()
	s.imports.sources[src.ID] = src
	return nil
}

// UnregisterImportSource removes an import source.
func (s *Service) UnregisterImportSource(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrMissingID
	}
	s.imports.mu.Lock()
	defer s.imports.mu.Unlock()
	if _, ok := s.imports.sources[id]; !ok {
		return ErrResourceNotFound
	}
	delete(s.imports.sources, id)
	return nil
}

// RegisterSourceResolver attaches a resolver for a source type. The resolver
// is consulted by SearchImportCandidates/ValidateImport/ExecuteImport.
func (s *Service) RegisterSourceResolver(r SourceResolver) {
	if r == nil {
		return
	}
	s.imports.mu.Lock()
	defer s.imports.mu.Unlock()
	s.imports.resolvers[r.SourceType()] = r
}

// SearchImportCandidates searches a source for importable items. If a
// SourceResolver is registered for the source type, it is consulted; otherwise
// an empty list is returned.
func (s *Service) SearchImportCandidates(sourceID, query string) ([]ImportCandidate, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, ErrImportSourceUnknown
	}
	s.imports.mu.RLock()
	src, ok := s.imports.sources[sourceID]
	resolver := s.imports.resolvers[src.Type]
	s.imports.mu.RUnlock()
	if !ok {
		return nil, ErrImportSourceUnknown
	}
	if resolver == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return resolver.Search(ctx, src, query)
}

// ValidateImport checks that an import request is well-formed. If a resolver
// is registered, it is consulted.
func (s *Service) ValidateImport(sourceID, itemID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return ErrImportSourceUnknown
	}
	s.imports.mu.RLock()
	src, ok := s.imports.sources[sourceID]
	resolver := s.imports.resolvers[src.Type]
	s.imports.mu.RUnlock()
	if !ok {
		return ErrImportSourceUnknown
	}
	if strings.TrimSpace(itemID) == "" {
		return ErrMissingID
	}
	if resolver == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return resolver.Validate(ctx, src, itemID)
}

// ExecuteImport imports an item from a source. If a resolver is registered,
// it is consulted; otherwise the item is returned as a generic imported candidate.
func (s *Service) ExecuteImport(sourceID, itemID string) (ImportCandidate, error) {
	if err := s.ValidateImport(sourceID, itemID); err != nil {
		return ImportCandidate{}, err
	}
	s.imports.mu.RLock()
	src, _ := s.imports.sources[sourceID]
	resolver := s.imports.resolvers[src.Type]
	s.imports.mu.RUnlock()
	if resolver == nil {
		return ImportCandidate{ID: itemID, Name: itemID, Type: "imported"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return resolver.Import(ctx, src, itemID)
}
