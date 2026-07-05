package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/godeps/gonacos/pkg/app"
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

// TestServerReopenAuditLog verifies that Server.ReopenAuditLog swaps the
// audit file descriptor so events written after Reopen land in the file at
// the configured path (rather than continuing to write to a renamed inode).
//
// This is the SIGHUP-based log rotation contract: logrotate renames
// audit.log -> audit.log.1, sends SIGHUP, gonacos calls ReopenAuditLog,
// and the next event lands in the freshly-created audit.log. Without
// Reopen, the logger would keep writing to the renamed inode (audit.log.1)
// and the new audit.log would stay empty.
func TestServerReopenAuditLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	srv, err := New(
		WithAddr("127.0.0.1:0"),
		WithGRPCAddr("127.0.0.1:0"),
		WithRoot(".."),
		WithAuditLogFile(path),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// Write an event to the original file.
	srv.Services().AuditLogger.Log(app.AuditEvent{
		Action: app.AuditActionLogin,
		User:   "before",
	})

	// Rename the file out from under the logger (simulates logrotate).
	renamedPath := path + ".1"
	if err := os.Rename(path, renamedPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Reopen — swap the fd to a fresh file at the original path.
	if err := srv.ReopenAuditLog(); err != nil {
		t.Fatalf("ReopenAuditLog: %v", err)
	}

	// Write an event after Reopen — should land in the new file.
	srv.Services().AuditLogger.Log(app.AuditEvent{
		Action: app.AuditActionLogin,
		User:   "after",
	})

	newData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if !strings.Contains(string(newData), "after") {
		t.Errorf("new audit.log missing 'after' event: %s", newData)
	}

	// The renamed file should contain only the 'before' event. If the fd
	// was not swapped, the 'after' event would also be in the renamed file.
	renamedData, err := os.ReadFile(renamedPath)
	if err != nil {
		t.Fatalf("read renamed: %v", err)
	}
	if !strings.Contains(string(renamedData), "before") {
		t.Errorf("renamed audit.log.1 missing 'before' event: %s", renamedData)
	}
	if strings.Contains(string(renamedData), "after") {
		t.Errorf("renamed audit.log.1 should NOT contain 'after' event (fd not swapped): %s", renamedData)
	}
}

// TestServerReopenAuditLogNoFileIsNoOp verifies that ReopenAuditLog returns
// nil and does nothing when no audit file is configured — the default
// loggerAuditLogger doesn't hold a file descriptor.
func TestServerReopenAuditLogNoFileIsNoOp(t *testing.T) {
	srv, err := New(
		WithAddr("127.0.0.1:0"),
		WithGRPCAddr("127.0.0.1:0"),
		WithRoot(".."),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// No audit file configured — ReopenAuditLog should be a no-op.
	if err := srv.ReopenAuditLog(); err != nil {
		t.Errorf("ReopenAuditLog without file: got %v, want nil", err)
	}
}
