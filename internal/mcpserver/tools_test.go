package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// fixture wires the tools over the real Packet 7 quality service and the real
// outcome service, with fakes only where a provider would be contacted.
type fixture struct {
	store      *memoryStore
	discoverer *recordingDiscoverer
	enricher   *recordingEnricher
	deps       Dependencies
}

func newFixture(external bool) *fixture {
	store := newMemoryStore()
	store.seed()
	discoverer := &recordingDiscoverer{}
	enricher := &recordingEnricher{}
	return &fixture{
		store:      store,
		discoverer: discoverer,
		enricher:   enricher,
		deps: Dependencies{
			Catalog:                   store,
			Discoverer:                discoverer,
			Enricher:                  enricher,
			Resolver:                  resolver.NewQualityService(store, fixtureClock),
			Inspector:                 store,
			Outcomes:                  outcome.NewService(store, fixtureClock),
			ExternalOperationsEnabled: external,
		},
	}
}

func (f *fixture) resolveRequest(candidateRefs ...CandidateRef) ResolveInput {
	return ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  candidateRefs,
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// ----------------------------------------------------------------- catalog

func TestCatalogListIsBoundedAndOrdered(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleCatalog(context.Background(), nil, CatalogInput{})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(result.Primitives) != 1 {
		t.Fatalf("primitives = %d, want 1 (one seeded capability)", len(result.Primitives))
	}
	if result.Primitives[0].PrimitiveID != fixturePrimitive {
		t.Errorf("primitive_id = %q, want %q", result.Primitives[0].PrimitiveID, fixturePrimitive)
	}
	for i := 1; i < len(result.Primitives); i++ {
		if result.Primitives[i-1].PrimitiveID >= result.Primitives[i].PrimitiveID {
			t.Errorf("primitives are not in id order: %q then %q",
				result.Primitives[i-1].PrimitiveID, result.Primitives[i].PrimitiveID)
		}
	}
	if result.Primitive != nil || result.Contract != nil {
		t.Errorf("list mode populated the detail fields: %+v", result)
	}
	if result.Primitives[0].ContractSummary == "" {
		t.Error("capability list did not carry the contract summary")
	}
}

func TestCatalogDetailReturnsOrderedRequirements(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleCatalog(context.Background(), nil, CatalogInput{PrimitiveID: fixturePrimitive})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if result.Primitive == nil || result.Contract == nil {
		t.Fatal("detail mode did not populate primitive and contract")
	}
	if len(result.Contract.Requirements) != len(requirementIDs) {
		t.Fatalf("requirements = %d, want %d", len(result.Contract.Requirements), len(requirementIDs))
	}
	for i, id := range requirementIDs {
		if result.Contract.Requirements[i].RequirementID != id {
			t.Errorf("requirement %d = %q, want %q", i, result.Contract.Requirements[i].RequirementID, id)
		}
		if !result.Contract.Requirements[i].Required {
			t.Errorf("requirement %q is not marked required", id)
		}
	}
	if len(result.Primitives) != 0 {
		t.Errorf("detail mode returned %d list entries", len(result.Primitives))
	}
}

func TestCatalogUnknownPrimitiveIsNotFound(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleCatalog(context.Background(), nil, CatalogInput{PrimitiveID: "does/not/exist"})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeNotFound {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeNotFound)
	}
}

// ----------------------------------------------------------------- discover

func TestDiscoverDisabledReturnsToolErrorWithoutCallingProviders(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleDiscover(context.Background(), nil, discovery.Profile{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Providers: []discovery.ProviderPlan{
			{ID: "pkg.go.dev", Queries: []discovery.Query{{Text: "subprocess", Limit: 3}}},
		},
	})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeExternalOperations {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeExternalOperations)
	}
	if f.discoverer.calls != 0 {
		t.Errorf("provider ran %d time(s) while disabled, want 0", f.discoverer.calls)
	}
}

