package server

import (
	"context"
	"log"
	"os"
)

// Logger is the minimal logging interface used by the Server. Plug in a
// structured logger (zap, zerolog, slog) by wrapping it to match this
// interface and passing it to [WithLogger]. The default logger writes to
// stderr via the standard log package.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

// stdLogger adapts the standard log package to the Logger interface.
type stdLogger struct {
	l *log.Logger
}

func (s stdLogger) Infof(format string, args ...any) {
	s.l.Printf("INFO  "+format, args...)
}

func (s stdLogger) Warnf(format string, args ...any) {
	s.l.Printf("WARN  "+format, args...)
}

// defaultLogger is the package-level default Logger used when [WithLogger]
// is not set. Writes to stderr so it does not collide with stdout-based
// tooling that may scrape the server's output.
var defaultLogger Logger = stdLogger{l: log.New(os.Stderr, "", log.LstdFlags)}

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
