package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/godeps/gonacos/pkg/ai/dify"
)

// TestDifySourceResolverSearch verifies that Search returns locally-known
// workflows filtered by query.
func TestDifySourceResolverSearch(t *testing.T) {
	t.Parallel()
	c := dify.NewClient("https://api.dify.ai", "key")
	c.SetWorkflows([]dify.WorkflowSummary{
		{ID: "wf-1", Name: "Search Workflow", Description: "searches things"},
		{ID: "wf-2", Name: "Other Workflow"},
	})
	r := NewDifySourceResolver(c)
	out, err := r.Search(context.Background(), ImportSource{ID: "dify", Type: "dify"}, "search")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].ID != "wf-1" {
		t.Fatalf("id = %q", out[0].ID)
	}
	if out[0].Type != "dify-workflow" {
		t.Fatalf("type = %q", out[0].Type)
	}
}

// TestDifySourceResolverSearchEmptyQuery verifies an empty query returns all.
func TestDifySourceResolverSearchEmptyQuery(t *testing.T) {
	t.Parallel()
	c := dify.NewClient("https://api.dify.ai", "key")
	c.SetWorkflows([]dify.WorkflowSummary{
		{ID: "wf-1", Name: "First"},
		{ID: "wf-2", Name: "Second"},
	})
	r := NewDifySourceResolver(c)
	out, err := r.Search(context.Background(), ImportSource{ID: "dify", Type: "dify"}, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
}

// TestDifySourceResolverValidate verifies Validate finds known workflows and
// rejects unknown ones.
func TestDifySourceResolverValidate(t *testing.T) {
	t.Parallel()
	c := dify.NewClient("https://api.dify.ai", "key")
	c.SetWorkflows([]dify.WorkflowSummary{{ID: "wf-1", Name: "First"}})
	r := NewDifySourceResolver(c)
	if err := r.Validate(context.Background(), ImportSource{}, "wf-1"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := r.Validate(context.Background(), ImportSource{}, "ghost"); err == nil {
		t.Fatalf("expected error for unknown workflow")
	}
}

// TestDifySourceResolverImport verifies Import returns the workflow candidate.
func TestDifySourceResolverImport(t *testing.T) {
	t.Parallel()
	c := dify.NewClient("https://api.dify.ai", "key")
	c.SetWorkflows([]dify.WorkflowSummary{
		{ID: "wf-1", Name: "First", Description: "first workflow"},
	})
	r := NewDifySourceResolver(c)
	cand, err := r.Import(context.Background(), ImportSource{}, "wf-1")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if cand.ID != "wf-1" {
		t.Fatalf("id = %q", cand.ID)
	}
	if cand.Name != "First" {
		t.Fatalf("name = %q", cand.Name)
	}
	if cand.Metadata["endpoint"] != "https://api.dify.ai" {
		t.Fatalf("endpoint = %q", cand.Metadata["endpoint"])
	}
}

// TestDifySourceResolverImportUnknown verifies importing an unknown workflow
// returns an error.
func TestDifySourceResolverImportUnknown(t *testing.T) {
	t.Parallel()
	c := dify.NewClient("https://api.dify.ai", "key")
	r := NewDifySourceResolver(c)
	_, err := r.Import(context.Background(), ImportSource{}, "ghost")
	if err == nil {
		t.Fatalf("expected error")
	}
}

// TestDifySourceResolverNoClient verifies a resolver without a client returns
// ErrNotConfigured.
func TestDifySourceResolverNoClient(t *testing.T) {
	t.Parallel()
	r := NewDifySourceResolver(nil)
	_, err := r.Search(context.Background(), ImportSource{}, "")
	if !errors.Is(err, dify.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// TestServiceRegisterImportSourceAndResolver wires up the full path: register
// a Dify source + resolver, then search/validate/import through the Service.
func TestServiceRegisterImportSourceAndResolver(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	c := dify.NewClient("https://api.dify.ai", "key")
	c.SetWorkflows([]dify.WorkflowSummary{{ID: "wf-1", Name: "First"}})
	svc.RegisterSourceResolver(NewDifySourceResolver(c))
	if err := svc.RegisterImportSource(ImportSource{
		ID: "dify", Name: "Dify", Type: "dify", Endpoint: "https://api.dify.ai",
	}); err != nil {
		t.Fatalf("register source: %v", err)
	}
	candidates, err := svc.SearchImportCandidates("dify", "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len = %d, want 1", len(candidates))
	}
	if candidates[0].ID != "wf-1" {
		t.Fatalf("id = %q", candidates[0].ID)
	}
	if err := svc.ValidateImport("dify", "wf-1"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cand, err := svc.ExecuteImport("dify", "wf-1")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if cand.ID != "wf-1" {
		t.Fatalf("id = %q", cand.ID)
	}
}

// TestServiceRegisterImportSourceValidation verifies RegisterImportSource guards.
func TestServiceRegisterImportSourceValidation(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	if err := svc.RegisterImportSource(ImportSource{ID: "", Name: "X"}); err == nil {
		t.Fatalf("expected error for empty ID")
	}
	if err := svc.RegisterImportSource(ImportSource{ID: "x", Name: ""}); err == nil {
		t.Fatalf("expected error for empty Name")
	}
}

// TestServiceUnregisterImportSource verifies unregister.
func TestServiceUnregisterImportSource(t *testing.T) {
	t.Parallel()
	svc := NewService(nil)
	_ = svc.RegisterImportSource(ImportSource{ID: "src-1", Name: "Source 1", Type: "custom"})
	if err := svc.UnregisterImportSource("src-1"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	sources := svc.ListImportSources()
	for _, s := range sources {
		if s.ID == "src-1" {
			t.Fatalf("source should be removed")
		}
	}
	if err := svc.UnregisterImportSource("ghost"); err == nil {
		t.Fatalf("expected error for unknown source")
	}
}
