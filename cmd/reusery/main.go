// Command reusery is the Reusery.dev command line and HTTP server entry point.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.New().Run(ctx, os.Args[1:]))
}
