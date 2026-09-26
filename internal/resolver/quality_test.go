package resolver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// ---- fixtures ---------------------------------------------------------

func qSpecimen(id, revision string, modes ...model.ReuseMode) model.Specimen {
	return model.Specimen{
		ID:          id,
		PrimitiveID: kernelPrimitive().ID,
		Name:        id,
		Source: model.SourceRef{
			URL:      "https://example.test/" + id,
			Revision: revision,
			Path:     "candidate.go",
		},
		ReuseMode: modes,
	}
}

func qEvidence(specimenID, kind string, result model.EvidenceResult, value any) []model.Evidence {
	artifact := ""
	if value != nil {
		encoded, err := policy.EncodeFactArtifact(value)
		if err != nil {
			panic(err)
		}
		artifact = encoded
	}
	return []model.Evidence{{
		ID:          "ev/" + specimenID + "/" + kind,
		SubjectID:   specimenID,
		Kind:        kind,
		Claim:       "test observation for " + kind,
		Result:      result,
		Source:      model.SourceRef{URL: "https://example.test/api"},
		ObservedAt:  kernelResolvedAt,
		Methodology: "quality unit test",
		Artifact:    artifact,
	}}
}

func qRelevance(specimenID string) []model.Evidence {
	return []model.Evidence{{
		ID:          "ev/" + specimenID + "/discovery_match",
		SubjectID:   specimenID,
		Kind:        policy.KindDiscoveryMatch,
		Claim:       "a provider surfaced this candidate",
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://example.test/api"},
		ObservedAt:  kernelResolvedAt,
		Methodology: "quality unit test",
	}}
}

// qBehaviour returns behavioural evidence only for the requirements named in
// results. An unlisted requirement therefore stays UNKNOWN, which is exactly
// how missing evidence behaves.
func qBehaviour(specimenID string, contract model.Contract, results map[string]model.EvidenceResult) []model.Evidence {
	out := make([]model.Evidence, 0, len(results))
	for _, requirement := range contract.Requirements {
		result, listed := results[requirement.ID]
		if !listed {
			continue
		}
		out = append(out, evidenceFor(specimenID, requirement.ID, "ev/"+specimenID+"/req/"+requirement.ID, result))
	}
	return out
}

func qConflict(specimenID string, contract model.Contract, requirementID string) []model.Evidence {
	return []model.Evidence{
		evidenceFor(specimenID, requirementID, "ev/"+specimenID+"/req/"+requirementID+"/pass", model.EvidencePass),
		evidenceFor(specimenID, requirementID, "ev/"+specimenID+"/req/"+requirementID+"/fail", model.EvidenceFail),
	}
}

func qAllPass(specimenID string, contract model.Contract) map[string]model.EvidenceResult {
	results := map[string]model.EvidenceResult{}
	for _, requirement := range contract.Requirements {
		if requirement.Required {
			results[requirement.ID] = model.EvidencePass
		}
	}
	return results
}

// permissivePolicy allows every reuse mode, has no numeric threshold other
// than a generous dependency maximum, and treats missing maintenance facts as
// acceptable. It is the "policy that allows the facts" of §66.
func permissivePolicy() policy.Policy {
	maxDirect := 8
	return policy.Policy{
		SchemaVersion: policy.SchemaVersion,
		ID:            "test/permissive/v1",
		Reuse: policy.ReusePolicy{
			Allowed: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference},
		},
		Licence: policy.LicencePolicy{
			Unknown:  policy.ActionAllow,
			Multiple: policy.ActionReview,
			Unlisted: policy.ActionAllow,
		},
		Security: policy.SecurityPolicy{
			KnownAdvisory: policy.ActionReview,
			Unknown:       policy.ActionAllow,
		},
		Dependencies: policy.DependencyPolicy{
			Unknown:   policy.ActionAllow,
			MaxDirect: &maxDirect,
		},
		Maintenance: policy.MaintenancePolicy{
			Archived:   policy.ActionReview,
			Deprecated: policy.ActionReview,
			Stale:      policy.ActionReview,
			Unknown:    policy.ActionAllow,
		},
		Source: policy.SourcePolicy{
			RequireRevisionFor: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt},
			MissingRevision:    policy.ActionReview,
		},
		Selection: policy.SelectionPolicy{MaxOptions: 3},
	}
}

func qInput(candidates []CandidateOption, pol policy.Policy) QualityInput {
	return QualityInput{
		Primitive:  kernelPrimitive(),
		Contract:   kernelContract(),
		Candidates: candidates,
		Policy:     pol,
		Now:        kernelResolvedAt,
	}
}

