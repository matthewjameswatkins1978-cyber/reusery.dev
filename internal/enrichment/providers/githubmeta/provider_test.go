package githubmeta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

const testToken = "ghp_test_token_never_in_errors"

type servedRequest struct {
	method string
	path   string
	header http.Header
}

func newServer(t *testing.T, status int, body string, mutate func(*http.Request, http.Header)) (*Provider, *[]servedRequest) {
	t.Helper()
	log := make([]servedRequest, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, servedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()})
		if mutate != nil {
			mutate(r, w.Header())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewWithBaseURL(server.URL, testToken), &log
}

func repositoryBody() string {
	return `{
		"full_name": "golang/tools",
		"html_url": "https://github.com/golang/tools",
		"archived": false,
		"pushed_at": "2026-08-20T10:00:00Z",
		"default_branch": "master",
		"license": {"spdx_id": "BSD-3-Clause"},
		"stargazers_count": 42000,
		"forks_count": 6000,
		"watchers_count": 42000,
		"subscribers_count": 4200
	}`
}

func repositorySpecimen() model.Specimen {
	return model.Specimen{
		ID:          "public/github/repository/golang/tools",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "golang/tools",
		Source:      model.SourceRef{URL: "https://github.com/golang/tools"},
		ReuseMode:   []model.ReuseMode{model.ReuseReference},
	}
}

func codeSpecimen() model.Specimen {
	return model.Specimen{
		ID:          "public/github/code/golang/tools@abc123def456:exec/exec.go",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "golang/tools:exec/exec.go",
		Source: model.SourceRef{
			URL:      "https://github.com/golang/tools/blob/abc123def456/exec/exec.go",
			Revision: "abc123def456",
			Path:     "exec/exec.go",
		},
		ReuseMode: []model.ReuseMode{model.ReuseReference},
	}
}

func testRequest(specimen model.Specimen) enrichment.Request {
	return enrichment.Request{
		Specimen:   specimen,
		ObservedAt: time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC),
		Budget:     enrichment.DefaultBudget(),
	}
}

func observationsByKind(result enrichment.ProviderResult) map[string][]model.Evidence {
	byKind := map[string][]model.Evidence{}
	for _, observation := range result.Evidence {
		byKind[observation.Kind] = append(byKind[observation.Kind], observation)
	}
	return byKind
}

func factString(t *testing.T, raw string) string {
	t.Helper()
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Value         json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if envelope.SchemaVersion != 1 {
		t.Fatalf("artifact schema version = %d", envelope.SchemaVersion)
	}
	var value string
	if err := json.Unmarshal(envelope.Value, &value); err != nil {
		t.Fatalf("artifact value: %v", err)
	}
	return value
}

func factBool(t *testing.T, raw string) bool {
	t.Helper()
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Value         json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	var value bool
	if err := json.Unmarshal(envelope.Value, &value); err != nil {
		t.Fatalf("artifact value: %v", err)
	}
	return value
}

func TestSupportsOnlyGitHubRepositoryAndCodeSpecimens(t *testing.T) {
	provider := New("")
	if provider.ID() != enrichment.ProviderGitHubMetadata {
		t.Errorf("id = %q", provider.ID())
	}
	if !provider.Supports(repositorySpecimen()) || !provider.Supports(codeSpecimen()) {
		t.Error("repository and code specimens must be supported")
	}
	if provider.Supports(model.Specimen{ID: "public/pkg.go.dev/example@v1"}) {
		t.Error("package specimens belong to deps.dev")
	}
	if provider.Supports(model.Specimen{ID: "fixture/process/bounded-subprocess/complete-dependency"}) {
		t.Error("development fixtures are not enriched")
	}
}

func TestRepositorySpecimenMapsToItsOwnRepository(t *testing.T) {
	provider, log := newServer(t, http.StatusOK, repositoryBody(), nil)
	result, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(*log) != 1 {
		t.Fatalf("requests = %+v", *log)
	}
	if (*log)[0].method != http.MethodGet || (*log)[0].path != "/repos/golang/tools" {
		t.Errorf("request = %+v", (*log)[0])
	}
	if result.Requests != 1 {
		t.Errorf("requests = %d", result.Requests)
	}
	if len(result.Evidence) != 4 {
		t.Fatalf("evidence = %+v, want archived, pushed_at, default_branch and licence", result.Evidence)
	}
	for _, observation := range result.Evidence {
		if observation.SubjectID != repositorySpecimen().ID {
			t.Errorf("subject = %q", observation.SubjectID)
		}
	}
}

func TestCodeSpecimenMapsToItsParentRepositoryWithoutRewritingIdentity(t *testing.T) {
	provider, log := newServer(t, http.StatusOK, repositoryBody(), nil)
	specimen := codeSpecimen()

	result, err := provider.Enrich(context.Background(), testRequest(specimen))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if (*log)[0].path != "/repos/golang/tools" {
		t.Errorf("path = %q, want the parent repository", (*log)[0].path)
	}
	if specimen.Source.Revision != "abc123def456" {
		t.Errorf("specimen revision was rewritten: %q", specimen.Source.Revision)
	}
	for _, observation := range result.Evidence {
		if observation.SubjectID != specimen.ID {
			t.Errorf("subject = %q", observation.SubjectID)
		}
	}
}

