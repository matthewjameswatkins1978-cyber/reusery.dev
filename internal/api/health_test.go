package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/server"
)

// get performs a GET request against h and decodes the JSON status body,
// preserving the Packet 1 health/readiness assertions exactly.
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
	h := newTestHandler(t, testDependencies())

	code, body := get(t, h, "/health")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

// TestHealthNeverCallsReadinessChecker preserves Packet 1: liveness is
// independent of every dependency, including a failing one.
func TestHealthNeverCallsReadinessChecker(t *testing.T) {
	calls := 0
	h := newTestHandler(t, Dependencies{
		Logger: testDependencies().Logger,
		ReadyCheckers: readyCheckers(func(context.Context) error {
			calls++
			return errors.New("database unreachable")
		}),
	})

	code, body := get(t, h, "/health")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
	if calls != 0 {
		t.Errorf("readiness checker ran %d time(s) during /health, want 0", calls)
	}
}

func TestReadyEndpointWithoutCheckers(t *testing.T) {
	h := newTestHandler(t, testDependencies())

	code, body := get(t, h, "/ready")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpointWithPassingChecker(t *testing.T) {
	h := newTestHandler(t, Dependencies{
		Logger:        testDependencies().Logger,
		ReadyCheckers: readyCheckers(func(context.Context) error { return nil }),
	})

	code, body := get(t, h, "/ready")

	if code != http.StatusOK {
		t.Errorf("status = %d, want %d", code, http.StatusOK)
	}
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadyEndpointWithFailingChecker(t *testing.T) {
	h := newTestHandler(t, Dependencies{
		Logger:        testDependencies().Logger,
		ReadyCheckers: readyCheckers(func(context.Context) error { return errors.New("database unreachable") }),
	})

	code, body := get(t, h, "/ready")

	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if body["status"] != "not ready" {
		t.Errorf("body status = %q, want %q", body["status"], "not ready")
	}
}

// TestWriteTimeoutOutlivesEveryOperationBudget keeps the corrected global
// WriteTimeout above the longest documented per-operation budget, so the
// server never terminates a legitimate bounded operation first.
func TestWriteTimeoutOutlivesEveryOperationBudget(t *testing.T) {
	budgets := []time.Duration{
		BudgetHealth,
		BudgetReady,
		BudgetInspect,
		BudgetResolve,
		BudgetRefine,
		BudgetNormalize,
		BudgetDiscover,
		BudgetEnrich,
	}
	var longest time.Duration
	for _, value := range budgets {
		if value <= 0 {
			t.Errorf("budget %v is not positive", value)
		}
		if value > longest {
			longest = value
		}
	}
	if longest >= server.WriteTimeout {
		t.Errorf("longest budget %v is not below the server WriteTimeout %v", longest, server.WriteTimeout)
	}
	if server.WriteTimeout != 65*time.Second {
		t.Errorf("WriteTimeout = %v, want 65s", server.WriteTimeout)
	}
}
