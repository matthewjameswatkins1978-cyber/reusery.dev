package depsdev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// factValue decodes the value field of a versioned fact artifact.
func factValue(raw string) (json.RawMessage, error) {
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Value         json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, err
	}
	if envelope.SchemaVersion != 1 {
		return nil, errors.New("unexpected fact artifact schema version")
	}
	return envelope.Value, nil
}

func jsonUnmarshalString(raw string, into *string) error {
	value, err := factValue(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(value, into)
}

func jsonUnmarshalBool(raw string, into *bool) error {
	value, err := factValue(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(value, into)
}

func jsonUnmarshalInt(raw string, into *int) error {
	value, err := factValue(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(value, into)
}

const (
	testModule  = "github.com/pkg/errors"
	testVersion = "v0.9.1"
)

type requestLog struct {
	uris []string
}

type endpoint struct {
	status  int
	body    string
	handler http.HandlerFunc
}

func newFixture(t *testing.T, version, requirements endpoint) (*Provider, *requestLog) {
	t.Helper()
	log := &requestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.uris = append(log.uris, r.RequestURI)
		target := version
		if strings.HasSuffix(r.URL.Path, requirementsSuffix) {
			target = requirements
		}
		if target.handler != nil {
			target.handler(w, r)
			return
		}
		if target.status == 0 {
			target.status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(target.status)
		_, _ = w.Write([]byte(target.body))
	}))
	t.Cleanup(server.Close)
	return NewWithBaseURL(server.URL), log
}

func defaultVersionBody() string {
	return `{
		"versionKey": {"system":"GO","name":"` + testModule + `","version":"` + testVersion + `"},
		"publishedAt": "2020-01-14T19:47:44Z",
		"isDeprecated": false,
		"deprecatedReason": "",
		"licenses": ["BSD-2-Clause"],
		"advisoryKeys": [],
		"links": [{"label":"SOURCE_REPO","url":"https://github.com/pkg/errors"}]
	}`
}

func defaultRequirementsBody() string {
	return `{"go":{"directDependencies":[{"name":"golang.org/x/tools","requirement":"v0.1.0"}],
		"indirectDependencies":[{"name":"golang.org/x/mod","requirement":"v0.4.0"}]}}`
}

func pkgSpecimen() model.Specimen {
	return model.Specimen{
		ID:          "public/pkg.go.dev/github.com%2Fpkg%2Ferrors@v0.9.1",
		PrimitiveID: "process/bounded-subprocess",
		Name:        testModule,
		Source: model.SourceRef{
			URL:      "https://pkg.go.dev/github.com/pkg/errors",
			Revision: testVersion,
			Path:     testModule,
		},
		ReuseMode: []model.ReuseMode{model.ReuseDependency},
	}
}

func moduleEvidence() []model.Evidence {
	specimen := pkgSpecimen()
	return []model.Evidence{{
		ID:         "discovery/module",
		SubjectID:  specimen.ID,
		Kind:       "package_module",
		Claim:      `package "` + testModule + `" belongs to module "` + testModule + `"`,
		Result:     model.EvidenceInfo,
		Source:     specimen.Source,
		ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}}
}

func testRequest(specimen model.Specimen, evidence []model.Evidence) enrichment.Request {
	return enrichment.Request{
		Specimen:         specimen,
		ExistingEvidence: evidence,
		ObservedAt:       time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC),
		Budget:           enrichment.DefaultBudget(),
	}
}

func observationsByKind(result enrichment.ProviderResult) map[string][]model.Evidence {
	byKind := map[string][]model.Evidence{}
	for _, observation := range result.Evidence {
		byKind[observation.Kind] = append(byKind[observation.Kind], observation)
	}
	return byKind
}