func option(specimen model.Specimen, mode model.ReuseMode, groups ...[]model.Evidence) CandidateOption {
	return CandidateOption{Specimen: specimen, ReuseMode: mode, Evidence: concat(groups...)}
}

func concat(groups ...[]model.Evidence) []model.Evidence {
	out := make([]model.Evidence, 0)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func decideOrFail(t *testing.T, in QualityInput) QualityOutcome {
	t.Helper()
	outcome, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return outcome
}

func findAssessment(t *testing.T, outcome QualityOutcome, specimenID string) CandidateAssessment {
	t.Helper()
	for _, assessment := range outcome.Decision.Assessments {
		if assessment.SpecimenID == specimenID {
			return assessment
		}
	}
	t.Fatalf("no assessment for %q", specimenID)
	return CandidateAssessment{}
}

// ---- §74 A–E ----------------------------------------------------------

func TestA_DirectEligibleCandidateResolvesToDepend(t *testing.T) {
	specimen := qSpecimen("fixture/eligible", "v1.0.0", model.ReuseDependency)
	candidate := option(specimen, model.ReuseDependency,
		qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract())),
		qEvidence(specimen.ID, policy.KindSourceLicense, model.EvidenceInfo, "MIT"),
	)

	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, permissivePolicy()))
	if outcome.Decision.Status != StatusResolved {
		t.Fatalf("status = %q", outcome.Decision.Status)
	}
	if outcome.Decision.Resolution == nil || outcome.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Fatalf("resolution = %+v", outcome.Decision.Resolution)
	}
	if assessment := findAssessment(t, outcome, specimen.ID); assessment.Disposition != ImplementationEligible {
		t.Errorf("disposition = %q", assessment.Disposition)
	}
}

func TestB_DirectCandidateWithUnknownBehaviourReturnsNeedsVerificationAndPersistsNothing(t *testing.T) {
	specimen := qSpecimen("fixture/unknown", "v1.0.0", model.ReuseDependency)
	candidate := option(specimen, model.ReuseDependency, qEvidence(specimen.ID, policy.KindSourceLicense, model.EvidenceInfo, "MIT"))

	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, permissivePolicy()))
	if outcome.Decision.Status != StatusNeedsVerification {
		t.Fatalf("status = %q, want needs_verification", outcome.Decision.Status)
	}
	if outcome.Decision.Resolution != nil {
		t.Fatalf("resolution = %+v, want nil: UNKNOWN must never become BUILD LOCALLY", outcome.Decision.Resolution)
	}
	if outcome.Decision.Selected != nil {
		t.Error("nothing may be selected")
	}
	assessment := findAssessment(t, outcome, specimen.ID)
	if assessment.Disposition != NeedsVerification {
		t.Errorf("disposition = %q", assessment.Disposition)
	}
}

func TestC_ReferenceCandidateWithUnknownBehaviourResolvesToReference(t *testing.T) {
	specimen := qSpecimen("fixture/reference", "", model.ReuseReference)
	specimen.Source.Revision = ""
	candidate := option(specimen, model.ReuseReference, qRelevance(specimen.ID))

	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, permissivePolicy()))
	if outcome.Decision.Status != StatusResolved {
		t.Fatalf("status = %q", outcome.Decision.Status)
	}
	resolution := outcome.Decision.Resolution
	if resolution == nil || resolution.Outcome != model.OutcomeReference {
		t.Fatalf("resolution = %+v", resolution)
	}
	reasons := strings.Join(resolution.Reasons, " | ")
	if !strings.Contains(reasons, "selected as reference-only engineering knowledge") {
		t.Errorf("reasons = %v", reasons)
	}
	if !strings.Contains(reasons, "behavioural contract satisfaction is not established") {
		t.Errorf("reasons = %v", reasons)
	}
	if len(resolution.Unknowns) == 0 {
		t.Error("Resolution.Unknowns must preserve the unresolved required requirements")
	}
	for _, unknown := range resolution.Unknowns {
		if !strings.Contains(unknown, specimen.ID) || !strings.Contains(unknown, "unknown") {
			t.Errorf("unknown %q does not identify the specimen and requirement", unknown)
		}
	}
}

