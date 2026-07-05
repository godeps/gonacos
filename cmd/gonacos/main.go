package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godeps/gonacos/pkg/app"
	"github.com/godeps/gonacos/pkg/server"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	args := os.Args[1:]
	switch {
	case len(args) > 0 && args[0] == "version":
		fmt.Println(app.Version)
	case len(args) > 0 && args[0] == "serve":
		addr := ":8848"
		if len(args) > 1 {
			addr = args[1]
		}
		srv, err := server.New(server.WithAddr(addr), server.WithRoot("."))
		if err != nil {
			fmt.Fprintf(os.Stderr, "gonacos: %v\n", err)
			os.Exit(1)
		}
		// SIGHUP triggers audit-log reopen. logrotate(8) renames the
		// audit file and sends SIGHUP; this handler swaps the file
		// descriptor so subsequent events land in the new file. Errors
		// go to stderr — a failed reopen must not crash the server, but
		// the operator needs to know rotation failed. The handler is
		// installed only when the audit logger holds a file (ReopenAuditLog
		// is a no-op otherwise), so sending SIGHUP to a process without
		// a file-based audit logger is harmless.
		go installSIGHUPReopen(srv)
		if err := srv.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "gonacos: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Println("usage: gonacos version | gonacos serve [addr]")
	}
}

// installSIGHUPReopen listens for SIGHUP and calls srv.ReopenAuditLog on
// each signal. The goroutine exits when the process is shutting down via
// SIGINT/SIGTERM — we don't subscribe to those here, so the channel never
// delivers them. SIGHUP is the canonical log-rotation signal in Unix
// tradition; sending it during shutdown is a no-op.
func installSIGHUPReopen(srv *server.Server) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for range ch {
		if err := srv.ReopenAuditLog(); err != nil {
			fmt.Fprintf(os.Stderr, "gonacos: SIGHUP audit reopen: %v\n", err)
		}
	}
}
