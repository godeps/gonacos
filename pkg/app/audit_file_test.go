package app

import (
	"encoding/json"
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
