package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// get performs a GET request against h and decodes the JSON status body.
func get(t *testing.T, h http.Handler, path string) (int, map[string]string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()

	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return res.StatusCode, body
}

func TestHealthEndpoint(t *testing.T) {
	s := New(":0", testLogger())

	code, body := get(t, s.Handler(), "/health")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpointWithoutCheckers(t *testing.T) {
	s := New(":0", testLogger())

	code, body := get(t, s.Handler(), "/ready")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpointWithPassingChecker(t *testing.T) {
	s := New(":0", testLogger(), func(_ context.Context) error { return nil })

	code, body := get(t, s.Handler(), "/ready")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpointWithFailingChecker(t *testing.T) {
	s := New(":0", testLogger(), func(_ context.Context) error {
		return errors.New("database unreachable")
	})

	code, body := get(t, s.Handler(), "/ready")

	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if body["status"] != "not ready" {
		t.Errorf("body status = %q, want %q", body["status"], "not ready")
	}
}