func TestD_AllHardBlockedCandidatesResolveToBuildLocally(t *testing.T) {
	contract := kernelContract()
	failing := qSpecimen("fixture/failing", "v1.0.0", model.ReuseDependency)
	other := qSpecimen("fixture/passing-but-denied", "v1.0.0", model.ReuseCopy)

	pol := permissivePolicy()
	pol.Reuse.Allowed = []model.ReuseMode{model.ReuseDependency} // copy is denied

	candidates := []CandidateOption{
		option(failing, model.ReuseDependency, qBehaviour(failing.ID, contract, map[string]model.EvidenceResult{
			"bounds-stdout": model.EvidenceFail,
		})),
		option(other, model.ReuseCopy, qBehaviour(other.ID, contract, qAllPass(other.ID, contract))),
	}

	outcome := decideOrFail(t, qInput(candidates, pol))
	if outcome.Decision.Status != StatusResolved {
		t.Fatalf("status = %q", outcome.Decision.Status)
	}
	resolution := outcome.Decision.Resolution
	if resolution == nil || resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("resolution = %+v", resolution)
	}
	reasons := strings.Join(resolution.Reasons, " | ")
	if !strings.Contains(reasons, "behavioural failures") && !strings.Contains(reasons, "denied by policy") {
		t.Errorf("build_locally reasons are too generic: %v", resolution.Reasons)
	}
	if len(resolution.Rejected) != 2 {
		t.Errorf("rejected = %+v, want both candidates with their reasons", resolution.Rejected)
	}
}

func TestE_ZeroCandidatesResolveToBuildLocallyWithASpecificReason(t *testing.T) {
	outcome := decideOrFail(t, qInput(nil, permissivePolicy()))
	resolution := outcome.Decision.Resolution
	if resolution == nil || resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("resolution = %+v", resolution)
	}
	if len(resolution.Reasons) != 1 || resolution.Reasons[0] != "no candidate options were supplied" {
		t.Errorf("reasons = %v", resolution.Reasons)
	}
}

// ---- §74 F–K ----------------------------------------------------------

func TestF_EligibleDirectCandidateOutranksReferenceOnly(t *testing.T) {
	eligible := qSpecimen("fixture/aaa-eligible", "v1.0.0", model.ReuseDependency)
	reference := qSpecimen("fixture/bbb-reference", "", model.ReuseReference)
	reference.Source.Revision = ""

	candidates := []CandidateOption{
		option(reference, model.ReuseReference, qRelevance(reference.ID)),
		option(eligible, model.ReuseDependency, qBehaviour(eligible.ID, kernelContract(), qAllPass(eligible.ID, kernelContract()))),
	}

	outcome := decideOrFail(t, qInput(candidates, permissivePolicy()))
	if outcome.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q, want depend", outcome.Decision.Resolution.Outcome)
	}
	if outcome.Decision.Selected.SpecimenID != eligible.ID {
		t.Errorf("selected = %q", outcome.Decision.Selected.SpecimenID)
	}
}

func TestG_PolicyPreferredModeOrdersEligibleCandidates(t *testing.T) {
	first := qSpecimen("fixture/aaa-copy", "v1.0.0", model.ReuseCopy, model.ReuseDependency)
	second := qSpecimen("fixture/bbb-dependency", "v1.0.0", model.ReuseDependency, model.ReuseCopy)

	// Both are eligible under the copy mode the caller chose for the first
	// specimen and the dependency mode chosen for the second.
	candidates := []CandidateOption{
		option(first, model.ReuseCopy, qBehaviour(first.ID, kernelContract(), qAllPass(first.ID, kernelContract()))),
		option(second, model.ReuseDependency, qBehaviour(second.ID, kernelContract(), qAllPass(second.ID, kernelContract()))),
	}

	pol := permissivePolicy()
	pol.Reuse.Preferred = []model.ReuseMode{model.ReuseDependency, model.ReuseCopy}

	outcome := decideOrFail(t, qInput(candidates, pol))
	if outcome.Decision.Selected.SpecimenID != second.ID {
		t.Errorf("selected = %q, want the preferred dependency candidate", outcome.Decision.Selected.SpecimenID)
	}
	if outcome.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q", outcome.Decision.Resolution.Outcome)
	}
}

func TestH_PinnedReferenceBeatsUnpinnedReference(t *testing.T) {
	unpinned := qSpecimen("fixture/aaa-unpinned", "", model.ReuseReference)
	unpinned.Source.Revision = ""
	pinned := qSpecimen("fixture/bbb-pinned", "abc123", model.ReuseReference)

	candidates := []CandidateOption{
		option(unpinned, model.ReuseReference, qRelevance(unpinned.ID)),
		option(pinned, model.ReuseReference, qRelevance(pinned.ID)),
	}

	outcome := decideOrFail(t, qInput(candidates, permissivePolicy()))
	if outcome.Decision.Selected.SpecimenID != pinned.ID {
		t.Errorf("selected = %q, want the pinned reference", outcome.Decision.Selected.SpecimenID)
	}
}

