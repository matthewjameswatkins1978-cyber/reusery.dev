// Command reusery starts the Reusery.dev HTTP server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/server"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	attrs := make([]any, 0, len(version.Info())*2+2)
	attrs = append(attrs, "addr", cfg.HTTPAddr)
	for k, v := range version.Info() {
		attrs = append(attrs, k, v)
	}
	logger.Info("starting reusery", attrs...)

	srv := server.New(cfg.HTTPAddr, logger)
	if err := srv.Start(ctx); err != nil {
		logger.Error("server stopped with error", "error", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}
