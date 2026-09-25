package pkggodev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var observedAt = time.Date(2026, time.September, 25, 10, 0, 0, 0, time.UTC)

const (
	oneItemSearch = `{"items":[{"packagePath":"github.com/example/pkg","modulePath":"github.com/example","version":"v1.0.0","synopsis":"Package pkg runs bounded subprocesses."}],"total":1}`
	basicPackage  = `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","name":"pkg","synopsis":"Package pkg runs bounded subprocesses.","isStandardLibrary":false,"isRedistributable":true}`
)

func newProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewWithBaseURL(server.URL)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func newRequest(queries []discovery.Query, budget discovery.Budget) discovery.ProviderRequest {
	return discovery.ProviderRequest{
		Primitive: model.Primitive{
			ID:         "process/bounded-subprocess",
			ContractID: "process/bounded-subprocess/v1",
		},
		Contract: model.Contract{
			ID:          "process/bounded-subprocess/v1",
			PrimitiveID: "process/bounded-subprocess",
		},
		Queries:    queries,
		Budget:     budget,
		ObservedAt: observedAt,
	}
}

func singleQuery() []discovery.Query {
	return []discovery.Query{{Text: "subprocess cancellation", Limit: 4}}
}

// defaultHandler answers search with one item and metadata with the basics.
func defaultHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/search"):
			if r.URL.Query().Get("q") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, oneItemSearch)
		case strings.HasPrefix(r.URL.Path, "/v1/package/"):
			writeJSON(w, http.StatusOK, basicPackage)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestSearchResultBecomesADependencyCandidate(t *testing.T) {
	provider := newProvider(t, defaultHandler())

	result, err := provider.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(result.Candidates))
	}

	candidate := result.Candidates[0]
	wantID := "public/pkg.go.dev/github.com%2Fexample%2Fpkg@v1.0.0"
	if candidate.Specimen.ID != wantID {
		t.Errorf("specimen id = %q, want %q", candidate.Specimen.ID, wantID)
	}
	if candidate.ProviderID != discovery.ProviderPkgGoDev || candidate.Specimen.PrimitiveID != "process/bounded-subprocess" {
		t.Errorf("candidate = %#v", candidate)
	}
	if len(candidate.Specimen.ReuseMode) != 1 || candidate.Specimen.ReuseMode[0] != model.ReuseDependency {
		t.Errorf("reuse modes = %v, want [dependency]", candidate.Specimen.ReuseMode)
	}
	if candidate.Specimen.Source.URL != "https://pkg.go.dev/github.com/example/pkg" {
		t.Errorf("source url = %q", candidate.Specimen.Source.URL)
	}
	// Exact search version, never whatever the package endpoint calls latest.
	if candidate.Specimen.Source.Revision != "v1.0.0" {
		t.Errorf("revision = %q, want the exact search version", candidate.Specimen.Source.Revision)
	}
	if candidate.Specimen.Source.Path != "github.com/example/pkg" {
		t.Errorf("source path = %q", candidate.Specimen.Source.Path)
	}
	assertHasEvidence(t, candidate, "package_synopsis", model.EvidenceInfo, "bounded subprocesses")
	assertHasEvidence(t, candidate, "package_module", model.EvidenceInfo, `module "github.com/example"`)
	assertHasEvidence(t, candidate, "package_version", model.EvidenceInfo, `version "v1.0.0"`)
	if result.Requests != 2 {
		t.Errorf("requests = %d, want search plus package metadata", result.Requests)
	}
}

func TestPackageMetadataIsAppliedWithoutSwitchingVersion(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			writeJSON(w, http.StatusOK, `{"items":[{"packagePath":"github.com/example/pkg","modulePath":"github.com/example","version":"v1.0.2","synopsis":"Older synopsis."}],"total":1}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"modulePath":"github.com/example","version":"v9.9.9","path":"github.com/example/pkg","name":"pkg","synopsis":"Newest synopsis.","isStandardLibrary":false,"isRedistributable":false}`)
	})

	result, err := provider.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	candidate := result.Candidates[0]
	if candidate.Specimen.Source.Revision != "v1.0.2" {
		t.Errorf("revision = %q, want the search version v1.0.2 rather than metadata v9.9.9", candidate.Specimen.Source.Revision)
	}
	if candidate.Specimen.ID != "public/pkg.go.dev/github.com%2Fexample%2Fpkg@v1.0.2" {
		t.Errorf("id = %q, want identity pinned to the searched version", candidate.Specimen.ID)
	}
	assertHasEvidence(t, candidate, "package_standard_library", model.EvidenceInfo, "isStandardLibrary=false")
	assertHasEvidence(t, candidate, "package_redistributable", model.EvidenceInfo, "isRedistributable=false")
	assertHasEvidence(t, candidate, "package_version", model.EvidenceInfo, `version "v1.0.2"`)
}

