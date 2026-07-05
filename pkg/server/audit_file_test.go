package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveAuditLogFileEmptyByDefault verifies that a zero options struct
// (no explicit config, no env vars) resolves to an empty path — audit
// events go only to the application logger.
func TestResolveAuditLogFileEmptyByDefault(t *testing.T) {
	o := options{}
	if got := o.resolveAuditLogFile(); got != "" {
		t.Errorf("resolveAuditLogFile = %q, want empty", got)
	}
}

// TestResolveAuditLogFileExplicit verifies that an explicit WithAuditLogFile
// option wins over env vars.
func TestResolveAuditLogFileExplicit(t *testing.T) {
	t.Setenv("GONACOS_AUDIT_LOG_FILE", "/from/env")
	o := options{}
	WithAuditLogFile("/explicit")(&o)
	if got := o.resolveAuditLogFile(); got != "/explicit" {
		t.Errorf("resolveAuditLogFile = %q, want /explicit", got)
	}
}

// TestResolveAuditLogFileEnvFallback verifies that the env var is used when
// no explicit option is set.
func TestResolveAuditLogFileEnvFallback(t *testing.T) {
	t.Setenv("GONACOS_AUDIT_LOG_FILE", "/var/log/gonacos/audit.log")
	o := options{}
	if got := o.resolveAuditLogFile(); got != "/var/log/gonacos/audit.log" {
		t.Errorf("resolveAuditLogFile = %q, want env value", got)
	}
}

// TestBuildAuditLoggerLoggerOnly verifies that an empty path returns a
// non-nil audit logger without attempting to open a file.
func TestBuildAuditLoggerLoggerOnly(t *testing.T) {
	got := buildAuditLogger(nil, "")
	if got == nil {
		t.Fatal("buildAuditLogger returned nil")
	}
}

// TestBuildAuditLoggerWithFile verifies that a configured path returns a
// non-nil audit logger and creates the named file.
func TestBuildAuditLoggerWithFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	got := buildAuditLogger(nil, path)
	if got == nil {
		t.Fatal("buildAuditLogger returned nil")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("audit file not created: %v", err)
	}
}

// TestBuildAuditLoggerBadPathReturnsLogger verifies that when the file
// cannot be opened, the function still returns a non-nil audit logger so
// events are not lost.
func TestBuildAuditLoggerBadPathReturnsLogger(t *testing.T) {
	// A path under a non-directory cannot be created.
	path := "/dev/null/cannot-create/audit.log"
	got := buildAuditLogger(nil, path)
	if got == nil {
		t.Fatal("buildAuditLogger returned nil for bad path")
	}
}
