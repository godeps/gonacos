package ai

import (
	"strings"
	"sync"
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

// importStore owns the in-memory import source registry.
type importStore struct {
	mu      sync.RWMutex
	sources map[string]ImportSource
}

func newImportStore() *importStore {
	return &importStore{
		sources: map[string]ImportSource{
			"builtin": {ID: "builtin", Name: "Built-in", Type: "builtin", Description: "Built-in resources"},
		},
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

// SearchImportCandidates searches a source for importable items. Without a
// remote source client, this returns an empty list for non-builtin sources.
func (s *Service) SearchImportCandidates(sourceID, query string) ([]ImportCandidate, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, ErrImportSourceUnknown
	}
	s.imports.mu.RLock()
	_, ok := s.imports.sources[sourceID]
	s.imports.mu.RUnlock()
	if !ok {
		return nil, ErrImportSourceUnknown
	}
	return nil, nil
}

// ValidateImport checks that an import request is well-formed. Without a
// remote source client, this accepts any non-empty source ID and item ID.
func (s *Service) ValidateImport(sourceID, itemID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return ErrImportSourceUnknown
	}
	s.imports.mu.RLock()
	_, ok := s.imports.sources[sourceID]
	s.imports.mu.RUnlock()
	if !ok {
		return ErrImportSourceUnknown
	}
	if strings.TrimSpace(itemID) == "" {
		return ErrMissingID
	}
	return nil
}

// ExecuteImport imports an item from a source. Without a remote source client,
// this returns ErrImportSourceUnknown for non-builtin sources and an empty
// resource for builtin.
func (s *Service) ExecuteImport(sourceID, itemID string) (ImportCandidate, error) {
	if err := s.ValidateImport(sourceID, itemID); err != nil {
		return ImportCandidate{}, err
	}
	return ImportCandidate{ID: itemID, Name: itemID, Type: "imported"}, nil
}