func TestMaintenanceFactsAreRecorded(t *testing.T) {
	provider, _ := newServer(t, http.StatusOK, repositoryBody(), nil)
	result, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	byKind := observationsByKind(result)

	if archived := byKind[enrichment.KindRepositoryArchived]; len(archived) != 1 || factBool(t, archived[0].Artifact) {
		t.Errorf("archived = %+v", archived)
	}
	pushed := byKind[enrichment.KindRepositoryPushedAt]
	if len(pushed) != 1 || factString(t, pushed[0].Artifact) != "2026-08-20T10:00:00Z" {
		t.Errorf("pushed_at = %+v", pushed)
	}
	branch := byKind[enrichment.KindRepositoryDefaultBranch]
	if len(branch) != 1 || factString(t, branch[0].Artifact) != "master" {
		t.Errorf("default_branch = %+v", branch)
	}
}

func TestNoPopularityFieldIsDecodedOrEmitted(t *testing.T) {
	provider, _ := newServer(t, http.StatusOK, repositoryBody(), nil)
	result, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"stargazers_count", "forks_count", "watchers_count", "subscribers_count", "42000", "6000"} {
		if strings.Contains(string(payload), banned) {
			t.Errorf("popularity value %q leaked into output: %s", banned, payload)
		}
	}

	// The decoded struct itself must have no popularity field at all.
	structType := reflect.TypeOf(repositoryResponse{})
	fields := map[string]bool{}
	for index := 0; index < structType.NumField(); index++ {
		fields[structType.Field(index).Name] = true
	}
	for _, banned := range []string{"Stars", "Forks", "Watchers", "Subscribers", "Stargazers"} {
		if fields[banned] {
			t.Errorf("repositoryResponse decodes %q", banned)
		}
	}
}

func TestLicenceVariants(t *testing.T) {
	cases := map[string]struct {
		licenseJSON string
		wantResult  model.EvidenceResult
		wantValue   string
	}{
		"single spdx":     {licenseJSON: `{"spdx_id":"MIT"}`, wantResult: model.EvidenceInfo, wantValue: "MIT"},
		"null licence":    {licenseJSON: `null`, wantResult: model.EvidenceUnknown},
		"missing licence": {licenseJSON: ``, wantResult: model.EvidenceUnknown},
		"noassertion":     {licenseJSON: `{"spdx_id":"NOASSERTION"}`, wantResult: model.EvidenceUnknown},
		"none":            {licenseJSON: `{"spdx_id":"NONE"}`, wantResult: model.EvidenceUnknown},
		"empty spdx":      {licenseJSON: `{"spdx_id":""}`, wantResult: model.EvidenceUnknown},
		"compound":        {licenseJSON: `{"spdx_id":"MIT OR Apache-2.0"}`, wantResult: model.EvidenceUnknown},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			license := testCase.licenseJSON
			if license == "" {
				license = "null"
			}
			body := `{"full_name":"golang/tools","archived":false,"license":` + license + `}`
			provider, _ := newServer(t, http.StatusOK, body, nil)

			result, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
			if err != nil {
				t.Fatalf("Enrich: %v", err)
			}
			licences := observationsByKind(result)[enrichment.KindSourceLicense]
			if len(licences) != 1 {
				t.Fatalf("licence observations = %+v", licences)
			}
			if licences[0].Result != testCase.wantResult {
				t.Errorf("result = %q, want %q", licences[0].Result, testCase.wantResult)
			}
			if testCase.wantResult == model.EvidenceInfo {
				if value := factString(t, licences[0].Artifact); value != testCase.wantValue {
					t.Errorf("value = %q, want %q", value, testCase.wantValue)
				}
			} else if licences[0].Artifact != "" {
				t.Errorf("artifact = %q, want empty for an unknown licence", licences[0].Artifact)
			}
		})
	}
}

func TestAuthenticationAndRateLimitAreClassifiedWithoutLeakingTheToken(t *testing.T) {
	cases := map[string]struct {
		status          int
		responseHeaders map[string]string
		body            string
		wantKind        enrichment.IssueKind
	}{
		"unauthorized": {
			status:   http.StatusUnauthorized,
			body:     `{"message":"Bad credentials for ` + testToken + `"}`,
			wantKind: enrichment.IssueAuthentication,
		},
		"rate limited": {
			status:          http.StatusTooManyRequests,
			responseHeaders: map[string]string{"Retry-After": "30"},
			body:            `{"message":"API rate limit exceeded"}`,
			wantKind:        enrichment.IssueRateLimited,
		},
		"exhausted secondary limit": {
			status:          http.StatusForbidden,
			responseHeaders: map[string]string{"X-RateLimit-Remaining": "0"},
			body:            `{"message":"rate limited"}`,
			wantKind:        enrichment.IssueRateLimited,
		},
		"forbidden": {
			status:   http.StatusForbidden,
			body:     `{"message":"Forbidden"}`,
			wantKind: enrichment.IssueForbidden,
		},
		"server error": {
			status:   http.StatusInternalServerError,
			body:     `{"message":"boom"}`,
			wantKind: enrichment.IssueUnavailable,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			provider, _ := newServer(t, testCase.status, testCase.body, func(_ *http.Request, header http.Header) {
				for key, value := range testCase.responseHeaders {
					header.Set(key, value)
				}
			})
			_, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks the token: %v", err)
			}

			issue := enrichment.IssueFromError(provider.ID(), repositorySpecimen().ID, err)
			if issue.Kind != testCase.wantKind {
				t.Errorf("kind = %q, want %q", issue.Kind, testCase.wantKind)
			}
			if strings.Contains(issue.Message, testToken) {
				t.Errorf("issue leaks the token: %q", issue.Message)
			}
		})
	}
}

