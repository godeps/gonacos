package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/observability"
)

// TestFileAuditLoggerWriteFailureIncrementsMetric verifies that when
// the underlying file write fails, fileAuditLogger increments
// gonacos_audit_write_failures_total{reason="write"} so operators can
// alert from /metrics when the audit pipeline is silently dropping
// events.
//
// Without this metric, a full disk, revoked permission, or dropped
// NFS mount would cause audit events to vanish into stderr with no
// signal in /metrics — the operator has no way to alert on "audit
// stopped working".
//
// The write failure is simulated by closing the underlying file
// descriptor before calling Log. On Linux, file permissions are
// checked at open time, not write time — chmod 0o400 on a file
// already opened with O_WRONLY does NOT cause Write to fail, because
// the descriptor retains its open-time permissions. Closing the fd
// first causes the next Write to return "file already closed",
// which is the same error class the production path sees when the
// filesystem reclaims a descriptor (NFS drop, etc.).
func TestFileAuditLoggerWriteFailureIncrementsMetric(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	// Close the underlying descriptor WITHOUT going through Reopen —
	// this leaves l.f non-nil but the descriptor is invalid, so the
	// next Write returns "file already closed" and the write-failure
	// path fires.
	fl, ok := logger.(*fileAuditLogger)
	if !ok {
		t.Fatalf("expected *fileAuditLogger, got %T", logger)
	}
	if err := fl.f.Close(); err != nil {
		t.Fatalf("close fd: %v", err)
	}

	logger.Log(AuditEvent{
		Action: AuditActionLogin,
		Result: AuditResultSuccess,
	})

	got := registry.Counter("gonacos_audit_write_failures_total",
		map[string]string{"reason": "write"},
	).Value()
	if got != 1 {
		t.Errorf("write failure counter = %d, want 1 (fd was closed, write should have failed)", got)
	}
}

// TestFileAuditLoggerOpenFailureIncrementsMetric verifies that when
// the file cannot be reopened after a previous write failure,
// fileAuditLogger increments gonacos_audit_write_failures_total{reason="open"}
// rather than silently dropping the event into stderr.
//
// This path fires when the underlying file or directory disappears
// (e.g., the audit directory is removed by an operator or a
// container restart loses the volume) between a previous write
// failure and the next reopen-on-failure attempt.
func TestFileAuditLoggerOpenFailureIncrementsMetric(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "audit.log")
	// Construct successfully — MkdirAll creates subdir.
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	// Force l.f = nil so the next Log call takes the reopen-on-failure
	// path. Then remove the directory so reopen fails with ENOENT.
	fl, ok := logger.(*fileAuditLogger)
	if !ok {
		t.Fatalf("expected *fileAuditLogger, got %T", logger)
	}
	_ = fl.f.Close()
	fl.f = nil

	if err := os.RemoveAll(filepath.Join(dir, "subdir")); err != nil {
		t.Fatalf("remove subdir: %v", err)
	}

	// Next Log call: l.f is nil, reopen fails because the directory
	// is gone.
	logger.Log(AuditEvent{Action: AuditActionLogin, Result: AuditResultSuccess})

	got := registry.Counter("gonacos_audit_write_failures_total",
		map[string]string{"reason": "open"},
	).Value()
	if got != 1 {
		t.Errorf("open failure counter = %d, want 1 (directory removed, reopen should have failed)", got)
	}
}

// TestFileAuditLoggerSuccessNoFailureMetric verifies that a
// successful write does NOT increment the failure counter — the
// metric is for signaling silent drops, not for counting total
// events (that is gonacos_audit_events_total's job).
func TestFileAuditLoggerSuccessNoFailureMetric(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	for i := 0; i < 5; i++ {
		logger.Log(AuditEvent{
			Action: AuditActionLogin,
			Result: AuditResultSuccess,
		})
	}

	for _, reason := range []string{"open", "write", "marshal"} {
		got := registry.Counter("gonacos_audit_write_failures_total",
			map[string]string{"reason": reason},
		).Value()
		if got != 0 {
			t.Errorf("failure counter{reason=%s} = %d, want 0 (all writes succeeded)", reason, got)
		}
	}
}

// TestFileAuditLoggerMetricsExposedInPrometheusOutput verifies the
// counter appears in /metrics output with the right name and label
// so scrapers pick it up.
func TestFileAuditLoggerMetricsExposedInPrometheusOutput(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	fl, ok := logger.(*fileAuditLogger)
	if !ok {
		t.Fatalf("expected *fileAuditLogger, got %T", logger)
	}
	if err := fl.f.Close(); err != nil {
		t.Fatalf("close fd: %v", err)
	}

	logger.Log(AuditEvent{Action: AuditActionLogin, Result: AuditResultSuccess})

	var buf strings.Builder
	registry.WritePrometheus(&buf)
	out := buf.String()
	if !strings.Contains(out, "gonacos_audit_write_failures_total") {
		t.Errorf("metric missing from /metrics output: %s", out)
	}
	if !strings.Contains(out, `reason="write"`) {
		t.Errorf("reason label missing: %s", out)
	}
}

// TestNewFileAuditLoggerPicksUpMetricsRegistry verifies that
// NewFileAuditLogger automatically wires the package-level
// AuditMetricsRegistry into the fileAuditLogger — the caller does
// not need to call SetMetricsRegistry separately. This is the
// construction-time wiring that makes the write-failure counter
// work without changing NewFileAuditLogger's signature.
func TestNewFileAuditLoggerPicksUpMetricsRegistry(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	registry := observability.NewRegistry()
	SetAuditMetricsRegistry(registry)

	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	fl, ok := logger.(*fileAuditLogger)
	if !ok {
		t.Fatalf("expected *fileAuditLogger, got %T", logger)
	}
	if fl.metrics != registry {
		t.Errorf("fileAuditLogger.metrics not wired: got %v, want %v", fl.metrics, registry)
	}
}

// TestNewFileAuditLoggerNoRegistryLeavesMetricsNil verifies that
// when AuditMetricsRegistry is nil at construction time, the
// fileAuditLogger.metrics field is nil — write failures still go to
// stderr, but no metric is recorded. This is the path for tests and
// embedders that opted out of observability.
func TestNewFileAuditLoggerNoRegistryLeavesMetricsNil(t *testing.T) {
	defer func() { AuditMetricsRegistry = nil }()
	AuditMetricsRegistry = nil

	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	fl, ok := logger.(*fileAuditLogger)
	if !ok {
		t.Fatalf("expected *fileAuditLogger, got %T", logger)
	}
	if fl.metrics != nil {
		t.Errorf("fileAuditLogger.metrics = %v, want nil when no registry configured", fl.metrics)
	}
}
