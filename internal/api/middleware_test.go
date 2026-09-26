package api

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
)

// recordingLogger captures structured log output for assertions.
func recordingLogger(buffer *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func TestRequestIDIsPreservedWhenSupplied(t *testing.T) {
	logs := &bytes.Buffer{}
	h := newTestHandler(t, Dependencies{Logger: recordingLogger(logs)})

	req := newRequest(t, "GET", "/health", "")
	req.Header.Set(headerRequestID, "client-supplied-id-1")
	rec := serve(h, req)

	if got := rec.Header().Get(headerRequestID); got != "client-supplied-id-1" {
		t.Errorf("X-Request-ID = %q, want %q", got, "client-supplied-id-1")
	}
	if !strings.Contains(logs.String(), "client-supplied-id-1") {
		t.Errorf("log does not carry the request id: %s", logs.String())
	}
}

func TestRequestIDIsGeneratedWhenAbsent(t *testing.T) {
	logs := &bytes.Buffer{}
	h := newTestHandler(t, Dependencies{Logger: recordingLogger(logs)})

	rec := serve(h, newRequest(t, "GET", "/health", ""))

	got := rec.Header().Get(headerRequestID)
	if got == "" {
		t.Fatal("no X-Request-ID was generated")
	}
	if len(got) > maxRequestIDLength {
		t.Errorf("generated id is %d bytes, want <= %d", len(got), maxRequestIDLength)
	}
	if !strings.Contains(logs.String(), got) {
		t.Errorf("log does not carry the generated request id: %s", logs.String())
	}
}

func TestUnsafeRequestIDsAreReplacedNotRejected(t *testing.T) {
	cases := map[string]string{
		"empty":   "",
		"long":    strings.Repeat("a", maxRequestIDLength+1),
		"spaces":  "has spaces",
		"newline": "line\nbreak",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler(t, testDependencies())
			req := newRequest(t, "GET", "/health", "")
			req.Header.Set(headerRequestID, value)
			rec := serve(h, req)

			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			replaced := rec.Header().Get(headerRequestID)
			if replaced == value && value != "" {
				t.Errorf("unsafe request id %q was accepted unchanged", value)
			}
			if replaced == "" {
				t.Error("no replacement request id was issued")
			}
		})
	}
}

func TestRequestIDAcceptsBoundaryLength(t *testing.T) {
	exact := strings.Repeat("a", maxRequestIDLength)
	if !isSafeRequestID(exact) {
		t.Errorf("a %d character id should be accepted", maxRequestIDLength)
	}
	if isSafeRequestID(exact + "a") {
		t.Errorf("a %d character id should be rejected", maxRequestIDLength+1)
	}
}

func TestSafeResponseHeadersAreAlwaysSet(t *testing.T) {
	h := newTestHandler(t, testDependencies())

	for _, path := range []string{"/health", "/ready", "/openapi.json", "/docs"} {
		rec := serve(h, newRequest(t, "GET", path, ""))
		if got := rec.Header().Get(headerAPIVersion); got != APIVersion {
			t.Errorf("%s: Reusery-API-Version = %q, want %q", path, got, APIVersion)
		}
		if got := rec.Header().Get(headerNoStore); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
		if got := rec.Header().Get(headerNoSniff); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", path, got)
		}
		if rec.Header().Get(headerRequestID) == "" {
			t.Errorf("%s: no X-Request-ID", path)
		}
	}
}

func TestProblemResponsesCarryHeadersAndCode(t *testing.T) {
	deps := testDependencies()
	deps.ExternalOperationsEnabled = false
	h := newTestHandler(t, deps)

	rec := serve(h, newRequest(t, "POST", "/v1/normalize", `{"input":"run child processes"}`))

	requireStatus(t, rec, 503)
	requireContentType(t, rec, "application/problem+json")
	requireCode(t, rec, CodeExternalOperations)
	if rec.Header().Get(headerAPIVersion) != APIVersion {
		t.Error("error response is missing Reusery-API-Version")
	}
	if rec.Header().Get(headerNoStore) != "no-store" {
		t.Error("error response is missing Cache-Control: no-store")
	}
	assertNoSecrets(t, rec)
}

// TestLogsCarryNoBodiesOrSecrets proves the request log is bounded to the
// documented fields and never records payload content.
func TestLogsCarryNoBodiesOrSecrets(t *testing.T) {
	logs := &bytes.Buffer{}
	deps := testDependencies()
	deps.Logger = recordingLogger(logs)
	h := newTestHandler(t, deps)

	body := `{"primitive_id":"process/bounded-subprocess","secret":"sk-this-must-not-be-logged"}`
	rec := serve(h, newRequest(t, "POST", "/v1/resolve", body))
	requireStatus(t, rec, 422)

	line := logs.String()
	for _, forbidden := range []string{"sk-this-must-not-be-logged", "process/bounded-subprocess", "secret"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("log leaked %q: %s", forbidden, line)
		}
	}
	for _, required := range []string{"request_id", "operation_id", "method", "status", "duration_ms"} {
		if !strings.Contains(line, required) {
			t.Errorf("log is missing field %q: %s", required, line)
		}
	}
	if !strings.Contains(line, "operation_id=resolveCandidates") {
		t.Errorf("log does not carry the contract operation id: %s", line)
	}
}

// TestNoCorsPolicyDocumentsPacket8's decision: no wildcard origin header is
// emitted anywhere.
func TestNoCorsPolicy(t *testing.T) {
	h := newTestHandler(t, testDependencies())
	rec := serve(h, newRequest(t, "GET", "/health", ""))
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("Access-Control-Allow-Origin must not be set in Packet 8")
	}
}

// TestResolvedRequestContextReachesHandlers proves cancellation flows from
// the server into a handler's service call.
func TestResolvedRequestContextReachesHandlers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	normalizer := &fakeNormalizer{}
	deps := testDependencies()
	deps.ExternalOperationsEnabled = true
	deps.NormalizerFactory = func() (app.Normalizer, error) { return normalizer, nil }
	h := newTestHandler(t, deps)

	req := newRequest(t, "POST", "/v1/normalize", `{"input":"run child processes"}`)
	req = req.WithContext(ctx)
	rec := serve(h, req)

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 for a cancelled context; body=%s", rec.Code, rec.Body.String())
	}
	if normalizer.calls != 0 {
		t.Errorf("normalizer ran %d time(s) on a cancelled context, want 0", normalizer.calls)
	}
}