func TestI_StableIDTieBreakIsDeterministicAndDisclosed(t *testing.T) {
	a := qSpecimen("fixture/aaa", "v1.0.0", model.ReuseDependency)
	b := qSpecimen("fixture/bbb", "v1.0.0", model.ReuseDependency)
	evidence := func(id string) []model.Evidence {
		return concat(
			qBehaviour(id, kernelContract(), qAllPass(id, kernelContract())),
			qEvidence(id, policy.KindSourceLicense, model.EvidenceInfo, "MIT"),
		)
	}

	candidates := []CandidateOption{
		option(a, model.ReuseDependency, evidence(a.ID)),
		option(b, model.ReuseDependency, evidence(b.ID)),
	}

	first := decideOrFail(t, qInput(candidates, permissivePolicy()))
	second := decideOrFail(t, qInput(candidates, permissivePolicy()))

	if first.Decision.Selected.SpecimenID != "fixture/aaa" || second.Decision.Selected.SpecimenID != "fixture/aaa" {
		t.Fatalf("selection is not stable: %q then %q", first.Decision.Selected.SpecimenID, second.Decision.Selected.SpecimenID)
	}

	reasons := strings.Join(first.Decision.Resolution.Reasons, " | ")
	if !strings.Contains(reasons, "stable specimen ID was used as the deterministic tie-break") {
		t.Errorf("the tie-break must be disclosed: %v", first.Decision.Resolution.Reasons)
	}
	disclosed := false
	for _, tradeoff := range first.Decision.Selected.Unknowns {
		if tradeoff.Dimension == "ordering" && strings.Contains(tradeoff.Message, "do not distinguish") {
			disclosed = true
		}
	}
	if !disclosed {
		t.Errorf("unknowns = %+v", first.Decision.Selected.Unknowns)
	}
}

func TestJ_ShortlistIsBoundedByMaxOptions(t *testing.T) {
	candidates := make([]CandidateOption, 0, 6)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		specimen := qSpecimen("fixture/"+name, "v1.0.0", model.ReuseDependency)
		candidates = append(candidates, option(specimen, model.ReuseDependency,
			qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract()))))
	}

	pol := permissivePolicy()
	pol.Selection.MaxOptions = 3
	outcome := decideOrFail(t, qInput(candidates, pol))
	if len(outcome.Decision.Shortlist) != 3 {
		t.Errorf("shortlist = %d, want 3", len(outcome.Decision.Shortlist))
	}

	pol.Selection.MaxOptions = 5
	outcome = decideOrFail(t, qInput(candidates, pol))
	if len(outcome.Decision.Shortlist) != 5 {
		t.Errorf("shortlist = %d, want the hard ceiling of 5", len(outcome.Decision.Shortlist))
	}
}

func TestK_ShortlistDiversifiesByReuseModeFirst(t *testing.T) {
	dependencyA := qSpecimen("fixture/aaa-dependency", "v1.0.0", model.ReuseDependency, model.ReuseCopy)
	dependencyB := qSpecimen("fixture/bbb-dependency", "v1.0.0", model.ReuseDependency, model.ReuseCopy)
	reference := qSpecimen("fixture/ccc-reference", "", model.ReuseReference)
	reference.Source.Revision = ""

	candidates := []CandidateOption{
		option(dependencyA, model.ReuseDependency, qBehaviour(dependencyA.ID, kernelContract(), qAllPass(dependencyA.ID, kernelContract()))),
		option(dependencyB, model.ReuseDependency, qBehaviour(dependencyB.ID, kernelContract(), qAllPass(dependencyB.ID, kernelContract()))),
		option(reference, model.ReuseReference, qRelevance(reference.ID)),
	}

	pol := permissivePolicy()
	pol.Selection.MaxOptions = 2
	outcome := decideOrFail(t, qInput(candidates, pol))

	modes := map[model.ReuseMode]bool{}
	ids := map[string]bool{}
	for _, candidate := range outcome.Decision.Shortlist {
		modes[candidate.ReuseMode] = true
		ids[candidate.SpecimenID] = true
	}
	if len(modes) != 2 {
		t.Errorf("shortlist modes = %v, want two materially different options", modes)
	}
	if !ids[dependencyA.ID] || !ids[reference.ID] {
		t.Errorf("shortlist ids = %v", ids)
	}
}

// ---- §74 L -----------------------------------------------------------

