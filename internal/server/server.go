// Package server owns the generic HTTP server lifecycle: listen, timeouts,
// cancellation and graceful shutdown.
//
// It knows nothing about Reusery's routes or domain. The API package owns the
// HTTP contract and hands this package an http.Handler.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Timeouts applied to every HTTP server instance.
//
// WriteTimeout is deliberately generous: legitimate bounded API operations
// include two 20-second model calls, three bounded discovery provider passes
// and a 30-second enrichment run. The per-operation budgets inside the handlers
// are always tighter, so the application deadline, never the server deadline,
// is what terminates a slow request.
const (
	ReadTimeout       = 10 * time.Second
	ReadHeaderTimeout = 5 * time.Second
	WriteTimeout      = 65 * time.Second
	IdleTimeout       = 60 * time.Second
)

// ShutdownTimeout bounds graceful shutdown after cancellation.
const ShutdownTimeout = 10 * time.Second

// Server wraps http.Server with the application's handler.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// New builds a Server bound to addr serving handler.
func New(addr string, logger *slog.Logger, handler http.Handler) *Server {
	return &Server{
		logger: logger,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadTimeout:       ReadTimeout,
			ReadHeaderTimeout: ReadHeaderTimeout,
			WriteTimeout:      WriteTimeout,
			IdleTimeout:       IdleTimeout,
		},
	}
}

// Handler exposes the served handler, primarily for tests.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start serves until ctx is cancelled, then shuts down gracefully.
//
// The serve context is also the BaseContext of every request, so cancelling it
// propagates to in-flight request contexts and from there to PostgreSQL,
// OpenAI, GitHub, pkg.go.dev and deps.dev through context.Context. A server
// failure is returned immediately without waiting for ctx.
func (s *Server) Start(ctx context.Context) error {
	s.httpServer.BaseContext = func(net.Listener) context.Context { return ctx }

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		shutdownErr := s.httpServer.Shutdown(shutdownCtx)
		// The serve goroutine always sends exactly one value.
		if serverErr := <-errCh; serverErr != nil {
			return serverErr
		}
		return shutdownErr
	case err := <-errCh:
		return err
	}
}