func TestDiscoverEnabledReturnsCompactCandidates(t *testing.T) {
	f := newFixture(true)
	f.discoverer.result = discovery.Result{
		ObservedAt: fixtureClock(),
		Candidates: []discovery.Candidate{{
			ProviderID: "pkg.go.dev",
			Specimen: model.Specimen{
				ID: "public/pkg.go.dev/example/pkg@v1.0.0", PrimitiveID: fixturePrimitive,
				Name: "example/pkg", ReuseMode: []model.ReuseMode{model.ReuseDependency},
				Source: model.SourceRef{URL: "https://pkg.go.dev/example/pkg", Revision: "v1.0.0"},
			},
		}},
		Providers: []discovery.ProviderReport{{ID: "pkg.go.dev", Succeeded: true, CandidateCount: 1}},
	}

	_, result, err := f.deps.handleDiscover(context.Background(), nil, discovery.Profile{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Providers: []discovery.ProviderPlan{
			{ID: "pkg.go.dev", Queries: []discovery.Query{{Text: "subprocess", Limit: 3}}},
		},
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if f.discoverer.calls != 1 {
		t.Errorf("discoverer calls = %d, want 1", f.discoverer.calls)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].SpecimenID != "public/pkg.go.dev/example/pkg@v1.0.0" {
		t.Errorf("candidates = %+v", result.Candidates)
	}
	if result.Candidates[0].ReuseModes[0] != model.ReuseDependency {
		t.Errorf("reuse modes = %v", result.Candidates[0].ReuseModes)
	}
}

func TestDiscoverInvalidProfileIsInvalidRequest(t *testing.T) {
	f := newFixture(true)
	_, _, err := f.deps.handleDiscover(context.Background(), nil, discovery.Profile{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Providers:   []discovery.ProviderPlan{{ID: "not-a-provider", Queries: []discovery.Query{{Text: "x", Limit: 1}}}},
	})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeInvalidRequest {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeInvalidRequest)
	}
	if f.discoverer.calls != 0 {
		t.Errorf("discoverer ran %d time(s) for an invalid profile", f.discoverer.calls)
	}
}

func TestDiscoverAllProvidersFailedCarriesSafeIssues(t *testing.T) {
	f := newFixture(true)
	f.discoverer.err = &discovery.ProvidersFailedError{Providers: []string{"pkg.go.dev"}}
	f.discoverer.result.Providers = []discovery.ProviderReport{{
		ID: "pkg.go.dev", Issues: []discovery.ProviderIssue{{
			Kind: discovery.IssueUnavailable, Provider: "pkg.go.dev", Message: "provider unavailable",
		}},
	}}

	_, _, err := f.deps.handleDiscover(context.Background(), nil, discovery.Profile{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Providers: []discovery.ProviderPlan{
			{ID: "pkg.go.dev", Queries: []discovery.Query{{Text: "subprocess", Limit: 3}}},
		},
	})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeAllProvidersFailed {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeAllProvidersFailed)
	}
	if !strings.Contains(toolErr.Message, "provider unavailable") {
		t.Errorf("safe provider issue not preserved: %q", toolErr.Message)
	}
}

// ------------------------------------------------------------------ enrich

func TestEnrichDisabledReturnsToolErrorWithoutCallingProviders(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleEnrich(context.Background(), nil, EnrichInput{SpecimenIDs: []string{fixtureEligible}})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeExternalOperations {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeExternalOperations)
	}
	if f.enricher.calls != 0 {
		t.Errorf("provider ran %d time(s) while disabled, want 0", f.enricher.calls)
	}
}

func TestEnrichEnabledIsCompactAndInformational(t *testing.T) {
	f := newFixture(true)
	f.enricher.result = enrichment.Result{
		ObservedAt: fixtureClock(),
		Evidence: []model.Evidence{
			{ID: "e1", SubjectID: fixtureEligible, Kind: "source_license", Result: model.EvidenceInfo},
			{ID: "e2", SubjectID: fixtureEligible, Kind: "known_advisory_count", Result: model.EvidenceInfo},
		},
		Specimens: []enrichment.SpecimenReport{{
			SpecimenID: fixtureEligible, Supported: true, EvidenceCount: 2,
			Providers: []enrichment.ProviderReport{{ID: "deps.dev", Succeeded: true, EvidenceCount: 2}},
		}},
	}

	_, result, err := f.deps.handleEnrich(context.Background(), nil, EnrichInput{SpecimenIDs: []string{fixtureEligible}})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if result.Evidence != 2 {
		t.Errorf("evidence = %d, want 2", result.Evidence)
	}
	if result.Specimens[0].EvidenceCount != 2 {
		t.Errorf("specimen evidence count = %d, want 2", result.Specimens[0].EvidenceCount)
	}
	// Observation bodies are deliberately absent: the agent must ask
	// reusery_inspect_evidence for them.
	if strings.Contains(mustJSON(t, result), "subject_id") {
		t.Errorf("enrich output dumped evidence bodies: %s", mustJSON(t, result))
	}
}

