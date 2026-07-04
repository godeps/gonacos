package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/godeps/gonacos/pkg/ai/dify"
)

// DifySourceResolver exposes Dify workflows as importable candidates. It
// implements SourceResolver so the AI Service can delegate search/validate/
// import to a Dify client.
type DifySourceResolver struct {
	client *dify.Client
}

// NewDifySourceResolver builds a resolver backed by the given Dify client.
func NewDifySourceResolver(c *dify.Client) *DifySourceResolver {
	return &DifySourceResolver{client: c}
}

// SourceType returns "dify".
func (r *DifySourceResolver) SourceType() string { return "dify" }

// Search returns locally-known Dify workflows whose name or ID contains the
// query (empty query returns all). Dify's HTTP API does not expose a search
// endpoint, so this is a client-side filter.
func (r *DifySourceResolver) Search(_ context.Context, _ ImportSource, query string) ([]ImportCandidate, error) {
	if r.client == nil {
		return nil, dify.ErrNotConfigured
	}
	workflows := r.client.ListWorkflows()
	out := make([]ImportCandidate, 0, len(workflows))
	for _, wf := range workflows {
		if query != "" {
			if !strings.Contains(strings.ToLower(wf.Name), strings.ToLower(query)) &&
				!strings.Contains(strings.ToLower(wf.ID), strings.ToLower(query)) {
				continue
			}
		}
		out = append(out, ImportCandidate{
			ID:          wf.ID,
			Name:        wf.Name,
			Type:        "dify-workflow",
			Description: wf.Description,
			Metadata: map[string]string{
				"sourceType": "dify",
			},
		})
	}
	return out, nil
}

// Validate checks that the workflow ID is known locally.
func (r *DifySourceResolver) Validate(_ context.Context, _ ImportSource, itemID string) error {
	if r.client == nil {
		return dify.ErrNotConfigured
	}
	for _, wf := range r.client.ListWorkflows() {
		if wf.ID == itemID {
			return nil
		}
	}
	return fmt.Errorf("dify: workflow %q not found", itemID)
}

// Import returns the workflow as a candidate. The candidate's Metadata includes
// the workflow ID so downstream consumers (e.g. an MCP importer) can invoke it.
func (r *DifySourceResolver) Import(ctx context.Context, src ImportSource, itemID string) (ImportCandidate, error) {
	if err := r.Validate(ctx, src, itemID); err != nil {
		return ImportCandidate{}, err
	}
	for _, wf := range r.client.ListWorkflows() {
		if wf.ID == itemID {
			return ImportCandidate{
				ID:          wf.ID,
				Name:        wf.Name,
				Type:        "dify-workflow",
				Description: wf.Description,
				Metadata: map[string]string{
					"sourceType": "dify",
					"endpoint":   r.client.Endpoint(),
				},
			}, nil
		}
	}
	return ImportCandidate{}, fmt.Errorf("dify: workflow %q not found", itemID)
}
