package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var observedAt = time.Date(2026, time.September, 25, 11, 0, 0, 0, time.UTC)

const repositorySearchBody = `{
  "total_count": 1,
  "incomplete_results": false,
  "items": [{
    "full_name": "example/bounded-proc",
    "html_url": "https://github.com/example/bounded-proc",
    "description": "A bounded subprocess runner for Go",
    "language": "Go",
    "archived": false,
    "pushed_at": "2026-09-01T12:00:00Z",
    "default_branch": "main",
    "license": {"key": "mit", "name": "MIT License", "spdx_id": "MIT"},
    "stargazers_count": 4242
  }]
}`

const codeSearchBody = `{
  "total_count": 1,
  "incomplete_results": false,
  "items": [{
    "name": "cmd.go",
    "path": "dexec/cmd.go",
    "sha": "93a09597cb2b8ff6b4f6c6b79a818f7c140afe60",
    "html_url": "https://github.com/example/bounded-proc/blob/4eb87239/dexec/cmd.go",
    "repository": {"full_name": "example/bounded-proc", "html_url": "https://github.com/example/bounded-proc"}
  }]
}`

type recordedRequest struct {
	path          string
	authorization string
	apiVersion    string
	accept        string
}

type harness struct {
	client   *Client
	repos    *RepositoryProvider
	codes    *CodeProvider
	requests []recordedRequest
}

// newHarness serves the given status/body for every search path and records
// the headers each provider actually sent.
func newHarness(t *testing.T, status int, body string, token string) *harness {
	t.Helper()
	h := &harness{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests = append(h.requests, recordedRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			apiVersion:    r.Header.Get("X-GitHub-Api-Version"),
			accept:        r.Header.Get("Accept"),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	h.client = NewClientWithBaseURL(server.URL, token)
	h.repos = NewRepositoryProvider(h.client)
	h.codes = NewCodeProvider(h.client)
	return h
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
	return []discovery.Query{{Text: "subprocess runner language:go", Limit: 4}}
}

func TestRepositorySearchMapping(t *testing.T) {
	h := newHarness(t, http.StatusOK, repositorySearchBody, "")

	result, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(result.Candidates))
	}

	candidate := result.Candidates[0]
	specimen := candidate.Specimen
	if specimen.ID != "public/github/repository/example/bounded-proc" {
		t.Errorf("specimen id = %q", specimen.ID)
	}
	if specimen.PrimitiveID != "process/bounded-subprocess" {
		t.Errorf("primitive = %q", specimen.PrimitiveID)
	}
	if specimen.Name != "example/bounded-proc" {
		t.Errorf("name = %q", specimen.Name)
	}
	if len(specimen.ReuseMode) != 1 || specimen.ReuseMode[0] != model.ReuseReference {
		t.Errorf("reuse modes = %v, want [reference]", specimen.ReuseMode)
	}
	if specimen.Source.URL != "https://github.com/example/bounded-proc" {
		t.Errorf("source url = %q", specimen.Source.URL)
	}
	if specimen.Source.Path != "" {
		t.Errorf("source path = %q, want empty at repository-discovery stage", specimen.Source.Path)
	}
	if specimen.Source.Revision != "" {
		t.Errorf("source revision = %q, want empty: repository search pins nothing", specimen.Source.Revision)
	}
	if specimen.Source.License != "MIT" {
		t.Errorf("source licence = %q, want the unambiguous SPDX identifier", specimen.Source.License)
	}

	assertHasEvidence(t, candidate, "repository_description", model.EvidenceInfo, "bounded subprocess runner")
	assertHasEvidence(t, candidate, "repository_language", model.EvidenceInfo, `"Go"`)
	assertHasEvidence(t, candidate, "repository_archived", model.EvidenceInfo, "archived=false")
	assertHasEvidence(t, candidate, "repository_last_push", model.EvidenceInfo, "2026-09-01T12:00:00Z")
	assertHasEvidence(t, candidate, "repository_default_branch", model.EvidenceInfo, `"main"`)
	assertHasEvidence(t, candidate, "source_license", model.EvidenceInfo, `"MIT"`)
	assertHasEvidence(t, candidate, "source_revision", model.EvidenceUnknown, "no immutable revision")
	assertHasEvidence(t, candidate, "discovery_match", model.EvidenceInfo, "matched repository")
	assertArtifact(t, candidate, "discovery_match", "query=subprocess runner language:go")

	// Popularity signals are never collected or turned into quality claims.
	for _, evidence := range candidate.Evidence {
		if strings.Contains(evidence.Claim, "4242") || strings.Contains(evidence.Kind, "star") {
			t.Errorf("popularity leaked into evidence: %#v", evidence)
		}
	}
	assertNoBehaviouralEvidence(t, discovery.ProviderGitHubRepositories, result.Candidates)
}

