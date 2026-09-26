package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// orderedEvidence sorts observations the way the store does: observed_at
// ascending then evidence id ascending.
func orderedEvidence(values []model.Evidence) []model.Evidence {
	out := append([]model.Evidence(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ObservedAt.Before(out[j].ObservedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ------------------------------------------------------------------- fakes

type fakeNormalizer struct {
	result intent.Result
	err    error
	calls  int
	inputs []string
}

func (f *fakeNormalizer) Normalize(ctx context.Context, input string) (intent.Result, error) {
	if err := ctx.Err(); err != nil {
		return intent.Result{}, err
	}
	f.calls++
	f.inputs = append(f.inputs, input)
	return f.result, f.err
}

type fakeDiscoverer struct {
	result  discovery.Result
	err     error
	calls   int
	profile discovery.Profile
}

func (f *fakeDiscoverer) Discover(_ context.Context, profile discovery.Profile) (discovery.Result, error) {
	f.calls++
	f.profile = profile
	return f.result, f.err
}

type fakeEnricher struct {
	result enrichment.Result
	err    error
	calls  int
	ids    []string
}

func (f *fakeEnricher) Enrich(_ context.Context, ids []string) (enrichment.Result, error) {
	f.calls++
	f.ids = append([]string(nil), ids...)
	return f.result, f.err
}

// --------------------------------------------------------------- helpers

// readyCheckers converts plain functions into the readiness checker type.
func readyCheckers(fns ...func(context.Context) error) []app.ReadyChecker {
	out := make([]app.ReadyChecker, 0, len(fns))
	for _, fn := range fns {
		out = append(out, app.ReadyChecker(fn))
	}
	return out
}

func testDependencies() Dependencies {
	return Dependencies{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newTestHandler(t *testing.T, deps Dependencies) *Handler {
	t.Helper()
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return NewHandler(deps)
}

// newRequest builds one API request with an optional JSON body.
func newRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// serve performs one request against the handler.
func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// call performs one request against the handler and returns the recorder.
func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(h, newRequest(t, method, path, body))
}

// jsonBody decodes a JSON response body.
func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	return out
}

// requireStatus fails unless the response has the expected status.
func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, want, rec.Body.String())
	}
}

// requireCode fails unless the problem body carries the expected code.
func requireCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	body := jsonBody(t, rec)
	if got := body["code"]; got != want {
		t.Fatalf("code = %v, want %q; body=%s", got, want, rec.Body.String())
	}
}

// requireContentType fails unless the response content type matches.
func requireContentType(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(encoded)
}

// assertNoSecrets scans a response for anything that must never leak.
func assertNoSecrets(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	for _, forbidden := range []string{
		"REUSERY_DATABASE_URL", "postgres://", "sk-", "ghp_", "Authorization",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response leaked %q: %s", forbidden, truncate(body, 400))
		}
	}
}
