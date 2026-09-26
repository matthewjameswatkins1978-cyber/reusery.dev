package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

func enrichHandler(t *testing.T, enricher app.Enricher, external bool) *Handler {
	t.Helper()
	deps := testDependencies()
	deps.ExternalOperationsEnabled = external
	deps.Enricher = enricher
	return newTestHandler(t, deps)
}

// sampleEnrichment builds a success result carrying an INFO observation only.
func sampleEnrichment() enrichment.Result {
	observed := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	return enrichment.Result{
		ObservedAt: observed,
		Evidence: []model.Evidence{{
			ID:         "enrichment/deps.dev/abc",
			SubjectID:  "public/pkg.go.dev/example/pkg@v1.0.0",
			Kind:       "source_license",
			Claim:      "deps.dev reported license MIT",
			Result:     model.EvidenceInfo,
			ObservedAt: observed,
		}},
		Specimens: []enrichment.SpecimenReport{{
			SpecimenID:    "public/pkg.go.dev/example/pkg@v1.0.0",
			Supported:     true,
			EvidenceCount: 1,
			Providers: []enrichment.ProviderReport{{
				ID:            "deps.dev",
				Succeeded:     true,
				Requests:      1,
				EvidenceCount: 1,
				Issues:        []enrichment.ProviderIssue{},
			}},
		}},
	}
}

func TestEnrichSuccessReturns200(t *testing.T) {
	enricher := &fakeEnricher{result: sampleEnrichment()}
	h := enrichHandler(t, enricher, true)

	rec := call(t, h, http.MethodPost, "/v1/enrich",
		`{"specimen_ids":["public/pkg.go.dev/example/pkg@v1.0.0"]}`)

	requireStatus(t, rec, http.StatusOK)
	if enricher.calls != 1 {
		t.Errorf("enricher calls = %d, want 1", enricher.calls)
	}
	body := rec.Body.String()
	// Enrichment can never emit behavioural evidence.
	if strings.Contains(body, `"result":"pass"`) || strings.Contains(body, `"result":"fail"`) {
		t.Errorf("enrichment produced behavioural evidence: %s", body)
	}
	assertNoSecrets(t, rec)
}

func TestEnrichPartialProviderFailureReturns200(t *testing.T) {
	result := sampleEnrichment()
	result.Specimens[0].Providers = append(result.Specimens[0].Providers, enrichment.ProviderReport{
		ID:        "github-metadata",
		Succeeded: false,
		Issues: []enrichment.ProviderIssue{{
			Kind:       enrichment.IssueRateLimited,
			Provider:   "github-metadata",
			SpecimenID: "public/pkg.go.dev/example/pkg@v1.0.0",
			StatusCode: 429,
			Message:    "rate limited by the provider",
		}},
	})
	enricher := &fakeEnricher{result: result}
	h := enrichHandler(t, enricher, true)

	rec := call(t, h, http.MethodPost, "/v1/enrich",
		`{"specimen_ids":["public/pkg.go.dev/example/pkg@v1.0.0"]}`)

	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "rate limited by the provider") {
		t.Errorf("provider issue missing: %s", rec.Body.String())
	}
}

func TestEnrichAllProvidersFailedIsUpstream(t *testing.T) {
	result := sampleEnrichment()
	result.Specimens[0].Providers = []enrichment.ProviderReport{{
		ID:        "deps.dev",
		Succeeded: false,
		Issues: []enrichment.ProviderIssue{{
			Kind:     enrichment.IssueUnavailable,
			Provider: "deps.dev",
			Message:  "provider unavailable",
		}},
	}}
	enricher := &fakeEnricher{
		result: result,
		err:    &enrichment.ProvidersFailedError{Providers: []string{"deps.dev"}},
	}
	h := enrichHandler(t, enricher, true)

	rec := call(t, h, http.MethodPost, "/v1/enrich",
		`{"specimen_ids":["public/pkg.go.dev/example/pkg@v1.0.0"]}`)

	requireStatus(t, rec, http.StatusBadGateway)
	requireCode(t, rec, CodeAllProvidersFailed)
	if !strings.Contains(rec.Body.String(), "provider unavailable") {
		t.Errorf("safe provider issue not preserved: %s", rec.Body.String())
	}
	assertNoSecrets(t, rec)
}

func TestEnrichRequestShapeErrorsAre422(t *testing.T) {
	tooMany := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		tooMany = append(tooMany, "public/pkg.go.dev/example/pkg@v1.0.0")
	}

	quoted := make([]string, 0, len(tooMany))
	for _, id := range tooMany {
		quoted = append(quoted, `"`+id+`"`)
	}

	cases := map[string]struct {
		body string
		err  error
	}{
		"empty":     {`{"specimen_ids":[]}`, enrichment.ErrNoSpecimens},
		"absent":    {`{}`, enrichment.ErrNoSpecimens},
		"duplicate": {`{"specimen_ids":["a","a"]}`, enrichment.ErrDuplicateSpecimen},
		"too many":  {`{"specimen_ids":[` + strings.Join(quoted, ",") + `]}`, enrichment.ErrTooManySpecimens},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			h := enrichHandler(t, &fakeEnricher{err: test.err}, true)

			rec := call(t, h, http.MethodPost, "/v1/enrich", test.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			requireContentType(t, rec, "application/problem+json")
		})
	}
}

func TestEnrichEvidenceConflictIs409(t *testing.T) {
	enricher := &fakeEnricher{
		result: sampleEnrichment(),
		err:    enrichment.ErrEvidenceConflict,
	}
	h := enrichHandler(t, enricher, true)

	rec := call(t, h, http.MethodPost, "/v1/enrich",
		`{"specimen_ids":["public/pkg.go.dev/example/pkg@v1.0.0"]}`)

	requireStatus(t, rec, http.StatusConflict)
	requireCode(t, rec, CodeConflict)
}

func TestEnrichDisabledIs503(t *testing.T) {
	enricher := &fakeEnricher{result: sampleEnrichment()}
	h := enrichHandler(t, enricher, false)

	rec := call(t, h, http.MethodPost, "/v1/enrich",
		`{"specimen_ids":["public/pkg.go.dev/example/pkg@v1.0.0"]}`)

	requireStatus(t, rec, http.StatusServiceUnavailable)
	requireCode(t, rec, CodeExternalOperations)
	if enricher.calls != 0 {
		t.Errorf("enricher ran %d time(s) while disabled, want 0", enricher.calls)
	}
}