func TestL_PolicyIDReachesTheResolution(t *testing.T) {
	specimen := qSpecimen("fixture/eligible", "v1.0.0", model.ReuseDependency)
	candidate := option(specimen, model.ReuseDependency,
		qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract())))

	pol := permissivePolicy()
	pol.ID = "public-go-baseline/v1"
	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, pol))

	if outcome.Decision.PolicyID != "public-go-baseline/v1" {
		t.Errorf("decision policy id = %q", outcome.Decision.PolicyID)
	}
	if outcome.Decision.Resolution.PolicyID != "public-go-baseline/v1" {
		t.Errorf("resolution policy id = %q", outcome.Decision.Resolution.PolicyID)
	}
}

// ---- §72 disposition rules --------------------------------------------

func TestDispositionRules(t *testing.T) {
	contract := kernelContract()

	t.Run("fully satisfied and policy allows is implementation eligible", func(t *testing.T) {
		specimen := qSpecimen("fixture/eligible", "v1.0.0", model.ReuseDependency)
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency,
			qBehaviour(specimen.ID, contract, qAllPass(specimen.ID, contract)))}, permissivePolicy()))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != ImplementationEligible {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("fully satisfied but policy review is needs_review", func(t *testing.T) {
		specimen := qSpecimen("fixture/review", "v1.0.0", model.ReuseDependency)
		pol := permissivePolicy()
		pol.Security.KnownAdvisory = policy.ActionReview
		evidence := concat(
			qBehaviour(specimen.ID, contract, qAllPass(specimen.ID, contract)),
			qEvidence(specimen.ID, policy.KindKnownAdvisory, model.EvidenceInfo, "GHSA-1111-2222-3333"),
			qEvidence(specimen.ID, policy.KindKnownAdvisoryCount, model.EvidenceInfo, 1),
		)
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, pol))

		assessment := findAssessment(t, outcome, specimen.ID)
		if assessment.Disposition != NeedsReview {
			t.Fatalf("disposition = %q, want needs_review", assessment.Disposition)
		}
		if outcome.Decision.Status != StatusNeedsVerification || outcome.Decision.Resolution != nil {
			t.Errorf("a review-only shortlist must not resolve: %+v", outcome.Decision)
		}
	})

	t.Run("unknown required behaviour is needs_verification", func(t *testing.T) {
		specimen := qSpecimen("fixture/unknown", "v1.0.0", model.ReuseDependency)
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency)}, permissivePolicy()))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != NeedsVerification {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("required fail is blocked", func(t *testing.T) {
		specimen := qSpecimen("fixture/fail", "v1.0.0", model.ReuseDependency)
		evidence := qBehaviour(specimen.ID, contract, map[string]model.EvidenceResult{"bounds-stdout": model.EvidenceFail})
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, permissivePolicy()))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != Blocked {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("required conflicting is blocked", func(t *testing.T) {
		specimen := qSpecimen("fixture/conflict", "v1.0.0", model.ReuseDependency)
		evidence := qConflict(specimen.ID, contract, "bounds-stdout")
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, permissivePolicy()))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != Blocked {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("hard policy deny is blocked even with full behavioural satisfaction", func(t *testing.T) {
		specimen := qSpecimen("fixture/denied", "v1.0.0", model.ReuseDependency)
		pol := permissivePolicy()
		pol.Licence.Deny = []string{"GPL-3.0"}
		evidence := concat(
			qBehaviour(specimen.ID, contract, qAllPass(specimen.ID, contract)),
			qEvidence(specimen.ID, policy.KindSourceLicense, model.EvidenceInfo, "GPL-3.0"),
		)
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, pol))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != Blocked {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("reference with required unknown is reference_only", func(t *testing.T) {
		specimen := qSpecimen("fixture/reference", "", model.ReuseReference)
		specimen.Source.Revision = ""
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseReference, qRelevance(specimen.ID))}, permissivePolicy()))
		assessment := findAssessment(t, outcome, specimen.ID)
		if assessment.Disposition != ReferenceOnly {
			t.Fatalf("disposition = %q", assessment.Disposition)
		}
		if len(assessment.Unknowns) == 0 {
			t.Error("required unknown behaviour must appear in Unknowns")
		}
	})

	t.Run("reference with required fail is blocked", func(t *testing.T) {
		specimen := qSpecimen("fixture/reference-fail", "", model.ReuseReference)
		specimen.Source.Revision = ""
		evidence := concat(qRelevance(specimen.ID),
			qBehaviour(specimen.ID, contract, map[string]model.EvidenceResult{"bounds-stdout": model.EvidenceFail}))
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseReference, evidence)}, permissivePolicy()))
		if got := findAssessment(t, outcome, specimen.ID).Disposition; got != Blocked {
			t.Errorf("disposition = %q", got)
		}
	})

	t.Run("reference without discovery relevance is blocked", func(t *testing.T) {
		specimen := qSpecimen("fixture/no-relevance", "", model.ReuseReference)
		specimen.Source.Revision = ""
		outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseReference)}, permissivePolicy()))
		assessment := findAssessment(t, outcome, specimen.ID)
		if assessment.Disposition != Blocked {
			t.Fatalf("disposition = %q", assessment.Disposition)
		}
	})

	t.Run("mode the specimen does not declare is blocked", func(t *testing.T) {
		// Bypass validateQualityInput by asserting through Assess directly.
		specimen := qSpecimen("fixture/no-mode", "v1.0.0")
		assessment := Assess(specimen, model.ReuseDependency, contract,
			CandidateEvaluation{SpecimenID: specimen.ID}, policy.Facts{}, permissivePolicy(), kernelResolvedAt)
		if assessment.Disposition != Blocked {
			t.Errorf("disposition = %q", assessment.Disposition)
		}
	})
}