func assertTrustRules(t *testing.T, provider *Provider, specimen model.Specimen, result enrichment.ProviderResult) {
	t.Helper()
	for _, observation := range result.Evidence {
		if observation.Result != model.EvidenceInfo && observation.Result != model.EvidenceUnknown {
			t.Errorf("observation %q has result %q; enrichment may never emit pass/fail", observation.Kind, observation.Result)
		}
		if observation.AppliesTo != "" {
			t.Errorf("observation %q applies to %q; enrichment evidence never maps to a requirement", observation.Kind, observation.AppliesTo)
		}
		if observation.SubjectID != specimen.ID {
			t.Errorf("observation %q has subject %q", observation.Kind, observation.SubjectID)
		}
		if err := enrichment.ValidateObservation(provider.ID(), specimen.ID, observation); err != nil {
			t.Errorf("observation %q is invalid: %v", observation.Kind, err)
		}
	}
}

func TestSupportsOnlyPkgGoDevSpecimens(t *testing.T) {
	provider := New()
	if !provider.Supports(pkgSpecimen()) {
		t.Error("pkg.go.dev specimens must be supported")
	}
	repository := model.Specimen{ID: "public/github/repository/golang/go"}
	if provider.Supports(repository) {
		t.Error("repository specimens must not be supported by deps.dev")
	}
	if provider.ID() != enrichment.ProviderDepsDev {
		t.Errorf("id = %q", provider.ID())
	}
	if New().baseURL != DefaultBaseURL {
		t.Errorf("base url = %q, want the code-owned production endpoint", New().baseURL)
	}
}

func TestRequestMappingAndModulePathEscaping(t *testing.T) {
	provider, log := newFixture(t,
		endpoint{body: defaultVersionBody()},
		endpoint{body: defaultRequirementsBody()})

	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if result.Requests != 2 {
		t.Errorf("requests = %d, want 2", result.Requests)
	}
	if len(log.uris) != 2 {
		t.Fatalf("uris = %v", log.uris)
	}
	wantPrefix := "/v3/systems/GO/packages/github.com%2Fpkg%2Ferrors/versions/" + testVersion
	if !strings.HasPrefix(log.uris[0], wantPrefix) {
		t.Errorf("version uri = %q, want prefix %q", log.uris[0], wantPrefix)
	}
	if !strings.HasSuffix(log.uris[1], ":requirements") {
		t.Errorf("requirements uri = %q", log.uris[1])
	}
	assertTrustRules(t, provider, pkgSpecimen(), result)
}

func TestExactVersionPreservation(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	versions := observationsByKind(result)[enrichment.KindPackageVersion]
	if len(versions) != 1 {
		t.Fatalf("package_version observations = %d", len(versions))
	}
	var value string
	if err := jsonUnmarshalString(versions[0].Artifact, &value); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if value != testVersion {
		t.Errorf("version = %q, want %q", value, testVersion)
	}
}

func TestSingleLicence(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	licences := observationsByKind(result)[enrichment.KindSourceLicense]
	if len(licences) != 1 || licences[0].Result != model.EvidenceInfo {
		t.Fatalf("licence observations = %+v", licences)
	}
	var value string
	if err := jsonUnmarshalString(licences[0].Artifact, &value); err != nil || value != "BSD-2-Clause" {
		t.Errorf("licence value = %q (%v)", value, err)
	}
	if _, present := observationsByKind(result)[enrichment.KindLicenceRelationship]; present {
		t.Error("a single licence must not emit a relationship observation")
	}
}