func TestStandardLibraryMetadataIsPreserved(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			writeJSON(w, http.StatusOK, `{"items":[{"packagePath":"os/exec","modulePath":"std","version":"v1.27.1","synopsis":"Package exec runs external commands."}],"total":1}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"modulePath":"std","version":"v1.27.1","path":"os/exec","name":"exec","synopsis":"Package exec runs external commands.","isStandardLibrary":true,"isRedistributable":true}`)
	})

	result, err := provider.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	assertHasEvidence(t, result.Candidates[0], "package_standard_library", model.EvidenceInfo, "isStandardLibrary=true")
	assertHasEvidence(t, result.Candidates[0], "package_module", model.EvidenceInfo, `module "std"`)
}

func TestLicenceMapping(t *testing.T) {
	tests := []struct {
		name        string
		metadata    string
		wantLicense string
		wantResult  model.EvidenceResult
	}{
		{
			name:        "single clear licence",
			metadata:    `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","name":"pkg","synopsis":"s","isStandardLibrary":false,"isRedistributable":true,"license":"MIT"}`,
			wantLicense: "MIT",
			wantResult:  model.EvidenceInfo,
		},
		{
			name:        "missing licence",
			metadata:    `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","name":"pkg","synopsis":"s","isStandardLibrary":false,"isRedistributable":true}`,
			wantLicense: "",
			wantResult:  model.EvidenceUnknown,
		},
		{
			name:        "null licence",
			metadata:    `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","license":null}`,
			wantLicense: "",
			wantResult:  model.EvidenceUnknown,
		},
		{
			name:        "compound licence expression",
			metadata:    `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","license":"MIT OR Apache-2.0"}`,
			wantLicense: "",
			wantResult:  model.EvidenceUnknown,
		},
		{
			name:        "licence as a list",
			metadata:    `{"modulePath":"github.com/example","version":"v1.0.0","path":"github.com/example/pkg","license":["MIT","Apache-2.0"]}`,
			wantLicense: "",
			wantResult:  model.EvidenceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/search") {
					writeJSON(w, http.StatusOK, oneItemSearch)
					return
				}
				writeJSON(w, http.StatusOK, tt.metadata)
			})

			result, err := provider.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			candidate := result.Candidates[0]
			if candidate.Specimen.Source.License != tt.wantLicense {
				t.Errorf("SourceRef.License = %q, want %q (Reusery never invents an SPDX expression)",
					candidate.Specimen.Source.License, tt.wantLicense)
			}
			assertHasEvidence(t, candidate, "source_license", tt.wantResult, "licence")
		})
	}
}

func TestResultLimitIsEnforcedEvenIfTheServerOverserves(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/search") {
			writeJSON(w, http.StatusOK, basicPackage)
			return
		}
		items := make([]string, 0, 10)
		for i := 0; i < 10; i++ {
			items = append(items, `{"packagePath":"github.com/example/pkg`+string(rune('a'+i))+`","modulePath":"github.com/example","version":"v1.0.0","synopsis":"s"}`)
		}
		writeJSON(w, http.StatusOK, `{"items":[`+strings.Join(items, ",")+`],"total":10}`)
	})

	budget := discovery.DefaultBudget()
	budget.MaxResults = 3
	result, err := provider.Discover(t.Context(), newRequest([]discovery.Query{{Text: "q", Limit: 3}}, budget))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 3 {
		t.Errorf("candidates = %d, want the per-query result ceiling of %d", len(result.Candidates), budget.MaxResults)
	}
}

func TestRequestBudgetIsEnforced(t *testing.T) {
	var requests int
	provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			writeJSON(w, http.StatusOK, oneItemSearch)
			return
		}
		writeJSON(w, http.StatusOK, basicPackage)
	})

	budget := discovery.DefaultBudget()
	budget.MaxQueries = 3
	budget.MaxHTTPRequests = 2
	queries := []discovery.Query{
		{Text: "one", Limit: 1},
		{Text: "two", Limit: 1},
		{Text: "three", Limit: 1},
	}

	result, err := provider.Discover(t.Context(), newRequest(queries, budget))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if requests != budget.MaxHTTPRequests {
		t.Errorf("HTTP requests = %d, want the provider to stop at %d", requests, budget.MaxHTTPRequests)
	}
	if !result.Incomplete {
		t.Error("result must be marked incomplete once the request budget is spent")
	}
	if !hasIssueKind(result.Issues, discovery.IssueBudgetExhausted) {
		t.Errorf("issues = %#v, want budget_exhausted", result.Issues)
	}
	if len(result.Candidates) == 0 {
		t.Error("searches that already succeeded must still yield candidates")
	}
}

