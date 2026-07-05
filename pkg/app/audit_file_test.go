package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFileAuditLoggerWritesJSONLines verifies that the file audit logger
// writes one JSON object per line to the named file, and that the file is
// created (with parent directories) when missing.
func TestFileAuditLoggerWritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.log")
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	event := AuditEvent{
		Timestamp: time.Now().UTC(),
		Action:    AuditActionLogin,
		User:      "alice",
		IP:        "10.0.0.1",
		Result:    AuditResultSuccess,
	}
	logger.Log(event)
	logger.Log(event)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for i, line := range lines {
		var got AuditEvent
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d unmarshal: %v", i, err)
		}
		if got.Action != AuditActionLogin {
			t.Errorf("line %d action = %v, want login", i, got.Action)
		}
		if got.User != "alice" {
			t.Errorf("line %d user = %v, want alice", i, got.User)
		}
	}
}

// TestFileAuditLoggerAppends verifies that the file audit logger appends
// to an existing file rather than truncating it.
func TestFileAuditLoggerAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte(`{"preexisting":true}`+"\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	logger.Log(AuditEvent{Action: AuditActionBackup, Result: AuditResultSuccess})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if !strings.HasPrefix(string(data), `{"preexisting":true}`) {
		t.Error("file was truncated; expected preexisting content preserved")
	}
	if !strings.Contains(string(data), `"backup"`) {
		t.Error("appended event missing")
	}
}

// TestFileAuditLoggerEmptyPathErrors verifies that an empty path is rejected.
func TestFileAuditLoggerEmptyPathErrors(t *testing.T) {
	if _, err := NewFileAuditLogger(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestFileAuditLoggerDirectoryPermissions verifies the audit log parent
// directory is created with mode 0o700. The audit log contains user IPs,
// usernames, and security-relevant actions (login success/failure, user/
// role/permission mutations, backup/restore) — all PII or compliance-
// relevant. Restricting the directory to the gonacos process user is
// defense in depth on multi-user hosts.
func TestFileAuditLoggerDirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "audit", "gonacos")
	path := filepath.Join(nested, "audit.log")

	if _, err := NewFileAuditLogger(path); err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("audit dir mode = %o, want 0700", got)
	}

	// The audit file itself is 0o600 (OpenFile with 0o600).
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("audit file mode = %o, want 0600", got)
	}
}

// TestFileAuditLoggerPreExistingDirectoryModeUnchanged verifies MkdirAll
// only sets the mode on directories it creates — a pre-existing directory
// keeps its existing mode. This is the safety contract: an operator who
// pre-provisions the audit dir with a specific mode (e.g., to share with a
// SIEM collector) is not surprised by gonacos changing it out from under
// them.
func TestFileAuditLoggerPreExistingDirectoryModeUnchanged(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "audit")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(nested, "audit.log")

	if _, err := NewFileAuditLogger(path); err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("pre-existing dir mode changed: got %o, want 0750", got)
	}
}

// TestMultiAuditLoggerFansOut verifies that the multi audit logger dispatches
// events to all wrapped loggers.
func TestMultiAuditLoggerFansOut(t *testing.T) {
	a := &recordingAuditLogger{}
	b := &recordingAuditLogger{}
	multi := NewMultiAuditLogger(a, b)
	multi.Log(AuditEvent{Action: AuditActionLogin, User: "x", Result: AuditResultSuccess})
	if len(a.Events()) != 1 {
		t.Errorf("logger A events = %d, want 1", len(a.Events()))
	}
	if len(b.Events()) != 1 {
		t.Errorf("logger B events = %d, want 1", len(b.Events()))
	}
}

// TestMultiAuditLoggerNilSkipped verifies that nil loggers are skipped.
func TestMultiAuditLoggerNilSkipped(t *testing.T) {
	a := &recordingAuditLogger{}
	multi := NewMultiAuditLogger(nil, a, nil)
	if multi != a {
		t.Error("expected single non-nil logger returned directly, not wrapped")
	}
}

// TestMultiAuditLoggerAllNilReturnsNoop verifies that wrapping all-nil
// loggers returns the noop logger.
func TestMultiAuditLoggerAllNilReturnsNoop(t *testing.T) {
	multi := NewMultiAuditLogger(nil, nil)
	if _, ok := multi.(noopAuditLogger); !ok {
		t.Error("expected noopAuditLogger for all-nil input")
	}
}