func TestEnrichEmptySpecimenIDsIsInvalidRequest(t *testing.T) {
	f := newFixture(true)
	f.enricher.err = enrichment.ErrNoSpecimens
	_, _, err := f.deps.handleEnrich(context.Background(), nil, EnrichInput{})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeInvalidRequest {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeInvalidRequest)
	}
}

// ----------------------------------------------------------------- resolve

func TestResolveImplementationEligibleIsDepend(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Status != resolver.StatusResolved {
		t.Fatalf("status = %q, want resolved", result.Status)
	}
	if result.Outcome == nil || *result.Outcome != model.OutcomeDepend {
		t.Fatalf("outcome = %v, want depend", result.Outcome)
	}
	if result.ResolutionID == nil || *result.ResolutionID != 1 {
		t.Fatalf("resolution_id = %v, want 1", result.ResolutionID)
	}
	if result.Selected == nil || result.Selected.SpecimenID != fixtureEligible {
		t.Fatalf("selected = %+v", result.Selected)
	}
	if len(result.Unknowns) != 0 {
		t.Errorf("unknowns = %v, want none", result.Unknowns)
	}
	if len(f.store.resolutions) != 1 {
		t.Errorf("persisted resolutions = %d, want 1", len(f.store.resolutions))
	}
	summary, _, _ := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if summary == nil || len(summary.Content) != 1 {
		t.Fatal("expected exactly one text summary")
	}
	text := textOf(summary)
	if !strings.Contains(text, "DEPEND selected") {
		t.Errorf("summary = %q", text)
	}
}

// TestResolveMetadataOnlyIsNeedsVerification is the API-level statement that
// provider metadata can never become behavioural PASS or a DEPEND outcome.
func TestResolveMetadataOnlyIsNeedsVerification(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureMetadata, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Status != resolver.StatusNeedsVerification {
		t.Fatalf("status = %q, want needs_verification", result.Status)
	}
	if result.ResolutionID != nil || result.Outcome != nil || result.Selected != nil {
		t.Errorf("needs_verification produced a decision: %+v", result)
	}
	if len(result.Unknowns) != len(requirementIDs) {
		t.Errorf("unknowns = %d, want %d", len(result.Unknowns), len(requirementIDs))
	}
	if len(f.store.resolutions) != 0 {
		t.Errorf("persisted %d resolution(s), want none", len(f.store.resolutions))
	}
}

func TestResolveReferenceIsHonest(t *testing.T) {
	f := newFixture(false)
	summary, result, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureReference, ReuseMode: model.ReuseReference}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Status != resolver.StatusResolved || result.Outcome == nil || *result.Outcome != model.OutcomeReference {
		t.Fatalf("decision = %+v", result)
	}
	if len(result.Unknowns) != len(requirementIDs) {
		t.Errorf("unknowns = %d, want %d", len(result.Unknowns), len(requirementIDs))
	}
	joined := strings.Join(result.Reasons, "\n")
	if !strings.Contains(joined, "reference-only") ||
		!strings.Contains(joined, "behavioural contract satisfaction is not established") {
		t.Errorf("reference reasons are not honest: %v", result.Reasons)
	}
	for _, forbidden := range []string{"recommended", "verified", "approved"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("reference wording used %q: %s", forbidden, joined)
		}
	}
	text := textOf(summary)
	if !strings.Contains(text, "contract satisfaction is not established") {
		t.Errorf("summary = %q", text)
	}
}

func TestResolveAllBlockedIsBuildLocally(t *testing.T) {
	f := newFixture(false)
	pol := policyDTOForTest()
	pol.Reuse.Allowed = []model.ReuseMode{model.ReuseCopy}
	pol.Reuse.Preferred = []model.ReuseMode{}

	_, result, err := f.deps.handleResolve(context.Background(), nil, withPolicy(
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}), pol))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Status != resolver.StatusResolved || result.Outcome == nil || *result.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("decision = %+v", result)
	}
	if len(result.Rejected) == 0 {
		t.Error("build_locally preserved no negative knowledge")
	}
}

func TestResolveMissingContractIsNotFound(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  "does/not/exist",
		Candidates:  []CandidateRef{},
	})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeNotFound {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeNotFound)
	}
	if len(f.store.resolutions) != 0 {
		t.Errorf("persisted %d resolution(s) for a missing contract", len(f.store.resolutions))
	}
}

func TestResolveRejectsUnsupportedReuseMode(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: "borrow"}))
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeInvalidRequest {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeInvalidRequest)
	}
}