func TestDuplicateSearchResultsAreDeduplicated(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			writeJSON(w, http.StatusOK, oneItemSearch)
			return
		}
		writeJSON(w, http.StatusOK, basicPackage)
	})

	queries := []discovery.Query{{Text: "subprocess cancellation", Limit: 4}, {Text: "bounded output pipe", Limit: 4}}
	result, err := provider.Discover(t.Context(), newRequest(queries, discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want one deduplicated candidate", len(result.Candidates))
	}

	matches := 0
	for _, evidence := range result.Candidates[0].Evidence {
		if evidence.Kind == "discovery_match" {
			matches++
			if !strings.HasPrefix(evidence.Artifact, "query=") {
				t.Errorf("discovery_match artifact = %q, want to record the query that found it", evidence.Artifact)
			}
		}
	}
	if matches != 2 {
		t.Errorf("discovery_match observations = %d, want one per matching query", matches)
	}
}

func TestProviderFailuresAreClassified(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantKind discovery.IssueKind
	}{
		{
			name: "rate limited",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "30")
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantKind: discovery.IssueRateLimited,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			wantKind: discovery.IssueUnavailable,
		},
		{
			name: "malformed json",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items": not json`))
			},
			wantKind: discovery.IssueInvalidResponse,
		},
		{
			name: "oversized body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":["` + strings.Repeat("a", 4096) + `"]}`))
			},
			wantKind: discovery.IssueInvalidResponse,
		},
		{
			name: "wrong content type",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html>login</html>`))
			},
			wantKind: discovery.IssueInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newProvider(t, tt.handler)
			budget := discovery.DefaultBudget()
			budget.MaxResponseBytes = 1024

			result, err := provider.Discover(t.Context(), newRequest(singleQuery(), budget))
			if err == nil {
				t.Fatal("Discover succeeded, want an operational failure")
			}
			if !hasIssueKind(result.Issues, tt.wantKind) {
				t.Errorf("issues = %#v, want kind %q", result.Issues, tt.wantKind)
			}
			if len(result.Candidates) != 0 {
				t.Errorf("candidates = %d, want none when every query failed", len(result.Candidates))
			}
		})
	}
}

func TestContextTimeoutStopsWork(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		writeJSON(w, http.StatusOK, oneItemSearch)
	})

	budget := discovery.DefaultBudget()
	budget.Timeout = 30 * time.Millisecond

	start := time.Now()
	result, err := provider.Discover(t.Context(), newRequest(singleQuery(), budget))
	if err == nil {
		t.Fatal("Discover succeeded, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("provider ran for %v, want it to respect the wall-clock budget", elapsed)
	}
	if !hasIssueKind(result.Issues, discovery.IssueTimeout) {
		t.Errorf("issues = %#v, want a timeout issue", result.Issues)
	}
}

func TestCancelledContextIsHonoured(t *testing.T) {
	provider := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		writeJSON(w, http.StatusOK, oneItemSearch)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.Discover(ctx, newRequest(singleQuery(), discovery.DefaultBudget())); err == nil {
		t.Fatal("Discover succeeded, want a cancellation error")
	}
}

func TestProviderNeverProducesBehaviouralEvidence(t *testing.T) {
	provider := newProvider(t, defaultHandler())

	result, err := provider.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	assertNoBehaviouralEvidence(t, discovery.ProviderPkgGoDev, result.Candidates)
	if result.Incomplete {
		t.Error("a clean run must not be marked incomplete")
	}
}

func assertHasEvidence(t *testing.T, candidate discovery.Candidate, kind string, result model.EvidenceResult, claimFragment string) {
	t.Helper()
	for _, evidence := range candidate.Evidence {
		if evidence.Kind != kind {
			continue
		}
		if evidence.Result != result {
			t.Errorf("%s result = %q, want %q", kind, evidence.Result, result)
		}
		if !strings.Contains(evidence.Claim, claimFragment) {
			t.Errorf("%s claim = %q, want it to mention %q", kind, evidence.Claim, claimFragment)
		}
		return
	}
	t.Errorf("candidate has no %q evidence; evidence = %#v", kind, candidate.Evidence)
}

func assertNoBehaviouralEvidence(t *testing.T, providerID string, candidates []discovery.Candidate) {
	t.Helper()
	for _, candidate := range candidates {
		if err := discovery.ValidateCandidate(candidate, "process/bounded-subprocess"); err != nil {
			t.Errorf("candidate %s: %v", candidate.Specimen.ID, err)
		}
		for _, evidence := range candidate.Evidence {
			if evidence.Result == model.EvidencePass || evidence.Result == model.EvidenceFail {
				t.Errorf("provider %s produced behavioural result %q for %s", providerID, evidence.Result, evidence.Kind)
			}
			if evidence.AppliesTo != "" {
				t.Errorf("provider %s aimed evidence at requirement %q", providerID, evidence.AppliesTo)
			}
			if !strings.HasPrefix(evidence.ID, "discovery/"+providerID+"/") {
				t.Errorf("evidence id %q does not name provider %q", evidence.ID, providerID)
			}
		}
	}
}

func hasIssueKind(issues []discovery.ProviderIssue, kind discovery.IssueKind) bool {
	for _, issue := range issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}
