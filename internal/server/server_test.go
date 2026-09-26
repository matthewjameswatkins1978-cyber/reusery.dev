package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewServesProvidedHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	s := New(":0", testLogger(), mux)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestTimeouts(t *testing.T) {
	if ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 5s", ReadHeaderTimeout)
	}
	if ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want 10s", ReadTimeout)
	}
	if IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", IdleTimeout)
	}
	if WriteTimeout < 65*time.Second {
		t.Errorf("WriteTimeout = %v, want at least 65s so the server never kills a bounded operation first", WriteTimeout)
	}
}

// TestBaseContextPropagatesCancellation proves the serve context becomes the
// parent of every request context. That is the mechanism which carries
// shutdown into PostgreSQL, OpenAI, GitHub, pkg.go.dev and deps.dev.
func TestBaseContextPropagatesCancellation(t *testing.T) {
	mux := http.NewServeMux()
	s := New(":0", testLogger(), mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.httpServer.BaseContext = func(net.Listener) context.Context { return ctx }

	base := s.httpServer.BaseContext(nil)
	if base != ctx {
		t.Fatal("BaseContext does not return the serve context")
	}

	derived, stop := context.WithCancel(base)
	defer stop()

	cancel()

	select {
	case <-derived.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the serve context did not cancel a derived request context")
	}
}