func TestRepositoryLicenceUnknowns(t *testing.T) {
	tests := []struct {
		name        string
		licenseJSON string
		wantLicense string
	}{
		{name: "missing licence", licenseJSON: "", wantLicense: ""},
		{name: "null licence", licenseJSON: `"license": null`, wantLicense: ""},
		{name: "empty spdx identifier", licenseJSON: `"license": {"spdx_id": ""}`, wantLicense: ""},
		{name: "NOASSERTION", licenseJSON: `"license": {"spdx_id": "NOASSERTION"}`, wantLicense: ""},
		{name: "compound expression", licenseJSON: `"license": {"spdx_id": "GPL-3.0-or-later WITH Classpath-exception-2.0"}`, wantLicense: ""},
		{name: "clear identifier", licenseJSON: `"license": {"spdx_id": "Apache-2.0"}`, wantLicense: "Apache-2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := `{"full_name":"example/repo","html_url":"https://github.com/example/repo","archived":false`
			if tt.licenseJSON != "" {
				item += "," + tt.licenseJSON
			}
			item += "}"
			body := `{"total_count":1,"incomplete_results":false,"items":[` + item + `]}`
			h := newHarness(t, http.StatusOK, body, "")

			result, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			candidate := result.Candidates[0]
			if candidate.Specimen.Source.License != tt.wantLicense {
				t.Errorf("SourceRef.License = %q, want %q", candidate.Specimen.Source.License, tt.wantLicense)
			}
			wantResult := model.EvidenceInfo
			if tt.wantLicense == "" {
				wantResult = model.EvidenceUnknown
			}
			assertHasEvidence(t, candidate, "source_license", wantResult, "licence")
		})
	}
}

func TestRepositoryIncompleteResults(t *testing.T) {
	body := strings.Replace(repositorySearchBody, `"incomplete_results": false`, `"incomplete_results": true`, 1)
	h := newHarness(t, http.StatusOK, body, "")

	result, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !result.Incomplete {
		t.Error("GitHub's incomplete_results must be reported as an incomplete provider run")
	}
	if !hasIssueKind(result.Issues, discovery.IssueIncompleteResults) {
		t.Errorf("issues = %#v, want incomplete_results", result.Issues)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("candidates = %d, want the partial results still returned", len(result.Candidates))
	}
}

func TestCodeSearchMapping(t *testing.T) {
	h := newHarness(t, http.StatusOK, codeSearchBody, "")

	result, err := h.codes.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(result.Candidates))
	}

	candidate := result.Candidates[0]
	specimen := candidate.Specimen
	wantID := "public/github/code/example/bounded-proc@93a09597cb2b8ff6b4f6c6b79a818f7c140afe60:dexec%2Fcmd.go"
	if specimen.ID != wantID {
		t.Errorf("specimen id = %q, want %q", specimen.ID, wantID)
	}
	if specimen.Source.Revision != "93a09597cb2b8ff6b4f6c6b79a818f7c140afe60" {
		t.Errorf("revision = %q, want the blob SHA", specimen.Source.Revision)
	}
	if specimen.Source.Path != "dexec/cmd.go" {
		t.Errorf("path = %q", specimen.Source.Path)
	}
	if specimen.Source.URL != "https://github.com/example/bounded-proc/blob/4eb87239/dexec/cmd.go" {
		t.Errorf("url = %q, want the exact GitHub file HTML URL", specimen.Source.URL)
	}
	if specimen.Source.License != "" {
		t.Errorf("licence = %q, want empty: code search establishes no licence", specimen.Source.License)
	}
	if len(specimen.ReuseMode) != 1 || specimen.ReuseMode[0] != model.ReuseReference {
		t.Errorf("reuse modes = %v, want [reference]", specimen.ReuseMode)
	}
	if specimen.Name != "example/bounded-proc:dexec/cmd.go" {
		t.Errorf("name = %q", specimen.Name)
	}

	assertHasEvidence(t, candidate, "discovery_match", model.EvidenceInfo, "matched file")
	assertArtifact(t, candidate, "discovery_match", "query="+singleQuery()[0].Text)
	assertHasEvidence(t, candidate, "source_revision", model.EvidenceInfo, "93a09597cb2b8ff6b4f6c6b79a818f7c140afe60")
	assertHasEvidence(t, candidate, "source_license", model.EvidenceUnknown, "no licence information")
	assertNoBehaviouralEvidence(t, discovery.ProviderGitHubCode, result.Candidates)
}