func TestZeroLicencesLeavesTheSourceLicenceUnresolved(t *testing.T) {
	body := `{"versionKey":{"system":"GO","name":"` + testModule + `","version":"` + testVersion + `"},"licenses":[]}`
	provider, _ := newFixture(t, endpoint{body: body}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	licences := observationsByKind(result)[enrichment.KindSourceLicense]
	if len(licences) != 1 || licences[0].Result != model.EvidenceUnknown {
		t.Fatalf("licence observations = %+v, want one UNKNOWN", licences)
	}
	if licences[0].Artifact != "" {
		t.Errorf("artifact = %q, want empty for an unknown observation", licences[0].Artifact)
	}
}

func TestMultipleLicencesAreEachRecordedExactlyPlusARelationshipUnknown(t *testing.T) {
	body := `{"versionKey":{"system":"GO","name":"` + testModule + `","version":"` + testVersion + `"},
		"licenses":["MIT","Apache-2.0"]}`
	provider, _ := newFixture(t, endpoint{body: body}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	byKind := observationsByKind(result)
	licences := byKind[enrichment.KindSourceLicense]
	if len(licences) != 2 {
		t.Fatalf("licence observations = %+v, want exactly the returned expressions", licences)
	}
	values := map[string]bool{}
	for _, observation := range licences {
		var value string
		if err := jsonUnmarshalString(observation.Artifact, &value); err != nil {
			t.Fatalf("artifact: %v", err)
		}
		values[value] = true
	}
	if !values["MIT"] || !values["Apache-2.0"] {
		t.Errorf("values = %v", values)
	}

	relationships := byKind[enrichment.KindLicenceRelationship]
	if len(relationships) != 1 || relationships[0].Result != model.EvidenceUnknown {
		t.Fatalf("relationship observations = %+v", relationships)
	}
	if relationships[0].Artifact != "" {
		t.Errorf("relationship artifact = %q, want empty", relationships[0].Artifact)
	}
}

func TestPublishedAtIsRecordedWhenReturned(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	published := observationsByKind(result)[enrichment.KindPackagePublishedAt]
	if len(published) != 1 {
		t.Fatalf("published_at observations = %d", len(published))
	}
	var value string
	if err := jsonUnmarshalString(published[0].Artifact, &value); err != nil || value != "2020-01-14T19:47:44Z" {
		t.Errorf("published_at = %q (%v)", value, err)
	}
}

func TestDeprecatedFalseAndTrue(t *testing.T) {
	cases := map[string]struct {
		body           string
		wantDeprecated bool
		wantReason     bool
	}{
		"false": {body: defaultVersionBody()},
		"true with reason": {
			body: `{"versionKey":{"system":"GO","name":"` + testModule + `","version":"` + testVersion + `"},
				"isDeprecated": true, "deprecatedReason": "superseded by v2"}`,
			wantDeprecated: true,
			wantReason:     true,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			provider, _ := newFixture(t, endpoint{body: testCase.body}, endpoint{body: defaultRequirementsBody()})
			result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
			if err != nil {
				t.Fatalf("Enrich: %v", err)
			}
			byKind := observationsByKind(result)
			deprecated := byKind[enrichment.KindPackageDeprecated]
			if len(deprecated) != 1 {
				t.Fatalf("deprecated observations = %d", len(deprecated))
			}
			var value bool
			if err := jsonUnmarshalBool(deprecated[0].Artifact, &value); err != nil {
				t.Fatalf("artifact: %v", err)
			}
			if value != testCase.wantDeprecated {
				t.Errorf("deprecated = %t, want %t", value, testCase.wantDeprecated)
			}
			reasons := byKind[enrichment.KindPackageDeprecatedReason]
			if testCase.wantReason && len(reasons) != 1 {
				t.Errorf("reason observations = %d, want 1", len(reasons))
			}
			if !testCase.wantReason && len(reasons) != 0 {
				t.Errorf("reason observations = %d, want none when nothing was returned", len(reasons))
			}
		})
	}
}

func TestZeroAdvisoriesStillEmitsACountObservation(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	byKind := observationsByKind(result)
	if ids := byKind[enrichment.KindKnownAdvisory]; len(ids) != 0 {
		t.Errorf("advisory observations = %+v, want none", ids)
	}
	counts := byKind[enrichment.KindKnownAdvisoryCount]
	if len(counts) != 1 {
		t.Fatalf("count observations = %d, want 1", len(counts))
	}
	claim := strings.ToLower(counts[0].Claim)
	for _, banned := range []string{"secure", "safe", "vulnerability", "passed security"} {
		if strings.Contains(claim, banned) {
			t.Errorf("claim %q must never say %q", counts[0].Claim, banned)
		}
	}
	if !strings.Contains(claim, "zero known direct advisory identifiers") {
		t.Errorf("claim = %q", counts[0].Claim)
	}
	if counts[0].Result != model.EvidenceInfo {
		t.Errorf("result = %q, want info: the count is a fact", counts[0].Result)
	}
}

func TestMultipleAdvisoryIdentifiers(t *testing.T) {
	body := `{"versionKey":{"system":"GO","name":"` + testModule + `","version":"` + testVersion + `"},
		"advisoryKeys":[{"id":"GHSA-w73w-5m7g-f7qc"},{"id":"GO-2020-0017"}]}`
	provider, _ := newFixture(t, endpoint{body: body}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	byKind := observationsByKind(result)
	ids := byKind[enrichment.KindKnownAdvisory]
	if len(ids) != 2 {
		t.Fatalf("advisory observations = %d, want 2", len(ids))
	}
	values := map[string]bool{}
	for _, observation := range ids {
		var value string
		if err := jsonUnmarshalString(observation.Artifact, &value); err != nil {
			t.Fatalf("artifact: %v", err)
		}
		values[value] = true
	}
	if !values["GHSA-w73w-5m7g-f7qc"] || !values["GO-2020-0017"] {
		t.Errorf("values = %v", values)
	}

	counts := byKind[enrichment.KindKnownAdvisoryCount]
	var count int
	if err := jsonUnmarshalInt(counts[0].Artifact, &count); err != nil || count != 2 {
		t.Errorf("count = %d (%v)", count, err)
	}
}

func TestDirectAndIndirectDependenciesWithCounts(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	byKind := observationsByKind(result)

	direct := byKind[enrichment.KindDirectDependency]
	if len(direct) != 1 {
		t.Fatalf("direct dependency observations = %d", len(direct))
	}
	var directValue string
	if err := jsonUnmarshalString(direct[0].Artifact, &directValue); err != nil || directValue != "golang.org/x/tools@v0.1.0" {
		t.Errorf("direct value = %q (%v)", directValue, err)
	}

	indirect := byKind[enrichment.KindIndirectDependency]
	if len(indirect) != 1 {
		t.Fatalf("indirect dependency observations = %d", len(indirect))
	}

	var directCount, indirectCount int
	if err := jsonUnmarshalInt(byKind[enrichment.KindDirectDependencyCount][0].Artifact, &directCount); err != nil {
		t.Fatalf("direct count: %v", err)
	}
	if err := jsonUnmarshalInt(byKind[enrichment.KindIndirectDependencyCount][0].Artifact, &indirectCount); err != nil {
		t.Fatalf("indirect count: %v", err)
	}
	if directCount != 1 || indirectCount != 1 {
		t.Errorf("counts = %d / %d, want 1 / 1", directCount, indirectCount)
	}
}

func TestRequirementsFailureKeepsVersionEvidenceAndReportsAnIssue(t *testing.T) {
	provider, _ := newFixture(t,
		endpoint{body: defaultVersionBody()},
		endpoint{status: http.StatusTooManyRequests, body: `{}`})

	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(result.Evidence) == 0 {
		t.Fatal("version evidence must survive a requirements failure")
	}
	if len(result.Issues) != 1 {
		t.Fatalf("issues = %+v, want one", result.Issues)
	}
	if result.Issues[0].Kind != enrichment.IssueRateLimited {
		t.Errorf("issue kind = %q", result.Issues[0].Kind)
	}
}

func TestGetVersionFailuresReturnAnError(t *testing.T) {
	cases := map[string]int{
		"not found":  http.StatusNotFound,
		"rate limit": http.StatusTooManyRequests,
		"server":     http.StatusInternalServerError,
	}
	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			provider, _ := newFixture(t, endpoint{status: status, body: `{}`}, endpoint{body: defaultRequirementsBody()})
			if _, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence())); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestTimeoutIsReported(t *testing.T) {
	provider, _ := newFixture(t, endpoint{
		handler: func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(150 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(defaultVersionBody()))
		},
	}, endpoint{body: defaultRequirementsBody()})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := testRequest(pkgSpecimen(), moduleEvidence())
	request.Budget.Timeout = 20 * time.Millisecond

	if _, err := provider.Enrich(ctx, request); err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestMalformedJSONIsAnError(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: `{"versionKey": `}, endpoint{body: defaultRequirementsBody()})
	if _, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence())); err == nil {
		t.Fatal("expected an error")
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	request := testRequest(pkgSpecimen(), moduleEvidence())
	request.Budget.MaxResponseBytes = 16

	if _, err := provider.Enrich(context.Background(), request); err == nil {
		t.Fatal("expected an oversized-body error")
	}
}

