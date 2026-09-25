// Package server builds the HTTP server and its routes.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Timeouts applied to every HTTP server instance.
const (
	ReadTimeout       = 10 * time.Second
	ReadHeaderTimeout = 5 * time.Second
	WriteTimeout      = 10 * time.Second
	IdleTimeout       = 60 * time.Second
)

// ShutdownTimeout bounds graceful shutdown after cancellation.
const ShutdownTimeout = 10 * time.Second

// ReadyChecker reports whether a dependency required for serving traffic is
// ready. Packet 1 registers no checkers; Packet 2 will add PostgreSQL here.
type ReadyChecker func(ctx context.Context) error

// Server wraps http.Server with the application's routes.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	checkers   []ReadyChecker
}

// New builds a Server bound to addr. Pass ReadyCheckers to extend /ready.
func New(addr string, logger *slog.Logger, checkers ...ReadyChecker) *Server {
	s := &Server{logger: logger, checkers: checkers}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       ReadTimeout,
		ReadHeaderTimeout: ReadHeaderTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
	}
	return s
}

// Handler exposes the route mux, primarily for tests.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start serves until ctx is cancelled, then shuts down gracefully.
// A server failure is returned immediately without waiting for ctx.
func (s *Server) Start(ctx context.Context) error {
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

// handleHealth reports liveness. It never depends on external systems.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeStatus(w, http.StatusOK, "ok")
}

// handleReady reports readiness. Each registered checker must pass;
// with no checkers (Packet 1) readiness is trivially ok.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	for _, check := range s.checkers {
		if err := check(r.Context()); err != nil {
			s.logger.Warn("readiness check failed", "error", err)
			s.writeStatus(w, http.StatusServiceUnavailable, "not ready")
			return
		}
	}
	s.writeStatus(w, http.StatusOK, "ok")
}

func (s *Server) writeStatus(w http.ResponseWriter, code int, status string) {
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		s.logger.Error("failed to encode status response", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		s.logger.Warn("failed to write status response", "error", err)
	}
}