func TestReviewDoesNotEraseACandidateFromTheShortlist(t *testing.T) {
	specimen := qSpecimen("fixture/review", "v1.0.0", model.ReuseDependency)
	pol := permissivePolicy()
	pol.Security.KnownAdvisory = policy.ActionReview
	evidence := concat(
		qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract())),
		qEvidence(specimen.ID, policy.KindKnownAdvisory, model.EvidenceInfo, "GHSA-1111-2222-3333"),
	)

	outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, pol))
	if len(outcome.Decision.Shortlist) != 1 {
		t.Fatalf("shortlist = %+v, want the reviewed candidate", outcome.Decision.Shortlist)
	}
	if outcome.Decision.Shortlist[0].Disposition != NeedsReview {
		t.Errorf("disposition = %q", outcome.Decision.Shortlist[0].Disposition)
	}
	// Review is inspectable but never an automatic selection.
	if outcome.Decision.Selected != nil {
		t.Error("a policy review must not be selected automatically")
	}
}

func TestAssessmentCarriesNoScoreField(t *testing.T) {
	specimen := qSpecimen("fixture/eligible", "v1.0.0", model.ReuseDependency)
	candidate := option(specimen, model.ReuseDependency,
		qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract())))
	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, permissivePolicy()))

	for _, assessment := range outcome.Decision.Assessments {
		payload, err := json.Marshal(assessment)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, banned := range []string{"score", "confidence", "weight", "rank", "quality_score"} {
			if strings.Contains(string(payload), `"`+banned+`"`) {
				t.Errorf("assessment exposes %q: %s", banned, payload)
			}
		}
	}
}

func TestOrderingIsDeterministicAcrossRuns(t *testing.T) {
	candidates := make([]CandidateOption, 0, 4)
	for _, name := range []string{"d", "b", "a", "c"} {
		specimen := qSpecimen("fixture/"+name, "v1.0.0", model.ReuseDependency)
		candidates = append(candidates, option(specimen, model.ReuseDependency,
			qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract()))))
	}

	first := decideOrFail(t, qInput(candidates, permissivePolicy()))
	second := decideOrFail(t, qInput(candidates, permissivePolicy()))
	if !reflect.DeepEqual(first.Decision.Assessments, second.Decision.Assessments) {
		t.Fatalf("ordering is not deterministic:\n%+v\n%+v", first.Decision.Assessments, second.Decision.Assessments)
	}
	// Deterministic does not mean sorted by ID first: claim strength leads.
	if first.Decision.Assessments[0].SpecimenID != "fixture/a" {
		t.Errorf("first = %q", first.Decision.Assessments[0].SpecimenID)
	}
}

// ---- §66 fixture direct-use regression ---------------------------------

func TestFixtureDirectUseFixtureStillProducesDepend(t *testing.T) {
	// A candidate whose required requirements all carry PASS evidence, under a
	// policy that allows its facts, must remain capable of DEPEND. Packet 7
	// must not make direct selection impossible.
	specimen := qSpecimen("fixture/complete-dependency", "v1.0.0", model.ReuseDependency)
	candidate := option(specimen, model.ReuseDependency,
		qBehaviour(specimen.ID, kernelContract(), qAllPass(specimen.ID, kernelContract())),
		qEvidence(specimen.ID, policy.KindSourceLicense, model.EvidenceInfo, "MIT"),
		qEvidence(specimen.ID, policy.KindKnownAdvisoryCount, model.EvidenceInfo, 0),
		qEvidence(specimen.ID, policy.KindDirectDependencyCount, model.EvidenceInfo, 2),
	)

	outcome := decideOrFail(t, qInput([]CandidateOption{candidate}, permissivePolicy()))
	if outcome.Decision.Resolution == nil || outcome.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Fatalf("resolution = %+v, want DEPEND", outcome.Decision.Resolution)
	}
}

