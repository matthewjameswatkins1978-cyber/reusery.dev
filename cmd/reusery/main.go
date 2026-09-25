// Command reusery starts the Reusery.dev HTTP server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/server"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version"
)

// startupTimeout bounds the initial PostgreSQL connection attempt.
const startupTimeout = 15 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		// config errors never include the database URL.
		slog.Error("invalid configuration", "error", err)
		return 1
	}

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

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	pool, err := postgres.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres unavailable at startup", "error", err)
		return 1
	}
	defer pool.Close()
	logger.Info("postgres connected")

	srv := server.New(cfg.HTTPAddr, logger, postgres.ReadyChecker(pool))
	if err := srv.Start(ctx); err != nil {
		logger.Error("server stopped with error", "error", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}
