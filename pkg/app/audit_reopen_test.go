package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileAuditLoggerReopenSwapsFileDescriptor verifies that Reopen closes
// the current file descriptor and opens a fresh one at the configured path.
// After Reopen, events land in the file at the configured path — even when
// the original file was renamed out from under the logger.
//
// This is the logrotate-with-SIGHUP contract: logrotate renames audit.log
// to audit.log.1, sends SIGHUP, gonacos calls Reopen, and the next event
// goes to the freshly-created audit.log. Without Reopen, the logger would
// keep writing to the renamed inode (audit.log.1) and the new audit.log
// would stay empty.
func TestFileAuditLoggerReopenSwapsFileDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	// Write one event to the original file.
	logger.Log(AuditEvent{Action: AuditActionLogin, User: "before"})
	originalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if len(originalData) == 0 {
		t.Fatal("original file empty before rename")
	}

	// Rename the file out from under the logger (simulates logrotate).
	renamedPath := path + ".1"
	if err := os.Rename(path, renamedPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Without Reopen, the next event would land in the renamed file
	// (audit.log.1) because the fd still points to the renamed inode.
	// Call Reopen to swap the fd to a fresh file at the original path.
	reopener, ok := logger.(AuditLogReopener)
	if !ok {
		t.Fatal("fileAuditLogger should implement AuditLogReopener")
	}
	if err := reopener.Reopen(); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// Write one event after Reopen — should land in the new file at the
	// original path, NOT in the renamed file.
	logger.Log(AuditEvent{Action: AuditActionLogin, User: "after"})

	newData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if len(newData) == 0 {
		t.Fatal("new file empty after Reopen — event did not land at the configured path")
	}

	// The renamed file should still contain only the "before" event, not
	// the "after" event. This proves the fd was swapped, not still pointing
	// at the renamed inode.
	renamedData, err := os.ReadFile(renamedPath)
	if err != nil {
		t.Fatalf("read renamed: %v", err)
	}
	if !contains(string(renamedData), "before") {
		t.Errorf("renamed file missing 'before' event: %s", renamedData)
	}
	if contains(string(renamedData), "after") {
		t.Errorf("renamed file should NOT contain 'after' event (fd was not swapped): %s", renamedData)
	}
}

// TestFileAuditLoggerReopenAfterFailure verifies Reopen recovers from a
// state where the file descriptor is nil (e.g., previous write failure
// closed the fd). After Reopen, Log writes succeed again.
func TestFileAuditLoggerReopenAfterFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	// Manually close the fd to simulate a write-failure recovery state.
	fl := logger.(*fileAuditLogger)
	if fl.f == nil {
		t.Fatal("expected non-nil fd after construction")
	}
	_ = fl.f.Close()
	fl.f = nil

	// Reopen should open a fresh fd.
	reopener, ok := logger.(AuditLogReopener)
	if !ok {
		t.Fatal("fileAuditLogger should implement AuditLogReopener")
	}
	if err := reopener.Reopen(); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// Log should now write successfully.
	logger.Log(AuditEvent{Action: AuditActionLogin, User: "recovered"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !contains(string(data), "recovered") {
		t.Errorf("event missing after Reopen recovery: %s", data)
	}
}

// TestMultiAuditLoggerReopenDelegates verifies multiAuditLogger.Reopen
// calls Reopen on every wrapped logger that supports it, and silently
// skips loggers that don't (e.g., loggerAuditLogger writing to stderr).
func TestMultiAuditLoggerReopenDelegates(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.log")
	path2 := filepath.Join(dir, "b.log")

	file1, err := NewFileAuditLogger(path1)
	if err != nil {
		t.Fatalf("NewFileAuditLogger path1: %v", err)
	}
	file2, err := NewFileAuditLogger(path2)
	if err != nil {
		t.Fatalf("NewFileAuditLogger path2: %v", err)
	}
	// loggerAuditLogger doesn't implement Reopen — must be silently
	// skipped by multi.Reopen.
	loggerOnly := NewLoggerAuditLogger(stubInfofLogger{})

	multi := NewMultiAuditLogger(file1, loggerOnly, file2)
	reopener, ok := multi.(AuditLogReopener)
	if !ok {
		t.Fatal("multiAuditLogger should implement AuditLogReopener")
	}
	if err := reopener.Reopen(); err != nil {
		t.Fatalf("multi.Reopen: %v", err)
	}

	// All file loggers should still be writable after Reopen.
	multi.Log(AuditEvent{Action: AuditActionLogin, User: "post-reopen"})

	for _, p := range []string{path1, path2} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if !contains(string(data), "post-reopen") {
			t.Errorf("%s missing event after multi.Reopen: %s", p, data)
		}
	}
}

// TestMultiAuditLoggerReopenReturnsFirstError verifies multi.Reopen returns
// the first error encountered but still calls Reopen on subsequent loggers.
// A single broken file must not block rotation of the others.
func TestMultiAuditLoggerReopenReturnsFirstError(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.log")

	good, err := NewFileAuditLogger(goodPath)
	if err != nil {
		t.Fatalf("NewFileAuditLogger good: %v", err)
	}
	// Bad: a fileAuditLogger whose path is in a non-existent directory
	// that can't be created (the parent dir is a file, not a dir).
	badPath := filepath.Join(dir, "blocker-file") // a regular file
	if err := os.WriteFile(badPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	// NewFileAuditLogger will try to MkdirAll the parent of badPath/blocker.log,
	// which fails because badPath is a file. Wait — NewFileAuditLogger calls
	// MkdirAll(filepath.Dir(path)) — the dir of "blocker-file/blocker.log"
	// is "blocker-file", which is a file, so MkdirAll fails. But
	// NewFileAuditLogger returns the MkdirAll error. So we can't construct
	// a "bad" fileAuditLogger this way directly. Construct it manually.
	bad := &fileAuditLogger{path: filepath.Join(badPath, "blocker.log")}

	multi := NewMultiAuditLogger(bad, good)
	reopener, ok := multi.(AuditLogReopener)
	if !ok {
		t.Fatal("multiAuditLogger should implement AuditLogReopener")
	}
	err = reopener.Reopen()
	if err == nil {
		t.Fatal("multi.Reopen with a broken file: expected error, got nil")
	}
	// The good logger must still have been Reopened (and remain writable).
	multi.Log(AuditEvent{Action: AuditActionLogin, User: "post-bad-reopen"})
	data, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatalf("read good: %v", err)
	}
	if !contains(string(data), "post-bad-reopen") {
		t.Errorf("good logger not writable after multi.Reopen returned error: %s", data)
	}
}

// contains is a tiny substring helper to keep the test file readable without
// importing strings (avoids an unused-import lint if strings is only used
// here in a single test).
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// stubInfofLogger is a minimal Logger-shaped stub for the audit logger
// adapter tests. Only Infof is called by loggerAuditLogger.Log.
type stubInfofLogger struct{}

func (stubInfofLogger) Infof(format string, args ...any) {}
