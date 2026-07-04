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
		if err := srv.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "gonacos: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Println("usage: gonacos version | gonacos serve [addr]")
	}
}