func TestRequestBudgetIsRespected(t *testing.T) {
	provider, log := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	request := testRequest(pkgSpecimen(), moduleEvidence())
	request.Budget.MaxHTTPRequests = 1

	result, err := provider.Enrich(context.Background(), request)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(log.uris) != 0 {
		t.Errorf("requests = %v, want none", log.uris)
	}
	if len(result.Issues) != 1 || result.Issues[0].Kind != enrichment.IssueBudgetExhausted {
		t.Errorf("issues = %+v", result.Issues)
	}
}

func TestMissingModuleIdentityIsAnExplicitIssueNotAGuess(t *testing.T) {
	provider, log := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), nil))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(log.uris) != 0 {
		t.Errorf("requests = %v, want none without a module identity", log.uris)
	}
	if len(result.Evidence) != 0 {
		t.Errorf("evidence = %+v, want none", result.Evidence)
	}
	if len(result.Issues) != 1 || result.Issues[0].Kind != enrichment.IssueIdentity {
		t.Errorf("issues = %+v", result.Issues)
	}
}

func TestMissingVersionIsAnExplicitIssue(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	specimen := pkgSpecimen()
	specimen.Source.Revision = ""

	result, err := provider.Enrich(context.Background(), testRequest(specimen, moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(result.Evidence) != 0 {
		t.Errorf("evidence = %+v, want none", result.Evidence)
	}
	if len(result.Issues) != 1 || result.Issues[0].Kind != enrichment.IssueIdentity {
		t.Errorf("issues = %+v", result.Issues)
	}
}

func TestModuleClaimFormatMismatchIsSkippedNotGuessed(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	specimen := pkgSpecimen()
	evidence := moduleEvidence()
	evidence[0].Claim = "pkg.go.dev matched some package" // not the code-owned format

	result, err := provider.Enrich(context.Background(), testRequest(specimen, evidence))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(result.Issues) == 0 || result.Issues[0].Kind != enrichment.IssueIdentity {
		t.Errorf("issues = %+v, want an identity issue", result.Issues)
	}
	if len(result.Evidence) != 0 {
		t.Errorf("evidence = %+v, want none", result.Evidence)
	}
}

func TestProviderEmitsNoBehaviouralEvidence(t *testing.T) {
	provider, _ := newFixture(t, endpoint{body: defaultVersionBody()}, endpoint{body: defaultRequirementsBody()})
	result, err := provider.Enrich(context.Background(), testRequest(pkgSpecimen(), moduleEvidence()))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(result.Evidence) == 0 {
		t.Fatal("expected observations")
	}
	assertTrustRules(t, provider, pkgSpecimen(), result)
	if errors.Is(err, enrichment.ErrUnsafeOutput) {
		t.Errorf("err = %v", err)
	}
}
