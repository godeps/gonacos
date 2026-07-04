package ai

import (
	"encoding/json"
	"testing"
)

// TestPipelineCRUD exercises the full Create/Read/Update/Delete cycle against
// the pipelineStore. It also verifies that the store correctly rejects
// duplicate creates and missing updates.
func TestPipelineCRUD(t *testing.T) {
	t.Parallel()
	s := NewService(nil)

	// Default pipeline ships with the service.
	defaults := s.ListPipelines()
	if len(defaults) == 0 || defaults[0].ID != "default" {
		t.Fatalf("default pipeline missing: %+v", defaults)
	}

	// Create.
	created, err := s.CreatePipeline(Pipeline{
		ID:          "ingest",
		Name:        "Ingest Pipeline",
		Description: "ingest stage",
		Stages: []PipelineStage{{
			Name:   "fetch",
			Type:   "http",
			Config: map[string]string{"url": "http://example.com"},
		}},
		Metadata: map[string]string{"owner": "ops"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", created)
	}

	// Read.
	got, err := s.GetPipeline("ingest")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Ingest Pipeline" || len(got.Stages) != 1 {
		t.Fatalf("got = %+v", got)
	}

	// Duplicate create.
	if _, err := s.CreatePipeline(Pipeline{ID: "ingest", Name: "dup"}); err != ErrResourceExists {
		t.Fatalf("dup create err = %v, want %v", err, ErrResourceExists)
	}

	// Update.
	updated, err := s.UpdatePipeline(Pipeline{
		ID:          "ingest",
		Name:        "Ingest V2",
		Description: "ingest stage v2",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Ingest V2" || len(updated.Stages) != 1 {
		t.Fatalf("updated = %+v", updated)
	}

	// Delete.
	if err := s.DeletePipeline("ingest"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetPipeline("ingest"); err != ErrResourceNotFound {
		t.Fatalf("get after delete err = %v, want %v", err, ErrResourceNotFound)
	}
}

// TestPipelineCreateValidation verifies the input guards.
func TestPipelineCreateValidation(t *testing.T) {
	t.Parallel()
	s := NewService(nil)

	if _, err := s.CreatePipeline(Pipeline{Name: "no-id"}); err != ErrMissingID {
		t.Fatalf("no id err = %v, want %v", err, ErrMissingID)
	}
	if _, err := s.CreatePipeline(Pipeline{ID: "x"}); err != ErrMissingName {
		t.Fatalf("no name err = %v, want %v", err, ErrMissingName)
	}
}

// TestPipelineUpdateMissing verifies update on a non-existent pipeline.
func TestPipelineUpdateMissing(t *testing.T) {
	t.Parallel()
	s := NewService(nil)
	if _, err := s.UpdatePipeline(Pipeline{ID: "ghost", Name: "x"}); err != ErrResourceNotFound {
		t.Fatalf("err = %v, want %v", err, ErrResourceNotFound)
	}
}

// TestPipelineDeleteMissing verifies delete on a non-existent pipeline.
func TestPipelineDeleteMissing(t *testing.T) {
	t.Parallel()
	s := NewService(nil)
	if err := s.DeletePipeline("ghost"); err != ErrResourceNotFound {
		t.Fatalf("err = %v, want %v", err, ErrResourceNotFound)
	}
	if err := s.DeletePipeline(""); err != ErrMissingID {
		t.Fatalf("err = %v, want %v", err, ErrMissingID)
	}
}

// TestPipelineSnapshotRestore verifies the pipelineStore survives a
// snapshot/restore round-trip.
func TestPipelineSnapshotRestore(t *testing.T) {
	t.Parallel()
	s := NewService(nil)
	_, _ = s.CreatePipeline(Pipeline{ID: "snap-test", Name: "Snap"})

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Snapshot returns a struct; Restore expects a map[string]any. The
	// JSON round-trip mirrors what the backup envelope does on disk.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored map[string]any
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	s2 := NewService(nil)
	if err := s2.Restore(restored); err != nil {
		t.Fatalf("restore: %v", err)
	}

	list := s2.ListPipelines()
	ids := map[string]bool{}
	for _, p := range list {
		ids[p.ID] = true
	}
	if !ids["default"] || !ids["snap-test"] {
		t.Fatalf("restore lost pipelines: %v", ids)
	}
}