// ---- §65 metadata is never behavioural fit ------------------------------

func TestMetadataNeverProducesDepend(t *testing.T) {
	// A package with only discovery and metadata evidence — no behavioural
	// PASS at all — must never come back as DEPEND.
	specimen := qSpecimen("fixture/metadata-only", "v1.0.0", model.ReuseDependency)
	evidence := concat(
		qEvidence(specimen.ID, policy.KindSourceLicense, model.EvidenceInfo, "MIT"),
		qEvidence(specimen.ID, policy.KindKnownAdvisoryCount, model.EvidenceInfo, 0),
		qEvidence(specimen.ID, policy.KindDirectDependencyCount, model.EvidenceInfo, 2),
		qEvidence(specimen.ID, policy.KindRepositoryArchived, model.EvidenceInfo, false),
		qRelevance(specimen.ID),
	)

	outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseDependency, evidence)}, permissivePolicy()))
	if outcome.Decision.Status == StatusResolved {
		t.Fatalf("status = %q, want needs_verification", outcome.Decision.Status)
	}
	if outcome.Decision.Resolution != nil {
		t.Fatalf("resolution = %+v, want nil", outcome.Decision.Resolution)
	}
	if findAssessment(t, outcome, specimen.ID).Disposition != NeedsVerification {
		t.Errorf("disposition = %q", findAssessment(t, outcome, specimen.ID).Disposition)
	}
}

// ---- §58 / §81 next-best-fit -------------------------------------------

func TestNextBestFitFeedbackChangesTheConstraintsNotTheQueue(t *testing.T) {
	contract := kernelContract()
	heavy := qSpecimen("fixture/a-heavy", "v1.0.0", model.ReuseDependency)
	light := qSpecimen("fixture/b-light", "v1.0.0", model.ReuseDependency)
	reference := qSpecimen("fixture/c-reference", "", model.ReuseReference)
	reference.Source.Revision = ""

	heavyFacts := func(count int) []model.Evidence {
		return qEvidence(heavy.ID, policy.KindDirectDependencyCount, model.EvidenceInfo, count)
	}
	candidates := []CandidateOption{
		option(heavy, model.ReuseDependency,
			qBehaviour(heavy.ID, contract, qAllPass(heavy.ID, contract)), heavyFacts(8)),
		option(light, model.ReuseDependency,
			qBehaviour(light.ID, contract, qAllPass(light.ID, contract)),
			qEvidence(light.ID, policy.KindDirectDependencyCount, model.EvidenceInfo, 2)),
		option(reference, model.ReuseReference, qRelevance(reference.ID)),
	}

	// Initial policy: no dependency maximum.
	pol := permissivePolicy()
	pol.Dependencies.MaxDirect = nil

	initial := decideOrFail(t, qInput(candidates, pol))
	if initial.Decision.Resolution == nil {
		t.Fatalf("expected a resolution: %+v", initial.Decision)
	}
	if initial.Decision.Selected.SpecimenID != heavy.ID {
		t.Fatalf("initial selection = %q, want the 8-dependency candidate", initial.Decision.Selected.SpecimenID)
	}

	// Feedback: too_many_dependencies on the heavy candidate. Decide applies
	// it, so the exclusion and the refined constraint travel together.
	refinedInput := qInput(candidates, pol)
	refinedInput.Feedback = []policy.Feedback{{CandidateID: heavy.ID, Reason: policy.FeedbackTooManyDependencies}}
	final := decideOrFail(t, refinedInput)

	if final.EffectivePolicy.Dependencies.MaxDirect == nil || *final.EffectivePolicy.Dependencies.MaxDirect != 7 {
		t.Fatalf("effective max_direct = %v, want 7", final.EffectivePolicy.Dependencies.MaxDirect)
	}
	if len(final.AppliedFeedback) != 1 || final.AppliedFeedback[0].Refinement != "dependencies.max_direct set to 7" {
		t.Fatalf("applied feedback = %+v", final.AppliedFeedback)
	}
	if final.Decision.Resolution == nil {
		t.Fatalf("expected a resolution: %+v", final.Decision)
	}
	if final.Decision.Selected.SpecimenID == heavy.ID {
		t.Fatal("the rejected candidate is still selected: feedback behaved like pagination")
	}
	if final.Decision.Selected.SpecimenID != light.ID {
		t.Errorf("final selection = %q, want the 2-dependency candidate", final.Decision.Selected.SpecimenID)
	}

	// The excluded candidate must carry its negative knowledge if a resolution
	// is produced.
	excluded := false
	for _, rejection := range final.Decision.Resolution.Rejected {
		if rejection.SpecimenID == heavy.ID {
			excluded = true
			if !strings.Contains(strings.Join(rejection.Reasons, " "), "user_feedback:too_many_dependencies") {
				t.Errorf("rejection reasons = %v", rejection.Reasons)
			}
		}
	}
	if !excluded {
		t.Error("the feedback-rejected candidate is missing from Resolution.Rejected")
	}
}

