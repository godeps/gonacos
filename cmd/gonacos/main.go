package main

import (
	"context"
	"fmt"
	"os"

	"github.com/godeps/gonacos/internal/app"
)

func main() {
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gonacos: %v\n", err)
		os.Exit(1)
	}
}
