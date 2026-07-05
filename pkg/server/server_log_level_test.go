package server

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestServerSetLogLevel verifies that Server.SetLogLevel switches the
// active logger's level and returns true when the logger supports runtime
// switching. Paired with GetLogLevel to confirm the new level is reported
// back. Operators use this path through POST /v3/admin/ops/log/level to
// silence a runaway INFO loop without restarting the server.
func TestServerSetLogLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := &stdLogger{l: log.New(&buf, "", 0)}
	logger.SetLevel(InfoLevel)

	s := &Server{logger: logger}

	// Initial state: INFO, supported.
	lvl, supported := s.GetLogLevel()
	if !supported || lvl != InfoLevel {
		t.Fatalf("GetLogLevel: level=%v supported=%v, want INFO/true", lvl, supported)
	}

	// Switch to WARN — subsequent INFO should be suppressed.
	if !s.SetLogLevel(WarnLevel) {
		t.Fatal("SetLogLevel(WARN) returned false, want true")
	}
	lvl, _ = s.GetLogLevel()
	if lvl != WarnLevel {
		t.Fatalf("GetLogLevel after switch: %v, want WARN", lvl)
	}

	buf.Reset()
	logger.Infof("info suppressed")
	logger.Warnf("warn passes")
	out := buf.String()
	if strings.Contains(out, "info suppressed") {
		t.Fatalf("INFO not suppressed after switch to WARN: %q", out)
	}
	if !strings.Contains(out, "WARN  warn passes") {
		t.Fatalf("WARN not emitted at WARN level: %q", out)
	}
}

// TestServerSetLogLevelUnsupportedLogger verifies that SetLogLevel returns
// false when the active logger does not implement SetLeveler — operators
// rely on the boolean to decide whether a rolling restart is needed.
// GetLogLevel similarly reports supported=false.
func TestServerSetLogLevelUnsupportedLogger(t *testing.T) {
	// stubCustomLogger is a Logger that does NOT implement SetLeveler or
	// Leveler — simulates a user-supplied zap/zerolog wrapper that has
	// not opted into runtime switching.
	s := &Server{logger: stubCustomLogger{}}
	if s.SetLogLevel(WarnLevel) {
		t.Fatal("SetLogLevel returned true for unsupported logger, want false")
	}
	lvl, supported := s.GetLogLevel()
	if supported {
		t.Fatal("GetLogLevel supported=true for unsupported logger, want false")
	}
	if lvl != InfoLevel {
		t.Fatalf("GetLogLevel level=%v, want INFO (default)", lvl)
	}
}

// stubCustomLogger is a Logger that does not implement SetLeveler or
// Leveler — used to verify the unsupported-logger path.
type stubCustomLogger struct{}

func (stubCustomLogger) Infof(format string, args ...any)  {}
func (stubCustomLogger) Warnf(format string, args ...any)  {}
func (stubCustomLogger) Errorf(format string, args ...any) {}

// TestServerSetLogLevelNilLogger verifies the nil-safety of SetLogLevel and
// GetLogLevel — a nil Server or nil logger returns false/InfoLevel
// instead of panicking. This guards the ops endpoint against a partially
// constructed Server.
func TestServerSetLogLevelNilLogger(t *testing.T) {
	var s *Server
	if s.SetLogLevel(WarnLevel) {
		t.Fatal("SetLogLevel on nil Server returned true, want false")
	}
	lvl, supported := s.GetLogLevel()
	if supported {
		t.Fatal("GetLogLevel on nil Server returned supported=true, want false")
	}
	if lvl != InfoLevel {
		t.Fatalf("GetLogLevel on nil Server: level=%v, want INFO", lvl)
	}

	// Server with nil logger — also safe.
	s2 := &Server{}
	if s2.SetLogLevel(WarnLevel) {
		t.Fatal("SetLogLevel on Server with nil logger returned true, want false")
	}
	lvl, supported = s2.GetLogLevel()
	if supported {
		t.Fatal("GetLogLevel on Server with nil logger returned supported=true, want false")
	}
}