// TestFileAuditLoggerRotatesOnMaxBytes verifies that the file audit
// logger auto-rotates when the current file reaches MaxBytes. After
// rotation, the original file should be renamed to .1 and a fresh file
// should be opened at the original path. This is the safety net for
// deployments where logrotate(8) is not configured — without it, a
// high-volume audit stream can fill the disk in hours.
func TestFileAuditLoggerRotatesOnMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	// maxBytes=1 — any single event (~100+ bytes) triggers rotation
	// immediately. Small value keeps the test fast and deterministic.
	logger, err := NewFileAuditLoggerWithRotation(path, 1, 3)
	if err != nil {
		t.Fatalf("NewFileAuditLoggerWithRotation: %v", err)
	}

	event := AuditEvent{
		Timestamp: time.Now().UTC(),
		Action:    AuditActionLogin,
		User:      "alice",
		IP:        "10.0.0.1",
		Result:    AuditResultSuccess,
	}
	// Write 3 events — each triggers a rotation.
	for i := 0; i < 3; i++ {
		logger.Log(event)
	}

	// After 3 rotations, audit.log.1, .2, .3 should all exist.
	// (maxBackups=3 keeps all three; a 4th rotation would delete .3.)
	for n := 1; n <= 3; n++ {
		backup := fmt.Sprintf("%s.%d", path, n)
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("expected backup file %s to exist: %v", backup, err)
		}
	}
	// The current file should exist (fresh file opened after last rotation).
	if _, err := os.Stat(path); err != nil {
		t.Errorf("current audit file should exist after rotation: %v", err)
	}
	// The oldest slot beyond maxBackups should not exist.
	if _, err := os.Stat(path + ".4"); err == nil {
		t.Errorf("backup .4 should not exist (maxBackups=3)")
	}
}

// TestFileAuditLoggerRespectsMaxBackups verifies that the file audit
// logger keeps at most maxBackups rotated copies. When the chain is
// full, the oldest backup is deleted before the next rotation. This
// bounds disk usage to (maxBackups+1) * maxBytes in the worst case.
func TestFileAuditLoggerRespectsMaxBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	maxBackups := 3
	logger, err := NewFileAuditLoggerWithRotation(path, 50, maxBackups)
	if err != nil {
		t.Fatalf("NewFileAuditLoggerWithRotation: %v", err)
	}

	event := AuditEvent{
		Timestamp: time.Now().UTC(),
		Action:    AuditActionLogin,
		User:      "alice",
		IP:        "10.0.0.1",
		Result:    AuditResultSuccess,
	}
	// Write enough events to trigger many rotations (more than
	// maxBackups).
	for i := 0; i < 20; i++ {
		logger.Log(event)
	}

	// Count backup files (.1, .2, .3, ...).
	count := 0
	for n := 1; n <= maxBackups+5; n++ {
		backup := fmt.Sprintf("%s.%d", path, n)
		if _, err := os.Stat(backup); err == nil {
			count++
		}
	}
	if count > maxBackups {
		t.Errorf("got %d backup files, want at most %d", count, maxBackups)
	}
	// The oldest backup beyond maxBackups should not exist.
	oldest := fmt.Sprintf("%s.%d", path, maxBackups+1)
	if _, err := os.Stat(oldest); err == nil {
		t.Errorf("oldest backup %s should have been deleted", oldest)
	}
}

// TestFileAuditLoggerNoRotationWhenMaxBytesZero verifies that
// maxBytes=0 disables size-based rotation — the file grows without
// bound (the SIGHUP+logrotate path handles rotation externally). This
// is the backward-compatible default.
func TestFileAuditLoggerNoRotationWhenMaxBytesZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	// maxBytes=0: no size-based rotation.
	logger, err := NewFileAuditLoggerWithRotation(path, 0, 5)
	if err != nil {
		t.Fatalf("NewFileAuditLoggerWithRotation: %v", err)
	}

	event := AuditEvent{
		Timestamp: time.Now().UTC(),
		Action:    AuditActionLogin,
		User:      "alice",
		IP:        "10.0.0.1",
		Result:    AuditResultSuccess,
	}
	for i := 0; i < 50; i++ {
		logger.Log(event)
	}

	// No backup files should exist.
	for n := 1; n <= 6; n++ {
		backup := fmt.Sprintf("%s.%d", path, n)
		if _, err := os.Stat(backup); err == nil {
			t.Errorf("backup file %s should not exist (rotation disabled)", backup)
		}
	}
}
