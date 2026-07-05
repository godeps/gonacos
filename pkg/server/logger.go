package server

import (
	"context"
	"log"
	"os"
	"strings"
)

// LogLevel controls which messages the default logger emits. Lower-severity
// levels are suppressed when the configured level is higher. The default is
// [InfoLevel], matching the documented GONACOS_LOG_LEVEL behavior.
//
// The default [Logger] only exposes [Logger.Infof] and [Logger.Warnf], so
// DebugLevel is currently equivalent to InfoLevel for the standard logger.
// Custom loggers passed via [WithLogger] are responsible for their own
// filtering; the level here only governs the package default.
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// ParseLogLevel parses a level string (case-insensitive). Empty string and
// unknown values fall back to [InfoLevel], so a typo in GONACOS_LOG_LEVEL
// never silently suppresses all logs.
func ParseLogLevel(s string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return DebugLevel
	case "INFO", "":
		return InfoLevel
	case "WARN", "WARNING":
		return WarnLevel
	case "ERROR":
		return ErrorLevel
	default:
		return InfoLevel
	}
}

// String returns the uppercase name of the level.
func (l LogLevel) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Logger is the minimal logging interface used by the Server. Plug in a
// structured logger (zap, zerolog, slog) by wrapping it to match this
// interface and passing it to [WithLogger]. The default logger writes to
// stderr via the standard log package.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// stdLogger adapts the standard log package to the Logger interface. It
// applies level filtering so GONACOS_LOG_LEVEL=WARN suppresses INFO lines
// and GONACOS_LOG_LEVEL=ERROR suppresses both INFO and WARN lines.
type stdLogger struct {
	l     *log.Logger
	level LogLevel
}

// Infof logs at INFO level. Suppressed when the configured level is WARN or
// ERROR.
func (s stdLogger) Infof(format string, args ...any) {
	if s.level > InfoLevel {
		return
	}
	s.l.Printf("INFO  "+format, args...)
}

// Warnf logs at WARN level. Suppressed when the configured level is ERROR.
func (s stdLogger) Warnf(format string, args ...any) {
	if s.level > WarnLevel {
		return
	}
	s.l.Printf("WARN  "+format, args...)
}

// Errorf logs at ERROR level. Always emitted unless the level is set above
// ERROR (which currently does not exist — ERROR is the highest). Use for
// conditions that require operator attention: snapshot load failures, serve
// errors, shutdown failures.
func (s stdLogger) Errorf(format string, args ...any) {
	s.l.Printf("ERROR "+format, args...)
}

// newStdLogger constructs a stdLogger at the given level, writing to stderr
// with the standard log flags (date + time).
func newStdLogger(level LogLevel) *stdLogger {
	return &stdLogger{l: log.New(os.Stderr, "", log.LstdFlags), level: level}
}

// defaultLogger is the package-level default Logger used when [WithLogger]
// is not set and as the fallback in [loggerFromContext]. Writes to stderr
// so it does not collide with stdout-based tooling that may scrape the
// server's output. Always emits at InfoLevel; per-process level filtering
// is applied via [newStdLogger] in [options.resolveLogger].
var defaultLogger Logger = newStdLogger(InfoLevel)

// loggerKey is the context key used to carry a Logger through goroutines
// started by [Server.Start]. Currently informational; reserved for future
// request-scoped logging.
type loggerKey struct{}

// loggerFromContext returns the Logger from ctx, falling back to
// defaultLogger when none is set.
func loggerFromContext(ctx context.Context) Logger {
	if v, ok := ctx.Value(loggerKey{}).(Logger); ok && v != nil {
		return v
	}
	return defaultLogger
}
