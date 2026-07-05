package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
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

// LogFormat selects the output format of the default logger. TextFormat
// (the default) writes "LEVEL  message" lines for humans; JSONFormat writes
// one JSON object per line for log collectors (ELK, Loki, Datadog).
type LogFormat int

const (
	TextFormat LogFormat = iota
	JSONFormat
)

// ParseLogFormat parses a format string (case-insensitive). Empty string
// and unknown values fall back to TextFormat, so a typo in
// GONACOS_LOG_FORMAT never breaks the server.
func ParseLogFormat(s string) LogFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return JSONFormat
	default:
		return TextFormat
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

// SetLeveler is an optional interface implemented by loggers that support
// runtime level switching. The default stdLogger and jsonLogger implement
// it; custom loggers passed via [WithLogger] can opt-in by implementing
// SetLevel themselves.
//
// The Server exposes [Server.SetLogLevel] as the public entry point so
// callers do not need to type-assert on this interface themselves.
// [Server.SetLogLevel] returns false when the underlying logger does not
// implement SetLeveler, letting operators know the runtime switch is a
// no-op for their chosen logger.
type SetLeveler interface {
	SetLevel(LogLevel)
}

// Leveler is an optional interface implemented by loggers that can report
// their current level. The default stdLogger and jsonLogger implement it
// by reading the atomic.Int32 that backs SetLevel. Used by
// [Server.GetLogLevel] so the ops endpoint can show the actual current
// level rather than the last requested one — important when an operator
// wants to confirm a switch landed.
type Leveler interface {
	Level() LogLevel
}

// stdLogger adapts the standard log package to the Logger interface. It
// applies level filtering so GONACOS_LOG_LEVEL=WARN suppresses INFO lines
// and GONACOS_LOG_LEVEL=ERROR suppresses both INFO and WARN lines.
//
// The level is stored in an atomic.Int32 so [Server.SetLogLevel] can flip
// it without taking a lock — useful when an operator hits a runaway-loop
// log line and needs to drop from INFO to ERROR mid-flight. Reads in the
// hot path (every Infof/Warnf call) cost a single atomic load.
type stdLogger struct {
	l     *log.Logger
	level atomic.Int32
}

// Infof logs at INFO level. Suppressed when the configured level is WARN or
// ERROR.
func (s *stdLogger) Infof(format string, args ...any) {
	if LogLevel(s.level.Load()) > InfoLevel {
		return
	}
	s.l.Printf("INFO  "+format, args...)
}

// Warnf logs at WARN level. Suppressed when the configured level is ERROR.
func (s *stdLogger) Warnf(format string, args ...any) {
	if LogLevel(s.level.Load()) > WarnLevel {
		return
	}
	s.l.Printf("WARN  "+format, args...)
}

// Errorf logs at ERROR level. Always emitted unless the level is set above
// ERROR (which currently does not exist — ERROR is the highest). Use for
// conditions that require operator attention: snapshot load failures, serve
// errors, shutdown failures.
func (s *stdLogger) Errorf(format string, args ...any) {
	s.l.Printf("ERROR "+format, args...)
}

// SetLevel atomically swaps the active log level. Subsequent Infof/Warnf
// calls observe the new level without restart. Safe for concurrent use.
func (s *stdLogger) SetLevel(level LogLevel) {
	s.level.Store(int32(level))
}

// Level returns the current effective log level. Reads the underlying
// atomic.Int32 so the value reflects the most recent SetLevel call —
// used by [Server.GetLogLevel] to report state to operators.
func (s *stdLogger) Level() LogLevel {
	return LogLevel(s.level.Load())
}

// newStdLogger constructs a stdLogger at the given level, writing to stderr
// with the standard log flags (date + time).
func newStdLogger(level LogLevel) *stdLogger {
	s := &stdLogger{l: log.New(os.Stderr, "", log.LstdFlags)}
	s.level.Store(int32(level))
	return s
}

// jsonLogger writes one JSON object per line to stderr. Each line is
// {"ts":"2026-07-05T15:04:05.123Z","level":"INFO","msg":"..."}. The
// timestamp is RFC3339 with milliseconds so log collectors can order
// events without parsing the message. The msg field is the printf-style
// format string with args applied — structured fields are not supported
// by the Logger interface (callers wrap zap/zerolog/slog for that).
//
// Like stdLogger, the level is held in an atomic.Int32 so runtime
// switches via [Server.SetLogLevel] take effect immediately.
type jsonLogger struct {
	l     *log.Logger
	level atomic.Int32
}

// newJSONLogger constructs a jsonLogger at the given level.
func newJSONLogger(level LogLevel) *jsonLogger {
	j := &jsonLogger{l: log.New(os.Stderr, "", 0)}
	j.level.Store(int32(level))
	return j
}

// emit writes a single JSON line. Level filtering happens before the
// marshal so a suppressed message costs only a comparison.
func (s *jsonLogger) emit(level LogLevel, format string, args ...any) {
	if LogLevel(s.level.Load()) > level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	rec := map[string]string{
		"ts":    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"level": level.String(),
		"msg":   msg,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		// Should not happen — the map is all strings. Fall back to text.
		s.l.Printf("%s %s", level, msg)
		return
	}
	s.l.Println(string(line))
}

func (s *jsonLogger) Infof(format string, args ...any) {
	s.emit(InfoLevel, format, args...)
}

func (s *jsonLogger) Warnf(format string, args ...any) {
	s.emit(WarnLevel, format, args...)
}

func (s *jsonLogger) Errorf(format string, args ...any) {
	s.emit(ErrorLevel, format, args...)
}

// SetLevel atomically swaps the active log level. Subsequent emit calls
// observe the new level without restart. Safe for concurrent use.
func (s *jsonLogger) SetLevel(level LogLevel) {
	s.level.Store(int32(level))
}

// Level returns the current effective log level. Reads the underlying
// atomic.Int32 so the value reflects the most recent SetLevel call —
// used by [Server.GetLogLevel] to report state to operators.
func (s *jsonLogger) Level() LogLevel {
	return LogLevel(s.level.Load())
}

// Compile-time assertions that stdLogger and jsonLogger satisfy both Logger
// and SetLeveler. If either method set drifts, the build breaks here
// instead of at a call site.
var (
	_ Logger     = (*stdLogger)(nil)
	_ SetLeveler = (*stdLogger)(nil)
	_ Leveler    = (*stdLogger)(nil)
	_ Logger     = (*jsonLogger)(nil)
	_ SetLeveler = (*jsonLogger)(nil)
	_ Leveler    = (*jsonLogger)(nil)
)

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