func TestResolveInvalidPolicyIsInvalidRequest(t *testing.T) {
	f := newFixture(false)
	pol := policyDTOForTest()
	pol.Selection.MaxOptions = 99
	_, _, err := f.deps.handleResolve(context.Background(), nil, withPolicy(
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}), pol))
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeInvalidRequest {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeInvalidRequest)
	}
}

func TestResolveOmitsPolicyAndUsesTheBuiltInBaseline(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureMetadata, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.PolicyID != "public-go-baseline/v1" {
		t.Errorf("policy_id = %q, want the built-in baseline", result.PolicyID)
	}
}

// ------------------------------------------------------------------- refine

func TestRefineRequiresAtLeastOneFeedbackItem(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleRefine(context.Background(), nil, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  []CandidateRef{{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}},
	})
	toolErr, ok := AsToolError(err)
	if !ok {
		t.Fatalf("error = %v, want a ToolError", err)
	}
	if toolErr.Code != CodeInvalidRequest {
		t.Errorf("code = %q, want %q", toolErr.Code, CodeInvalidRequest)
	}
}

func TestRefineNotQuiteSelectsADifferentCandidate(t *testing.T) {
	f := newFixture(false)
	candidates := []CandidateRef{
		{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency},
		{SpecimenID: fixtureLight, ReuseMode: model.ReuseDependency},
	}

	_, base, err := f.deps.handleResolve(context.Background(), nil, f.resolveRequest(candidates...))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if base.Selected == nil || base.Selected.SpecimenID != fixtureEligible {
		t.Fatalf("base selection = %+v", base.Selected)
	}

	_, refined, err := f.deps.handleRefine(context.Background(), nil, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  candidates,
		Feedback:    []policyFeedback{{CandidateID: fixtureEligible, Reason: feedbackNotQuite}},
	})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if refined.Selected == nil || refined.Selected.SpecimenID != fixtureLight {
		t.Fatalf("refined selection = %+v", refined.Selected)
	}
	if len(refined.AppliedFeedback) != 1 || refined.AppliedFeedback[0].Reason != feedbackNotQuite {
		t.Errorf("applied feedback = %+v", refined.AppliedFeedback)
	}
	found := false
	for _, rejected := range refined.Rejected {
		if rejected.SpecimenID == fixtureEligible {
			found = true
			if !strings.Contains(strings.Join(rejected.Reasons, " "), "user_feedback:not_quite") {
				t.Errorf("negative knowledge missing: %v", rejected.Reasons)
			}
		}
	}
	if !found {
		t.Errorf("excluded candidate missing from rejected: %+v", refined.Rejected)
	}
	if len(f.store.resolutions) != 2 {
		t.Errorf("persisted %d resolution(s), want 2 (base resolve plus re-resolve)", len(f.store.resolutions))
	}
}

func TestRefineTooManyDependenciesChangesTheEffectivePolicy(t *testing.T) {
	f := newFixture(false)
	pol := policyDTOForTest()
	pol.Dependencies.MaxDirect = intPointer(10)
	candidates := []CandidateRef{
		{SpecimenID: fixtureHeavy, ReuseMode: model.ReuseDependency},
		{SpecimenID: fixtureLight, ReuseMode: model.ReuseDependency},
	}

	_, base, err := f.deps.handleResolve(context.Background(), nil, withPolicy(f.resolveRequest(candidates...), pol))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if base.Selected == nil || base.Selected.SpecimenID != fixtureHeavy {
		t.Fatalf("base selection = %+v", base.Selected)
	}

	_, refined, err := f.deps.handleRefine(context.Background(), nil, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  candidates,
		Policy:      &pol,
		Feedback: []policyFeedback{
			{CandidateID: fixtureHeavy, Reason: feedbackTooManyDependencies},
		},
	})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if refined.Selected == nil || refined.Selected.SpecimenID != fixtureLight {
		t.Fatalf("refined selection = %+v", refined.Selected)
	}
	if refined.EffectivePolicy.MaxDirect == nil || *refined.EffectivePolicy.MaxDirect != 7 {
		t.Errorf("effective max_direct = %v, want 7", refined.EffectivePolicy.MaxDirect)
	}
	// This is re-resolution, not queue pagination: the effective policy itself
	// changed, and the previous answer is no longer selectable.
	if base.Selected.SpecimenID == refined.Selected.SpecimenID {
		t.Error("refinement returned the same candidate")
	}
}

// ------------------------------------------------------------------ inspect

