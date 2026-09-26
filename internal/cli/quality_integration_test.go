//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/depsdev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/githubmeta"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

const (
	e2ePackageSpecimen = "public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0"
	e2eCodeSpecimen    = "public/github/code/example/tools@abc123def456:exec/exec.go"
)

// hexDigest matches a long hex run, i.e. an evidence id digest. Digests are
// masked before popularity scanning so their content cannot collide with a
// banned substring.
var hexDigest = regexp.MustCompile(`[0-9a-f]{16,}`)

// fakeDepsDev serves a stable deps.dev v3 payload for the fixture package.
func fakeDepsDev(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":requirements") {
			_, _ = w.Write([]byte(`{"go":{"directDependencies":[{"name":"golang.org/x/sys","requirement":"v0.1.0"}],
				"indirectDependencies":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"versionKey": {"system":"GO","name":"github.com/example/subproc","version":"v1.0.0"},
			"publishedAt": "2026-01-02T03:04:05Z",
			"isDeprecated": false,
			"licenses": ["MIT"],
			"advisoryKeys": [],
			"links": [{"label":"SOURCE_REPO","url":"https://github.com/example/subproc"}]
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// fakeGitHubMetadata serves a repository payload for the fixture repository.
func fakeGitHubMetadata(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/tools" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"full_name": "example/tools",
			"html_url": "https://github.com/example/tools",
			"archived": false,
			"pushed_at": "2026-08-01T00:00:00Z",
			"default_branch": "main",
			"license": {"spdx_id": "Apache-2.0"},
			"stargazers_count": 1234,
			"forks_count": 56
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func e2eObservation(providerID, specimenID, kind, claim string, result model.EvidenceResult, source model.SourceRef, artifact string) model.Evidence {
	return discovery.NewObservation(discovery.ObservationSpec{
		ProviderID:  providerID,
		SubjectID:   specimenID,
		Kind:        kind,
		Claim:       claim,
		Result:      result,
		Source:      source,
		ObservedAt:  time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		Methodology: discovery.MethodologyGitHubCode,
		Artifact:    artifact,
	})
}

// TestQualityEndToEndWithRealPostgreSQL runs the Packet 7 flow against a real
// PostgreSQL instance with httptest enrichment providers:
//
// migrate -> seed -> fixture public candidates -> enrich -> policy ->
// quality assessment -> REFERENCE persisted -> reload.
//
// It then proves the second scenario: a public package with only metadata and
// no behavioural evidence returns needs_verification and persists nothing.
func TestQualityEndToEndWithRealPostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool, _ := startDatabase(t)
	store := postgres.NewStore(pool)

	// 1. seed the canonical first primitive.
	bundle, err := catalog.Load(repoRoot, manifestRel)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if err := catalog.Seed(ctx, bundle, store); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 2. fixture public candidates with attributable discovery relevance.
	packageSpecimen := model.Specimen{
		ID:          e2ePackageSpecimen,
		PrimitiveID: "process/bounded-subprocess",
		Name:        "github.com/example/subproc",
		Source: model.SourceRef{
			URL:      "https://pkg.go.dev/github.com/example/subproc",
			Revision: "v1.0.0",
			Path:     "github.com/example/subproc",
		},
		ReuseMode: []model.ReuseMode{model.ReuseDependency},
	}
	codeSpecimen := model.Specimen{
		ID:          e2eCodeSpecimen,
		PrimitiveID: "process/bounded-subprocess",
		Name:        "example/tools:exec/exec.go",
		Source: model.SourceRef{
			URL:      "https://github.com/example/tools/blob/abc123def456/exec/exec.go",
			Revision: "abc123def456",
			Path:     "exec/exec.go",
		},
		ReuseMode: []model.ReuseMode{model.ReuseReference},
	}
	for _, specimen := range []model.Specimen{packageSpecimen, codeSpecimen} {
		if err := store.UpsertSpecimen(ctx, specimen); err != nil {
			t.Fatalf("upsert specimen: %v", err)
		}
	}

	packageSource := packageSpecimen.Source
	if err := store.InsertEvidence(ctx, e2eObservation(
		discovery.ProviderPkgGoDev, packageSpecimen.ID, "discovery_match",
		`pkg.go.dev matched package "github.com/example/subproc"`, model.EvidenceInfo, packageSource,
		"query=subprocess cancellation")); err != nil {
		t.Fatalf("insert discovery evidence: %v", err)
	}
	// Packet 5 records the module identity with this exact code-owned claim
	// shape; deps.dev recovers the module path from it rather than guessing.
	if err := store.InsertEvidence(ctx, e2eObservation(
		discovery.ProviderPkgGoDev, packageSpecimen.ID, "package_module",
		`package "github.com/example/subproc" belongs to module "github.com/example/subproc"`,
		model.EvidenceInfo, packageSource, "query=subprocess cancellation")); err != nil {
		t.Fatalf("insert module evidence: %v", err)
	}
	codeSource := codeSpecimen.Source
	if err := store.InsertEvidence(ctx, e2eObservation(
		discovery.ProviderGitHubCode, codeSpecimen.ID, "discovery_match",
		`GitHub code search matched file "exec/exec.go" in repository "example/tools"`, model.EvidenceInfo, codeSource,
		"query=exec.CommandContext")); err != nil {
		t.Fatalf("insert discovery evidence: %v", err)
	}

	// 3. enrich package metadata and GitHub metadata through httptest providers.
	enrichClock := func() time.Time { return time.Date(2026, time.September, 26, 11, 0, 0, 0, time.UTC) }
	deps := fakeDepsDev(t)
	github := fakeGitHubMetadata(t)
	enricher := enrichment.NewService(store, enrichClock, []enrichment.Provider{
		depsdev.NewWithBaseURL(deps.URL),
		githubmeta.NewWithBaseURL(github.URL, ""),
	})
	enriched, err := enricher.Enrich(ctx, []string{packageSpecimen.ID, codeSpecimen.ID})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(enriched.Evidence) == 0 {
		t.Fatal("enrichment recorded nothing")
	}
	for _, observation := range enriched.Evidence {
		if observation.Result != model.EvidenceInfo && observation.Result != model.EvidenceUnknown {
			t.Errorf("enrichment emitted %q", observation.Result)
		}
		if observation.AppliesTo != "" {
			t.Errorf("enrichment aimed evidence at %q", observation.AppliesTo)
		}
	}

	packageEvidence, err := store.ListEvidenceBySubject(ctx, packageSpecimen.ID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(packageEvidence) < 5 {
		t.Errorf("package evidence = %d, want discovery plus enrichment", len(packageEvidence))
	}

	// 4. apply the authored baseline policy.
	baseline, err := policy.Load(repoRoot, "policies/public-go-baseline-v1.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	fixedClock := func() time.Time { return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC) }
	quality := resolver.NewQualityService(store, fixedClock)

	// 5a. a public package with only metadata must NOT resolve to DEPEND.
	packageDecision, err := quality.Choose(ctx, baseline, resolver.QualityRequest{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates: []resolver.CandidateRef{
			{SpecimenID: packageSpecimen.ID, ReuseMode: model.ReuseDependency},
		},
	})
	if err != nil {
		t.Fatalf("choose package: %v", err)
	}
	if packageDecision.Outcome.Decision.Status != resolver.StatusNeedsVerification {
		t.Errorf("package status = %q, want needs_verification", packageDecision.Outcome.Decision.Status)
	}
	if packageDecision.Outcome.Decision.Resolution != nil {
		t.Errorf("package resolution = %+v, want nil", packageDecision.Outcome.Decision.Resolution)
	}
	if packageDecision.ResolutionID != 0 {
		t.Errorf("package resolution id = %d, want 0", packageDecision.ResolutionID)
	}
	count, err := store.CountResolutions(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("resolutions = %d, want none after needs_verification", count)
	}
	for _, assessment := range packageDecision.Outcome.Decision.Assessments {
		if assessment.Disposition != resolver.NeedsVerification {
			t.Errorf("package disposition = %q", assessment.Disposition)
		}
	}

	// 5b. a relevant public reference resolves honestly to REFERENCE.
	referenceDecision, err := quality.Choose(ctx, baseline, resolver.QualityRequest{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates: []resolver.CandidateRef{
			{SpecimenID: codeSpecimen.ID, ReuseMode: model.ReuseReference},
		},
	})
	if err != nil {
		t.Fatalf("choose reference: %v", err)
	}
	if referenceDecision.ResolutionID == 0 {
		t.Fatalf("REFERENCE must be persisted: %+v", referenceDecision.Outcome.Decision)
	}
	resolution := referenceDecision.Outcome.Decision.Resolution
	if resolution.Outcome != model.OutcomeReference {
		t.Fatalf("outcome = %q", resolution.Outcome)
	}
	if resolution.SpecimenID != codeSpecimen.ID {
		t.Errorf("selected = %q", resolution.SpecimenID)
	}
	if resolution.PolicyID != baseline.ID {
		t.Errorf("policy id = %q, want %q", resolution.PolicyID, baseline.ID)
	}
	reasons := strings.Join(resolution.Reasons, " | ")
	if !strings.Contains(reasons, "reference-only") ||
		!strings.Contains(reasons, "behavioural contract satisfaction is not established") {
		t.Errorf("reasons = %v", resolution.Reasons)
	}
	if len(resolution.Unknowns) == 0 {
		t.Error("unresolved required requirements must be preserved")
	}
	if len(resolution.EvidenceIDs) == 0 {
		t.Error("evidence IDs must be preserved")
	}
	if len(resolution.Rejected) != 0 {
		t.Errorf("rejected = %+v", resolution.Rejected)
	}

	// 6. reload and confirm nothing was lost in the round trip.
	reloaded, err := quality.Resolution(ctx, referenceDecision.ResolutionID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PolicyID != baseline.ID {
		t.Errorf("reloaded policy id = %q", reloaded.PolicyID)
	}
	if len(reloaded.Unknowns) != len(resolution.Unknowns) {
		t.Errorf("reloaded unknowns = %v, want %v", reloaded.Unknowns, resolution.Unknowns)
	}
	if len(reloaded.EvidenceIDs) != len(resolution.EvidenceIDs) {
		t.Errorf("reloaded evidence ids = %v, want %v", reloaded.EvidenceIDs, resolution.EvidenceIDs)
	}
	if !reloaded.ResolvedAt.Equal(resolution.ResolvedAt) {
		t.Errorf("reloaded resolved at = %v", reloaded.ResolvedAt)
	}

	// 7. the stored row really carries the policy identity.
	var storedPolicy string
	if err := pool.QueryRow(ctx,
		"SELECT policy_id FROM resolutions WHERE id = $1", referenceDecision.ResolutionID).Scan(&storedPolicy); err != nil {
		t.Fatalf("query policy_id: %v", err)
	}
	if storedPolicy != baseline.ID {
		t.Errorf("stored policy_id = %q", storedPolicy)
	}

	// 8. popularity signals never reach the decision.
	payload, err := json.Marshal(referenceDecision.Outcome)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Mask hex digests before scanning. Evidence ids are content hashes, and
	// githubmeta records the (test) source URL, so the digest changes with the
	// ephemeral httptest port. A popularity scan against raw hex would be a
	// false positive whenever a digest happens to contain the banned substring.
	sanitised := hexDigest.ReplaceAllString(string(payload), "<digest>")
	for _, banned := range []string{"stargazers", "forks_count", "1234", "quality_score"} {
		if strings.Contains(sanitised, banned) {
			t.Errorf("popularity or score leaked into the decision: %s", payload)
		}
	}

	count, err = store.CountResolutions(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("resolutions = %d, want exactly 1", count)
	}
}
