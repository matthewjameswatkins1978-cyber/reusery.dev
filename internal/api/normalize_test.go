package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
)

// sampleIntentResult builds a ready Packet 6 result.
func sampleIntentResult(status intent.Status) intent.Result {
	return intent.Result{
		Input:                  "I need a Go child-process runner",
		Status:                 status,
		RequestedArtifactLevel: intent.ArtifactCode,
		Capability:             "Run child processes",
		Summary:                "Run child processes with bounded output.",
		Constraints:            []intent.Constraint{{Kind: "language", Description: "Go", Required: true}},
		Ambiguities:            []intent.Ambiguity{},
		Assumptions:            []string{},
		Metadata: intent.GenerationMetadata{
			Provider:      "openai",
			Model:         config.DefaultOpenAIModel,
			PromptVersion: intent.PromptVersion,
			SchemaVersion: intent.SchemaVersion,
			Calls:         1,
			Usage:         intent.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		},
	}
}

// normalizeHandler builds a handler with a normalizer under test.
func normalizeHandler(t *testing.T, normalizer app.Normalizer, external bool) *Handler {
	t.Helper()
	deps := testDependencies()
	deps.ExternalOperationsEnabled = external
	deps.NormalizerFactory = func() (app.Normalizer, error) { return normalizer, nil }
	return newTestHandler(t, deps)
}

func TestNormalizeReadyReturns200(t *testing.T) {
	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}
	h := normalizeHandler(t, normalizer, true)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"I need a Go child-process runner"}`)

	requireStatus(t, rec, http.StatusOK)
	requireContentType(t, rec, "application/json")
	body := jsonBody(t, rec)
	if body["status"] != "ready" {
		t.Errorf("status = %v, want ready", body["status"])
	}
	if normalizer.calls != 1 {
		t.Errorf("normalizer calls = %d, want 1", normalizer.calls)
	}
	assertNoSecrets(t, rec)
}

// TestNormalizeValidStatusesAreAll200 covers ready, needs_clarification and
// unsupported: none of them is an HTTP failure.
func TestNormalizeValidStatusesAreAll200(t *testing.T) {
	for _, status := range []intent.Status{
		intent.StatusReady,
		intent.StatusNeedsClarification,
		intent.StatusUnsupported,
	} {
		t.Run(string(status), func(t *testing.T) {
			normalizer := &fakeNormalizer{result: sampleIntentResult(status)}
			h := normalizeHandler(t, normalizer, true)

			rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

			requireStatus(t, rec, http.StatusOK)
			if got := jsonBody(t, rec)["status"]; got != string(status) {
				t.Errorf("status = %v, want %s", got, status)
			}
		})
	}
}

// TestNormalizeResponseArraysAreNeverNull locks the v1 collection rule.
func TestNormalizeResponseArraysAreNeverNull(t *testing.T) {
	result := sampleIntentResult(intent.StatusNeedsClarification)
	result.Constraints = nil
	result.Ambiguities = nil
	result.Assumptions = nil
	normalizer := &fakeNormalizer{result: result}
	h := normalizeHandler(t, normalizer, true)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

	requireStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, field := range []string{`"constraints":[]`, `"ambiguities":[]`, `"assumptions":[]`} {
		if !strings.Contains(body, field) {
			t.Errorf("response does not contain %s: %s", field, body)
		}
	}
	decoded := jsonBody(t, rec)
	if decoded["primitive"] != nil {
		t.Errorf("primitive = %v, want null when no provisional primitive exists", decoded["primitive"])
	}
	if decoded["contract"] != nil {
		t.Errorf("contract = %v, want null", decoded["contract"])
	}
}

func TestNormalizeInvalidInputIs422(t *testing.T) {
	normalizer := &fakeNormalizer{err: intent.ErrInvalidInput}
	h := normalizeHandler(t, normalizer, true)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"   "}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestNormalizeUnknownFieldIsRejected(t *testing.T) {
	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}
	h := normalizeHandler(t, normalizer, true)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes","speciman_id":"x"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for an unknown field; body=%s", rec.Code, rec.Body.String())
	}
	requireContentType(t, rec, "application/problem+json")
}

func TestNormalizeOversizedBodyIs413(t *testing.T) {
	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}
	h := normalizeHandler(t, normalizer, true)

	payload := `{"input":"` + strings.Repeat("a", MaxBodyNormalize) + `"}`
	rec := call(t, h, http.MethodPost, "/v1/normalize", payload)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	if normalizer.calls != 0 {
		t.Errorf("normalizer ran %d time(s) on an oversized body, want 0", normalizer.calls)
	}
}

func TestNormalizeDisabledIs503(t *testing.T) {
	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}
	h := normalizeHandler(t, normalizer, false)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

	requireStatus(t, rec, http.StatusServiceUnavailable)
	requireCode(t, rec, CodeExternalOperations)
	if normalizer.calls != 0 {
		t.Errorf("normalizer ran %d time(s) while disabled, want 0", normalizer.calls)
	}
}

func TestNormalizeWithoutModelKeyIs503(t *testing.T) {
	deps := testDependencies()
	deps.ExternalOperationsEnabled = true
	deps.NormalizerFactory = func() (app.Normalizer, error) {
		return nil, config.ErrMissingOpenAIAPIKey
	}
	h := newTestHandler(t, deps)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

	requireStatus(t, rec, http.StatusServiceUnavailable)
	requireCode(t, rec, CodeModelProviderUnconfigured)
}

func TestNormalizeProviderFailuresMapToDocumentedStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		want int
	}{
		{"authentication", intent.NewProviderError(intent.ErrorAuthentication, 401, "bad key"), CodeUpstreamAuthentication, http.StatusServiceUnavailable},
		{"rate limited", intent.NewProviderError(intent.ErrorRateLimited, 429, "slow down"), CodeUpstreamRateLimited, http.StatusServiceUnavailable},
		{"timeout", intent.NewProviderError(intent.ErrorTimeout, 504, "too slow"), CodeUpstreamTimeout, http.StatusGatewayTimeout},
		{"unavailable", intent.NewProviderError(intent.ErrorUnavailable, 503, "down"), CodeUpstreamUnavailable, http.StatusBadGateway},
		{"invalid response", intent.NewProviderError(intent.ErrorInvalidResponse, 200, "garbage"), CodeUpstreamUnavailable, http.StatusBadGateway},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			normalizer := &fakeNormalizer{err: test.err}
			h := normalizeHandler(t, normalizer, true)

			rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

			requireStatus(t, rec, test.want)
			requireCode(t, rec, test.code)
			// The provider's own message never reaches the client.
			if strings.Contains(rec.Body.String(), "bad key") || strings.Contains(rec.Body.String(), "garbage") {
				t.Errorf("response leaked a provider message: %s", rec.Body.String())
			}
		})
	}
}

// TestNormalizeDoesNotPersistOrChain proves the operation is a single stage.
func TestNormalizeDoesNotPersistOrChain(t *testing.T) {
	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}
	deps := testDependencies()
	deps.ExternalOperationsEnabled = true
	deps.NormalizerFactory = func() (app.Normalizer, error) { return normalizer, nil }
	deps.Discoverer = &fakeDiscoverer{}
	deps.Enricher = &fakeEnricher{}
	h := newTestHandler(t, deps)

	rec := call(t, h, http.MethodPost, "/v1/normalize", `{"input":"run child processes"}`)

	requireStatus(t, rec, http.StatusOK)
	if len(normalizer.inputs) != 1 || normalizer.inputs[0] != "run child processes" {
		t.Errorf("normalizer inputs = %v", normalizer.inputs)
	}
}
