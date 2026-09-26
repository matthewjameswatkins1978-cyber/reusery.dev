package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// discoverBody renders a valid bounded discovery profile.
func discoverBody(providers ...map[string]any) string {
	if providers == nil {
		providers = []map[string]any{{
			"id":      "pkg.go.dev",
			"queries": []map[string]any{{"text": "subprocess runner cancellation", "limit": 5}},
		}}
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"primitive_id":   "process/bounded-subprocess",
		"contract_id":    "process/bounded-subprocess/v1",
		"providers":      providers,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func discoverHandler(t *testing.T, discoverer app.Discoverer, external bool) *Handler {
	t.Helper()
	deps := testDependencies()
	deps.ExternalOperationsEnabled = external
	deps.Discoverer = discoverer
	return newTestHandler(t, deps)
}

// sampleDiscovery builds a success result carrying an INFO observation only.
func sampleDiscovery() discovery.Result {
	observed := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	return discovery.Result{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		ObservedAt:  observed,
		Candidates: []discovery.Candidate{{
			ProviderID: "pkg.go.dev",
			Specimen: model.Specimen{
				ID:          "public/pkg.go.dev/example/pkg@v1.0.0",
				PrimitiveID: "process/bounded-subprocess",
				Name:        "example/pkg",
				ReuseMode:   []model.ReuseMode{model.ReuseDependency},
				Source:      model.SourceRef{URL: "https://pkg.go.dev/example/pkg", Revision: "v1.0.0"},
			},
			Evidence: []model.Evidence{{
				ID:         "discovery/pkg.go.dev/abc",
				SubjectID:  "public/pkg.go.dev/example/pkg@v1.0.0",
				Kind:       "discovery_match",
				Claim:      "surfaced by pkg.go.dev for query \"subprocess runner cancellation\"",
				Result:     model.EvidenceInfo,
				ObservedAt: observed,
			}},
		}},
		Providers: []discovery.ProviderReport{{
			ID:             "pkg.go.dev",
			Succeeded:      true,
			Requests:       1,
			CandidateCount: 1,
			Issues:         []discovery.ProviderIssue{},
		}},
	}
}

func TestDiscoverSuccessReturns200(t *testing.T) {
	discoverer := &fakeDiscoverer{result: sampleDiscovery()}
	h := discoverHandler(t, discoverer, true)

	rec := call(t, h, http.MethodPost, "/v1/discover", discoverBody())

	requireStatus(t, rec, http.StatusOK)
	body := jsonBody(t, rec)
	if discoverer.calls != 1 {
		t.Errorf("discoverer calls = %d, want 1", discoverer.calls)
	}
	if discoverer.profile.PrimitiveID != "process/bounded-subprocess" {
		t.Errorf("profile primitive = %q", discoverer.profile.PrimitiveID)
	}
	if body["primitive_id"] != "process/bounded-subprocess" {
		t.Errorf("primitive_id = %v", body["primitive_id"])
	}
	// Discovery remains INFO/UNKNOWN only.
	if strings.Contains(rec.Body.String(), `"result":"pass"`) || strings.Contains(rec.Body.String(), `"result":"fail"`) {
		t.Errorf("discovery produced behavioural evidence: %s", rec.Body.String())
	}
	assertNoSecrets(t, rec)
}

func TestDiscoverZeroCandidatesReturns200(t *testing.T) {
	result := sampleDiscovery()
	result.Candidates = nil
	discoverer := &fakeDiscoverer{result: result}
	h := discoverHandler(t, discoverer, true)

	rec := call(t, h, http.MethodPost, "/v1/discover", discoverBody())

	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"candidates":[]`) {
		t.Errorf("candidates was not serialised as []: %s", rec.Body.String())
	}
}

func TestDiscoverPartialProviderFailureReturns200WithIssues(t *testing.T) {
	result := sampleDiscovery()
	result.Providers = append(result.Providers, discovery.ProviderReport{
		ID:        "github-code",
		Succeeded: false,
		Requests:  1,
		Issues: []discovery.ProviderIssue{{
			Kind:       discovery.IssueRateLimited,
			Provider:   "github-code",
			Message:    "rate limited by the provider",
			StatusCode: 429,
		}},
	})
	discoverer := &fakeDiscoverer{result: result}
	h := discoverHandler(t, discoverer, true)

	rec := call(t, h, http.MethodPost, "/v1/discover", discoverBody())

	requireStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "rate limited by the provider") {
		t.Errorf("provider issue missing from the response: %s", body)
	}
	if !strings.Contains(body, `"succeeded":false`) {
		t.Errorf("failed provider report missing: %s", body)
	}
}

func TestDiscoverAllProvidersFailedIsUpstream(t *testing.T) {
	result := sampleDiscovery()
	result.Candidates = nil
	result.Providers = []discovery.ProviderReport{{
		ID:        "pkg.go.dev",
		Succeeded: false,
		Issues: []discovery.ProviderIssue{{
			Kind:     discovery.IssueUnavailable,
			Provider: "pkg.go.dev",
			Message:  "provider unavailable",
		}},
	}}
	discoverer := &fakeDiscoverer{
		result: result,
		err:    &discovery.ProvidersFailedError{Providers: []string{"pkg.go.dev"}},
	}
	h := discoverHandler(t, discoverer, true)

	rec := call(t, h, http.MethodPost, "/v1/discover", discoverBody())

	requireStatus(t, rec, http.StatusBadGateway)
	requireCode(t, rec, CodeAllProvidersFailed)
	if !strings.Contains(rec.Body.String(), "provider unavailable") {
		t.Errorf("safe provider issue not preserved in the error detail: %s", rec.Body.String())
	}
	assertNoSecrets(t, rec)
}

func TestDiscoverInvalidProfileIs422(t *testing.T) {
	cases := map[string]string{
		"unknown provider": discoverBody(map[string]any{
			"id":      "not-a-real-provider",
			"queries": []map[string]any{{"text": "x", "limit": 1}},
		}),
		"limit out of range": discoverBody(map[string]any{
			"id":      "pkg.go.dev",
			"queries": []map[string]any{{"text": "x", "limit": 99}},
		}),
		"wrong schema version": `{"schema_version":2,"primitive_id":"p","contract_id":"c","providers":[{"id":"pkg.go.dev","queries":[{"text":"x","limit":1}]}]}`,
		"duplicate provider": `{"schema_version":1,"primitive_id":"p","contract_id":"c","providers":[` +
			`{"id":"pkg.go.dev","queries":[{"text":"x","limit":1}]},` +
			`{"id":"pkg.go.dev","queries":[{"text":"y","limit":1}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			discoverer := &fakeDiscoverer{result: sampleDiscovery()}
			h := discoverHandler(t, discoverer, true)

			rec := call(t, h, http.MethodPost, "/v1/discover", body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			requireContentType(t, rec, "application/problem+json")
			if discoverer.calls != 0 {
				t.Errorf("discoverer ran %d time(s) for an invalid profile, want 0", discoverer.calls)
			}
		})
	}
}

func TestDiscoverDisabledIs503(t *testing.T) {
	discoverer := &fakeDiscoverer{result: sampleDiscovery()}
	h := discoverHandler(t, discoverer, false)

	rec := call(t, h, http.MethodPost, "/v1/discover", discoverBody())

	requireStatus(t, rec, http.StatusServiceUnavailable)
	requireCode(t, rec, CodeExternalOperations)
	if discoverer.calls != 0 {
		t.Errorf("discoverer ran %d time(s) while disabled, want 0", discoverer.calls)
	}
}

func TestDiscoverOversizedBodyIs413(t *testing.T) {
	discoverer := &fakeDiscoverer{result: sampleDiscovery()}
	h := discoverHandler(t, discoverer, true)

	body := `{"schema_version":1,"primitive_id":"p","contract_id":"c","providers":[{"id":"pkg.go.dev","queries":[{"text":"` +
		strings.Repeat("a", MaxBodyDiscover) + `","limit":1}]}]}`
	rec := call(t, h, http.MethodPost, "/v1/discover", body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if discoverer.calls != 0 {
		t.Errorf("discoverer ran %d time(s) on an oversized body, want 0", discoverer.calls)
	}
}

// TestDiscoverNeverAcceptsFilesystemInput locks the "no remote filesystem"
// rule: the profile carries no root, path or profile_file field at all.
func TestDiscoverNeverAcceptsFilesystemInput(t *testing.T) {
	discoverer := &fakeDiscoverer{result: sampleDiscovery()}
	h := discoverHandler(t, discoverer, true)

	body := `{"schema_version":1,"primitive_id":"p","contract_id":"c","root":"/etc","profile_file":"x.yaml",` +
		`"providers":[{"id":"pkg.go.dev","queries":[{"text":"x","limit":1}]}]}`
	rec := call(t, h, http.MethodPost, "/v1/discover", body)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for filesystem fields; body=%s", rec.Code, rec.Body.String())
	}
	if discoverer.calls != 0 {
		t.Errorf("discoverer ran %d time(s), want 0", discoverer.calls)
	}
}