// TestCodeHitIsNotVerification is the packet's central trust assertion: finding
// cancellation or pipe APIs in source establishes nothing about behaviour.
func TestCodeHitIsNotVerification(t *testing.T) {
	body := `{"total_count":1,"incomplete_results":false,"items":[
	  {"name":"cmd.go","path":"dexec/cmd.go","sha":"aaa111","html_url":"https://github.com/example/repo/blob/aaa111/dexec/cmd.go",
	   "repository":{"full_name":"example/repo"}},
	  {"name":"proc.go","path":"proc.go","sha":"bbb222","html_url":"https://github.com/example/repo/blob/bbb222/proc.go",
	   "repository":{"full_name":"example/repo"}}
	]}`
	h := newHarness(t, http.StatusOK, body, "")
	queries := []discovery.Query{
		{Text: "exec.CommandContext StdoutPipe StderrPipe language:go", Limit: 4},
		{Text: "SysProcAttr Setpgid language:go", Limit: 4},
	}

	result, err := h.codes.Discover(t.Context(), newRequest(queries, discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, candidate := range result.Candidates {
		for _, evidence := range candidate.Evidence {
			if evidence.Result == model.EvidencePass || evidence.Result == model.EvidenceFail {
				t.Errorf("code hit produced behavioural result %q", evidence.Result)
			}
			if evidence.AppliesTo != "" {
				t.Errorf("code hit aimed evidence at requirement %q", evidence.AppliesTo)
			}
			if strings.Contains(evidence.Claim, "supports-cancellation") ||
				strings.Contains(evidence.Claim, "drains-stdout") ||
				strings.Contains(evidence.Claim, "process-tree") {
				t.Errorf("code hit made a behavioural claim: %q", evidence.Claim)
			}
		}
	}
}

func TestCodeHitsFromTwoQueriesAreDeduplicated(t *testing.T) {
	h := newHarness(t, http.StatusOK, codeSearchBody, "")
	queries := []discovery.Query{
		{Text: "exec.CommandContext StdoutPipe StderrPipe language:go", Limit: 4},
		{Text: "SysProcAttr Setpgid language:go", Limit: 4},
	}

	result, err := h.codes.Discover(t.Context(), newRequest(queries, discovery.DefaultBudget()))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want one deduplicated file", len(result.Candidates))
	}
	matches := 0
	for _, evidence := range result.Candidates[0].Evidence {
		if evidence.Kind == "discovery_match" {
			matches++
		}
	}
	if matches != 2 {
		t.Errorf("discovery_match observations = %d, want one per query", matches)
	}
	if result.Requests != 2 {
		t.Errorf("requests = %d, want one per query", result.Requests)
	}
}

func TestProviderFailureClassification(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		headers  map[string]string
		wantKind discovery.IssueKind
	}{
		{
			name:     "rate limited",
			status:   http.StatusTooManyRequests,
			headers:  map[string]string{"Retry-After": "60"},
			wantKind: discovery.IssueRateLimited,
		},
		{
			name:     "403 with exhausted rate budget",
			status:   http.StatusForbidden,
			headers:  map[string]string{"X-RateLimit-Remaining": "0"},
			wantKind: discovery.IssueRateLimited,
		},
		{
			name:     "403 without rate exhaustion",
			status:   http.StatusForbidden,
			wantKind: discovery.IssueForbidden,
		},
		{
			name:     "authentication required",
			status:   http.StatusUnauthorized,
			wantKind: discovery.IssueAuthentication,
		},
		{
			name:     "server error",
			status:   http.StatusServiceUnavailable,
			wantKind: discovery.IssueUnavailable,
		},
		{
			name:     "malformed json",
			status:   http.StatusOK,
			wantKind: discovery.IssueInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"total_count":1,"items": not json`
			if tt.status != http.StatusOK {
				body = ""
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			repos := NewRepositoryProvider(NewClientWithBaseURL(server.URL, ""))
			result, err := repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
			if err == nil {
				t.Fatal("Discover succeeded, want an operational failure")
			}
			if !hasIssueKind(result.Issues, tt.wantKind) {
				t.Errorf("issues = %#v, want kind %q", result.Issues, tt.wantKind)
			}
		})
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	body := `{"total_count":1,"incomplete_results":false,"items":[{` +
		`"full_name":"example/repo","html_url":"https://github.com/example/repo","description":"` +
		strings.Repeat("x", 4096) + `"}]}`
	h := newHarness(t, http.StatusOK, body, "")

	budget := discovery.DefaultBudget()
	budget.MaxResponseBytes = 1024
	result, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), budget))
	if err == nil {
		t.Fatal("Discover succeeded, want a rejected body")
	}
	if !hasIssueKind(result.Issues, discovery.IssueInvalidResponse) {
		t.Errorf("issues = %#v, want invalid_response", result.Issues)
	}
}

func TestQueryBudgetIsEnforced(t *testing.T) {
	h := newHarness(t, http.StatusOK, repositorySearchBody, "")
	budget := discovery.DefaultBudget()
	budget.MaxQueries = 3
	budget.MaxHTTPRequests = 1
	queries := []discovery.Query{{Text: "one", Limit: 1}, {Text: "two", Limit: 1}, {Text: "three", Limit: 1}}

	result, err := h.repos.Discover(t.Context(), newRequest(queries, budget))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(h.requests) != budget.MaxHTTPRequests {
		t.Errorf("requests = %d, want the provider to stop at %d", len(h.requests), budget.MaxHTTPRequests)
	}
	if !result.Incomplete || !hasIssueKind(result.Issues, discovery.IssueBudgetExhausted) {
		t.Errorf("result = %#v, want an incomplete run with a budget_exhausted issue", result)
	}
}

func TestAuthenticationIsOptionalAndNeverLeaked(t *testing.T) {
	const token = "ghp_super_secret_token_value"

	t.Run("token sent when configured", func(t *testing.T) {
		h := newHarness(t, http.StatusOK, repositorySearchBody, token)
		if _, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget())); err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(h.requests) != 1 {
			t.Fatalf("requests = %d, want 1", len(h.requests))
		}
		if got := h.requests[0].authorization; got != "Bearer "+token {
			t.Errorf("Authorization = %q, want the optional bearer token", got)
		}
		if h.requests[0].apiVersion != "2026-03-10" {
			t.Errorf("X-GitHub-Api-Version = %q", h.requests[0].apiVersion)
		}
		if h.requests[0].accept != "application/vnd.github+json" {
			t.Errorf("Accept = %q", h.requests[0].accept)
		}
		if strings.Contains(h.requests[0].path, token) {
			t.Error("token leaked into the request path")
		}
	})

	t.Run("token omitted when unconfigured", func(t *testing.T) {
		h := newHarness(t, http.StatusOK, repositorySearchBody, "")
		if _, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget())); err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if h.requests[0].authorization != "" {
			t.Errorf("Authorization = %q, want no header without a token", h.requests[0].authorization)
		}
		if h.client.HasToken() {
			t.Error("HasToken should report false for an empty token")
		}
	})

	t.Run("token absent from errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom","authorization_token":"` + token + `"}`))
		}))
		t.Cleanup(server.Close)

		repos := NewRepositoryProvider(NewClientWithBaseURL(server.URL, token))
		result, err := repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
		if err == nil {
			t.Fatal("Discover succeeded, want an error")
		}
		assertNoToken(t, err.Error(), token)
		encoded, marshalErr := json.Marshal(result.Issues)
		if marshalErr != nil {
			t.Fatalf("marshal issues: %v", marshalErr)
		}
		assertNoToken(t, string(encoded), token)
	})
}

func assertNoToken(t *testing.T, haystack, token string) {
	t.Helper()
	if strings.Contains(haystack, token) {
		t.Errorf("token leaked into %q", haystack)
	}
}

func TestGitHubProvidersShareOneClient(t *testing.T) {
	h := newHarness(t, http.StatusOK, repositorySearchBody, "")

	if _, err := h.repos.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget())); err != nil {
		t.Fatalf("repository Discover: %v", err)
	}
	if _, err := h.codes.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget())); err != nil {
		t.Fatalf("code Discover: %v", err)
	}
	if len(h.requests) != 2 {
		t.Errorf("shared client recorded %d requests, want 2", len(h.requests))
	}
	if h.repos.ID() == h.codes.ID() {
		t.Error("repository and code discovery must keep separate provider IDs")
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
				t.Errorf("provider %s produced behavioural result %q", providerID, evidence.Result)
			}
			if evidence.AppliesTo != "" {
				t.Errorf("provider %s aimed evidence at requirement %q", providerID, evidence.AppliesTo)
			}
		}
	}
}

// TestCodeProviderOperationalBehaviour covers the operational dimensions the
// repository provider tests already pin down, for the code provider too: it is
// a separate provider ID with its own failure surface.
func TestCodeProviderOperationalBehaviour(t *testing.T) {
	t.Run("incomplete results", func(t *testing.T) {
		body := strings.Replace(codeSearchBody, `"incomplete_results": false`, `"incomplete_results": true`, 1)
		h := newHarness(t, http.StatusOK, body, "")

		result, err := h.codes.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if !result.Incomplete || !hasIssueKind(result.Issues, discovery.IssueIncompleteResults) {
			t.Errorf("result = %#v, want an incomplete_results issue", result)
		}
		if len(result.Candidates) != 1 {
			t.Errorf("candidates = %d, want the partial results still returned", len(result.Candidates))
		}
	})

	for _, tt := range []struct {
		name     string
		status   int
		headers  map[string]string
		wantKind discovery.IssueKind
	}{
		{"authentication required", http.StatusUnauthorized, nil, discovery.IssueAuthentication},
		{"rate limited", http.StatusTooManyRequests, map[string]string{"Retry-After": "60"}, discovery.IssueRateLimited},
		{"forbidden", http.StatusForbidden, nil, discovery.IssueForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)

			codes := NewCodeProvider(NewClientWithBaseURL(server.URL, ""))
			result, err := codes.Discover(t.Context(), newRequest(singleQuery(), discovery.DefaultBudget()))
			if err == nil {
				t.Fatal("Discover succeeded, want an operational failure")
			}
			if !hasIssueKind(result.Issues, tt.wantKind) {
				t.Errorf("issues = %#v, want kind %q", result.Issues, tt.wantKind)
			}
		})
	}

	t.Run("query budget", func(t *testing.T) {
		h := newHarness(t, http.StatusOK, codeSearchBody, "")
		budget := discovery.DefaultBudget()
		budget.MaxQueries = 3
		budget.MaxHTTPRequests = 1
		queries := []discovery.Query{{Text: "one", Limit: 1}, {Text: "two", Limit: 1}, {Text: "three", Limit: 1}}

		result, err := h.codes.Discover(t.Context(), newRequest(queries, budget))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(h.requests) != budget.MaxHTTPRequests {
			t.Errorf("requests = %d, want the provider to stop at %d", len(h.requests), budget.MaxHTTPRequests)
		}
		if !result.Incomplete || !hasIssueKind(result.Issues, discovery.IssueBudgetExhausted) {
			t.Errorf("result = %#v, want an incomplete run with a budget_exhausted issue", result)
		}
	})
}

func hasIssueKind(issues []discovery.ProviderIssue, kind discovery.IssueKind) bool {
	for _, issue := range issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}

func assertArtifact(t *testing.T, candidate discovery.Candidate, kind, want string) {
	t.Helper()
	for _, evidence := range candidate.Evidence {
		if evidence.Kind == kind && evidence.Artifact == want {
			return
		}
	}
	t.Errorf("no %q observation records %q; evidence = %#v", kind, want, candidate.Evidence)
}