// ---- validation --------------------------------------------------------

func TestQualityInputValidationNeverManufacturesADecision(t *testing.T) {
	specimen := qSpecimen("fixture/a", "v1.0.0", model.ReuseDependency)
	valid := qInput([]CandidateOption{option(specimen, model.ReuseDependency)}, permissivePolicy())

	cases := map[string]func(*QualityInput){
		"empty primitive":    func(in *QualityInput) { in.Primitive.ID = "" },
		"empty contract":     func(in *QualityInput) { in.Contract.ID = "" },
		"contract mismatch":  func(in *QualityInput) { in.Primitive.ContractID = "other" },
		"primitive mismatch": func(in *QualityInput) { in.Contract.PrimitiveID = "other" },
		"zero clock":         func(in *QualityInput) { in.Now = time.Time{} },
		"empty specimen id":  func(in *QualityInput) { in.Candidates[0].Specimen.ID = "" },
		"wrong primitive":    func(in *QualityInput) { in.Candidates[0].Specimen.PrimitiveID = "other" },
		"unsupported mode":   func(in *QualityInput) { in.Candidates[0].ReuseMode = "vendor" },
		"undeclared mode": func(in *QualityInput) {
			in.Candidates[0].Specimen.ReuseMode = []model.ReuseMode{model.ReuseReference}
		},
		"invalid policy": func(in *QualityInput) { in.Policy.Selection.MaxOptions = 99 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input := valid
			input.Candidates = append([]CandidateOption(nil), valid.Candidates...)
			input.Candidates[0].Specimen = valid.Candidates[0].Specimen
			mutate(&input)
			if _, err := Decide(input); err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	t.Run("duplicate candidate", func(t *testing.T) {
		input := valid
		input.Candidates = []CandidateOption{
			option(specimen, model.ReuseDependency),
			option(specimen, model.ReuseDependency),
		}
		if _, err := Decide(input); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestFeedbackErrorPathsReachTheCallerUnchanged(t *testing.T) {
	specimen := qSpecimen("fixture/a", "v1.0.0", model.ReuseDependency)
	input := qInput([]CandidateOption{option(specimen, model.ReuseDependency)}, permissivePolicy())
	input.Feedback = []policy.Feedback{{CandidateID: "ghost", Reason: policy.FeedbackNotQuite}}

	if _, err := Decide(input); err == nil || !strings.Contains(err.Error(), "not in the request") {
		t.Errorf("err = %v", err)
	}
}

// ---- §74 reference unknown behaviour preserved --------------------------

func TestReferenceResolutionRetainsUnresolvedRequiredRequirements(t *testing.T) {
	contract := kernelContract()
	specimen := qSpecimen("fixture/reference", "", model.ReuseReference)
	specimen.Source.Revision = ""
	evidence := concat(qRelevance(specimen.ID),
		[]model.Evidence{evidenceFor(specimen.ID, "bounds-stdout", "ev/"+specimen.ID+"/req/bounds-stdout", model.EvidencePass)})

	outcome := decideOrFail(t, qInput([]CandidateOption{option(specimen, model.ReuseReference, evidence)}, permissivePolicy()))
	resolution := outcome.Decision.Resolution
	if resolution.Outcome != model.OutcomeReference {
		t.Fatalf("outcome = %q", resolution.Outcome)
	}
	if len(resolution.Unknowns) != 2 {
		t.Errorf("unknowns = %v, want the two unresolved required requirements", resolution.Unknowns)
	}
	for _, requirement := range contract.Requirements {
		if requirement.ID == "bounds-stdout" {
			continue
		}
		found := false
		for _, unknown := range resolution.Unknowns {
			if strings.Contains(unknown, requirement.ID) {
				found = true
			}
		}
		if !found {
			t.Errorf("unknowns %v are missing %q", resolution.Unknowns, requirement.ID)
		}
	}
}
