package server

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestParseLogLevel covers case-insensitive parsing and fallback to INFO
// for unknown values so a typo in GONACOS_LOG_LEVEL never silently
// suppresses all logs.
func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want LogLevel
	}{
		{"", InfoLevel},
		{"debug", DebugLevel},
		{"DEBUG", DebugLevel},
		{"info", InfoLevel},
		{"INFO", InfoLevel},
		{"warn", WarnLevel},
		{"WARN", WarnLevel},
		{"warning", WarnLevel},
		{"WARNING", WarnLevel},
		{"error", ErrorLevel},
		{"ERROR", ErrorLevel},
		{"bogus", InfoLevel}, // unknown falls back to INFO
		{"  debug  ", DebugLevel},
	}
	for _, c := range cases {
		got := ParseLogLevel(c.in)
		if got != c.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestLogLevelString covers the String() method so log output and tests
// can rely on the canonical uppercase name.
func TestLogLevelString(t *testing.T) {
	cases := []struct {
		level LogLevel
		want  string
	}{
		{DebugLevel, "DEBUG"},
		{InfoLevel, "INFO"},
		{WarnLevel, "WARN"},
		{ErrorLevel, "ERROR"},
		{LogLevel(99), "INFO"}, // unknown falls back to INFO
	}
	for _, c := range cases {
		if got := c.level.String(); got != c.want {
			t.Errorf("level %d String() = %q, want %q", c.level, got, c.want)
		}
	}
}

// TestStdLoggerLevelFiltering verifies that the default logger suppresses
// lower-severity messages based on the configured level. This is the
// behavior that GONACOS_LOG_LEVEL controls at the process level.
func TestStdLoggerLevelFiltering(t *testing.T) {
	cases := []struct {
		name     string
		level    LogLevel
		infoWant bool // whether INFO line should appear
		warnWant bool // whether WARN line should appear
		errWant  bool // whether ERROR line should appear
	}{
		{"debug emits all", DebugLevel, true, true, true},
		{"info emits all", InfoLevel, true, true, true},
		{"warn suppresses info", WarnLevel, false, true, true},
		{"error suppresses info+warn", ErrorLevel, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := &stdLogger{
				l:     log.New(&buf, "", 0),
				level: c.level,
			}
			lg.Infof("info msg %d", 1)
			lg.Warnf("warn msg %d", 2)
			lg.Errorf("err msg %d", 3)

			got := buf.String()
			hasInfo := strings.Contains(got, "INFO  info msg 1")
			hasWarn := strings.Contains(got, "WARN  warn msg 2")
			hasErr := strings.Contains(got, "ERROR err msg 3")
			if hasInfo != c.infoWant {
				t.Errorf("INFO line: got %v, want %v. buffer=%q", hasInfo, c.infoWant, got)
			}
			if hasWarn != c.warnWant {
				t.Errorf("WARN line: got %v, want %v. buffer=%q", hasWarn, c.warnWant, got)
			}
			if hasErr != c.errWant {
				t.Errorf("ERROR line: got %v, want %v. buffer=%q", hasErr, c.errWant, got)
			}
		})
	}
}

// TestResolveLogLevelFromOption verifies that WithLogLevel takes precedence
// over the GONACOS_LOG_LEVEL env var and the default.
func TestResolveLogLevelFromOption(t *testing.T) {
	t.Setenv("GONACOS_LOG_LEVEL", "ERROR")
	o := options{}
	if got := o.resolveLogLevel(); got != ErrorLevel {
		t.Fatalf("env var: got %v, want ERROR", got)
	}

	lvl := WarnLevel
	o.LogLevel = &lvl
	if got := o.resolveLogLevel(); got != WarnLevel {
		t.Fatalf("explicit option: got %v, want WARN", got)
	}
}

// TestResolveLogLevelFromEnv verifies that GONACOS_LOG_LEVEL is honored
// when no explicit option is set.
func TestResolveLogLevelFromEnv(t *testing.T) {
	o := options{}
	t.Setenv("GONACOS_LOG_LEVEL", "warn")
	if got := o.resolveLogLevel(); got != WarnLevel {
		t.Fatalf("env WARN: got %v, want WARN", got)
	}
}

// TestResolveLogLevelDefault verifies the default is INFO when neither the
// option nor the env var is set.
func TestResolveLogLevelDefault(t *testing.T) {
	o := options{}
	t.Setenv("GONACOS_LOG_LEVEL", "")
	if got := o.resolveLogLevel(); got != InfoLevel {
		t.Fatalf("default: got %v, want INFO", got)
	}
}

// TestResolveLoggerAppliesLevel verifies that resolveLogger returns a
// stdLogger that honors the configured level.
func TestResolveLoggerAppliesLevel(t *testing.T) {
	t.Setenv("GONACOS_LOG_LEVEL", "ERROR")
	o := options{}
	lg := o.resolveLogger()

	// Verify by type assertion that the underlying stdLogger's level
	// matches the configured env var.
	sl, ok := lg.(*stdLogger)
	if !ok {
		t.Fatalf("expected *stdLogger, got %T", lg)
	}
	if sl.level != ErrorLevel {
		t.Fatalf("level: got %v, want ERROR", sl.level)
	}
}