func TestTokenIsSentOnlyInAuthorizationHeader(t *testing.T) {
	provider, log := newServer(t, http.StatusOK, repositoryBody(), nil)
	if _, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen())); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	request := (*log)[0]
	if got := request.header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("authorization = %q", got)
	}
	if strings.Contains(request.path, testToken) {
		t.Errorf("token leaked into the path: %q", request.path)
	}
	if request.header.Get("X-GitHub-Api-Version") == "" {
		t.Error("the shared GitHub client must send the pinned API version header")
	}
}

func TestMissingTokenStillWorks(t *testing.T) {
	log := make([]servedRequest, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, servedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(repositoryBody()))
	}))
	t.Cleanup(server.Close)

	provider := NewWithBaseURL(server.URL, "")
	if _, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen())); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(log) != 1 {
		t.Fatalf("requests = %+v", log)
	}
	if got := log[0].header.Get("Authorization"); got != "" {
		t.Errorf("authorization = %q, want empty without a token", got)
	}
}

func TestTimeoutIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(repositoryBody()))
	}))
	t.Cleanup(server.Close)
	provider := NewWithBaseURL(server.URL, testToken)

	request := testRequest(repositorySpecimen())
	request.Budget.Timeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := provider.Enrich(ctx, request); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	provider, _ := newServer(t, http.StatusOK, repositoryBody(), nil)
	request := testRequest(repositorySpecimen())
	request.Budget.MaxResponseBytes = 16
	if _, err := provider.Enrich(context.Background(), request); err == nil {
		t.Fatal("expected an oversized-body error")
	}
}

func TestMalformedJSONIsRejected(t *testing.T) {
	provider, _ := newServer(t, http.StatusOK, `{"archived":`, nil)
	if _, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen())); err == nil {
		t.Fatal("expected an error")
	}
}

func TestUnresolvableIdentityIsAnIssueNotAGuess(t *testing.T) {
	provider, log := newServer(t, http.StatusOK, repositoryBody(), nil)
	for _, id := range []string{
		"public/github/repository/",
		"public/github/code/no-at-sign",
		"public/github/code/@sha:path",
		"public/github/repository/owner/repo/extra",
		"public/github/repository/../escape",
	} {
		specimen := repositorySpecimen()
		specimen.ID = id
		result, err := provider.Enrich(context.Background(), testRequest(specimen))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(result.Evidence) != 0 {
			t.Errorf("%s: evidence = %+v, want none", id, result.Evidence)
		}
		if len(result.Issues) != 1 || result.Issues[0].Kind != enrichment.IssueIdentity {
			t.Errorf("%s: issues = %+v", id, result.Issues)
		}
	}
	if len(*log) != 0 {
		t.Errorf("requests = %+v, want none", *log)
	}
}

func TestRequestBudgetIsRespected(t *testing.T) {
	provider, log := newServer(t, http.StatusOK, repositoryBody(), nil)
	request := testRequest(repositorySpecimen())
	request.Budget.MaxHTTPRequests = 0

	result, err := provider.Enrich(context.Background(), request)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(*log) != 0 {
		t.Errorf("requests = %+v, want none", *log)
	}
	if len(result.Issues) != 1 || result.Issues[0].Kind != enrichment.IssueBudgetExhausted {
		t.Errorf("issues = %+v", result.Issues)
	}
}

func TestEmitsOnlyInfoAndUnknownEvidence(t *testing.T) {
	provider, _ := newServer(t, http.StatusOK, repositoryBody(), nil)
	result, err := provider.Enrich(context.Background(), testRequest(repositorySpecimen()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	for _, observation := range result.Evidence {
		if observation.Result != model.EvidenceInfo && observation.Result != model.EvidenceUnknown {
			t.Errorf("observation %q has result %q", observation.Kind, observation.Result)
		}
		if observation.AppliesTo != "" {
			t.Errorf("observation %q applies to %q", observation.Kind, observation.AppliesTo)
		}
		if err := enrichment.ValidateObservation(provider.ID(), repositorySpecimen().ID, observation); err != nil {
			t.Errorf("observation %q invalid: %v", observation.Kind, err)
		}
	}
}