func TestInspectEvidencePaginatesWithNativeContinuation(t *testing.T) {
	f := newFixture(false)

	_, first, err := f.deps.handleInspectEvidence(context.Background(), nil, EvidenceInput{
		SubjectID: fixtureEligible, Limit: 3,
	})
	if err != nil {
		t.Fatalf("inspect evidence: %v", err)
	}
	if len(first.Evidence) != 3 {
		t.Fatalf("first page = %d records, want 3", len(first.Evidence))
	}
	if first.NextAfter == nil || first.NextAfter.ObservedAt == nil || first.NextAfter.EvidenceID == "" {
		t.Fatalf("missing continuation: %+v", first.NextAfter)
	}

	_, second, err := f.deps.handleInspectEvidence(context.Background(), nil, EvidenceInput{
		SubjectID: fixtureEligible, Limit: 50, After: first.NextAfter,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	seen := map[string]bool{}
	for _, record := range first.Evidence {
		seen[record.ID] = true
	}
	for _, record := range second.Evidence {
		if seen[record.ID] {
			t.Errorf("duplicate observation %q across pages", record.ID)
		}
		seen[record.ID] = true
	}
	total := len(f.store.evidence[fixtureEligible])
	if len(seen) != total {
		t.Errorf("paged %d of %d observations", len(seen), total)
	}
}

func TestInspectEvidenceRejectsBadLimits(t *testing.T) {
	f := newFixture(false)
	for _, limit := range []int{-1, MaxEvidenceLimit + 1} {
		_, _, err := f.deps.handleInspectEvidence(context.Background(), nil,
			EvidenceInput{SubjectID: fixtureEligible, Limit: limit})
		toolErr, ok := AsToolError(err)
		if !ok || toolErr.Code != CodeInvalidRequest {
			t.Errorf("limit %d: error = %v", limit, err)
		}
	}
}

func TestInspectEvidenceRejectsIncompleteCursor(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleInspectEvidence(context.Background(), nil, EvidenceInput{
		SubjectID: fixtureEligible,
		After:     &EvidenceCursor{EvidenceID: "ev/x"},
	})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeInvalidRequest {
		t.Errorf("error = %v, want invalid_request", err)
	}
}

func TestInspectResolutionReturnsHistoryPlusOutcomeFeedback(t *testing.T) {
	f := newFixture(false)
	_, resolved, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if _, _, err := f.deps.handleReportOutcome(context.Background(), nil, OutcomeInput{
		ResolutionID: *resolved.ResolutionID, Kind: outcome.KindAdopted, Note: "used in the parser",
	}); err != nil {
		t.Fatalf("report outcome: %v", err)
	}

	_, stored, err := f.deps.handleInspectResolution(context.Background(), nil,
		InspectResolutionInput{ResolutionID: *resolved.ResolutionID})
	if err != nil {
		t.Fatalf("inspect resolution: %v", err)
	}
	if stored.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q", stored.Resolution.Outcome)
	}
	if len(stored.OutcomeFeedback) != 1 || stored.OutcomeFeedback[0].Kind != outcome.KindAdopted {
		t.Errorf("outcome feedback = %+v", stored.OutcomeFeedback)
	}
}

func TestInspectResolutionUnknownIsNotFound(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleInspectResolution(context.Background(), nil, InspectResolutionInput{ResolutionID: 99})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeNotFound {
		t.Errorf("error = %v, want not_found", err)
	}
}

// ------------------------------------------------------------------ outcome

func TestReportOutcomeIsAppendOnlyAndDoesNotMutateTheResolution(t *testing.T) {
	f := newFixture(false)
	_, resolved, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	before := f.store.resolutions[0]

	for _, kind := range []outcome.Kind{outcome.KindAdopted, outcome.KindIntegrationSucceeded} {
		_, recorded, err := f.deps.handleReportOutcome(context.Background(), nil, OutcomeInput{
			ResolutionID: *resolved.ResolutionID, Kind: kind, Note: "actual result",
		})
		if err != nil {
			t.Fatalf("report %s: %v", kind, err)
		}
		if recorded.Kind != kind {
			t.Errorf("recorded kind = %q, want %q", recorded.Kind, kind)
		}
	}

	if len(f.store.feedback) != 2 {
		t.Fatalf("recorded %d event(s), want 2", len(f.store.feedback))
	}
	if f.store.feedback[0].RecordedAt.After(f.store.feedback[1].RecordedAt) {
		t.Error("events are not in chronological order")
	}
	if mustJSON(t, f.store.resolutions[0]) != mustJSON(t, before) {
		t.Error("reporting an outcome mutated the stored Resolution")
	}
	if len(f.store.evidence[fixtureEligible]) != totalFixtureEvidence(f, fixtureEligible) {
		t.Error("reporting an outcome created Evidence")
	}
	if f.store.resolutions[0].Outcome != model.OutcomeDepend {
		t.Error("reporting an outcome re-resolved the decision")
	}
}

func TestReportOutcomeRejectsUnknownResolution(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleReportOutcome(context.Background(), nil,
		OutcomeInput{ResolutionID: 42, Kind: outcome.KindAdopted})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeNotFound {
		t.Errorf("error = %v, want not_found", err)
	}
	if len(f.store.feedback) != 0 {
		t.Errorf("recorded %d event(s) for a missing resolution", len(f.store.feedback))
	}
}

func TestReportOutcomeRejectsUnknownKind(t *testing.T) {
	f := newFixture(false)
	_, _, err := f.deps.handleReportOutcome(context.Background(), nil,
		OutcomeInput{ResolutionID: 1, Kind: outcome.Kind("worked_great")})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeInvalidRequest {
		t.Errorf("error = %v, want invalid_request", err)
	}
}

func TestOutcomeNoteLengthIsBoundedInCharacters(t *testing.T) {
	f := newFixture(false)
	_, resolved, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	overLimit := strings.Repeat("é", outcome.MaxNoteLength+1)
	_, _, err = f.deps.handleReportOutcome(context.Background(), nil, OutcomeInput{
		ResolutionID: *resolved.ResolutionID, Kind: outcome.KindAdopted, Note: overLimit,
	})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeInvalidRequest {
		t.Errorf("error = %v, want invalid_request for an over-long note", err)
	}
}

func totalFixtureEvidence(f *fixture, subject string) int {
	return len(f.store.evidence[subject])
}

// ------------------------------------------------------------- guard rails

// TestNoToolClaimsUniversalQuality keeps the agent-facing copy honest: no
// description may present Reusery as a universal quality oracle.
func TestNoToolClaimsUniversalQuality(t *testing.T) {
	forbidden := []string{
		"best implementation", "guaranteed", "universally", "always correct",
		"verified by reusery", "approved by reusery",
	}
	for name, tool := range registeredDescriptions() {
		lower := strings.ToLower(tool)
		for _, phrase := range forbidden {
			if strings.Contains(lower, phrase) {
				t.Errorf("tool %s claims %q", name, phrase)
			}
		}
	}
}

// TestStructuredOutputCarriesNoScoreGuards the standing rule that Reusery has
// no universal quality score anywhere in its agent-facing output.
func TestStructuredOutputCarriesNoScore(t *testing.T) {
	f := newFixture(false)
	_, result, err := f.deps.handleResolve(context.Background(), nil,
		f.resolveRequest(CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	payload := mustJSON(t, result)
	for _, banned := range []string{"quality_score", "confidence", "overall_score", "stars"} {
		if strings.Contains(payload, banned) {
			t.Errorf("structured output contains %q: %s", banned, payload)
		}
	}
}

func TestErrorCodesAreStable(t *testing.T) {
	want := []string{
		CodeInvalidRequest, CodeNotFound, CodeConflict, CodeExternalOperations,
		CodeUpstreamRateLimited, CodeUpstreamTimeout, CodeUpstreamUnavailable,
		CodeAllProvidersFailed, CodeInternalError,
	}
	got := CodeOrder()
	if len(got) != len(want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("code %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClassifyNeverLeaksSecrets(t *testing.T) {
	secrets := []string{"postgres://user:pass@localhost/db", "sk-secret", "ghp_secret"}
	for _, secret := range secrets {
		err := classify(errorsNew(secret))
		if strings.Contains(err.Message, secret) {
			t.Errorf("classify leaked %q: %q", secret, err.Message)
		}
		if err.Code != CodeInternalError {
			t.Errorf("unknown failures must collapse to internal_error, got %q", err.Code)
		}
	}
}

func TestExternalOperationsSwitchesAreIndependent(t *testing.T) {
	if !strings.Contains(externalOperationsDisabled().Message, "REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS") {
		t.Errorf("message must name the MCP switch: %q", externalOperationsDisabled().Message)
	}
	if strings.Contains(externalOperationsDisabled().Message, "REUSERY_API_ENABLE_EXTERNAL_OPERATIONS") {
		t.Error("MCP must not name the HTTP switch")
	}
}
