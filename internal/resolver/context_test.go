package resolver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

func marshalOutcome(t *testing.T, outcome QualityOutcome) string {
	t.Helper()
	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	return string(encoded)
}

// eligibleOption builds a behaviourally complete dependency candidate.
func eligibleOption(t *testing.T, id, revision, licence string) CandidateOption {
	t.Helper()
	specimen := qSpecimen(id, revision, model.ReuseDependency)
	specimen.Source.License = licence
	evidence := concat(
		qRelevance(id),
		qEvidence(id, "source_license", model.EvidenceInfo, licence),
		qEvidence(id, "known_advisory_count", model.EvidenceInfo, 0),
		qEvidence(id, "repository_archived", model.EvidenceInfo, false),
		qEvidence(id, "package_deprecated", model.EvidenceInfo, false),
		qEvidence(id, "source_revision", model.EvidenceInfo, revision),
		qBehaviour(id, kernelContract(), qAllPass(id, kernelContract())),
	)
	return option(specimen, model.ReuseDependency, evidence)
}

// unknownOption is plausible but has no behavioural evidence at all.
func unknownOption(t *testing.T, id, revision string) CandidateOption {
	t.Helper()
	specimen := qSpecimen(id, revision, model.ReuseDependency)
	specimen.Source.License = "MIT"
	evidence := concat(
		qRelevance(id),
		qEvidence(id, "source_license", model.EvidenceInfo, "MIT"),
		qEvidence(id, "known_advisory_count", model.EvidenceInfo, 0),
		qEvidence(id, "repository_archived", model.EvidenceInfo, false),
		qEvidence(id, "package_deprecated", model.EvidenceInfo, false),
		qEvidence(id, "source_revision", model.EvidenceInfo, revision),
	)
	return option(specimen, model.ReuseDependency, evidence)
}

// TestNoProjectContextIsByteForBytePacket7 is the standing rule: an absent
// project context, an empty context and a context of only "not applicable"
// entries must all produce exactly the Packet 7 decision.
func TestNoProjectContextIsByteForBytePacket7(t *testing.T) {
	candidates := []CandidateOption{
		eligibleOption(t, "fixture/zeta", "v1.0.0", "MIT"),
		eligibleOption(t, "fixture/alpha", "v1.0.0", "MIT"),
	}

	baseline := decideOrFail(t, qInput(candidates, permissivePolicy()))

	withEmpty := qInput(candidates, permissivePolicy())
	withEmpty.Context = map[string]CandidateContext{}
	empty := decideOrFail(t, withEmpty)

	withNeutral := qInput(candidates, permissivePolicy())
	withNeutral.Context = map[string]CandidateContext{
		"fixture/zeta":  {DependencyFit: FitNotApplicable},
		"fixture/alpha": {DependencyFit: FitNotApplicable},
	}
	neutral := decideOrFail(t, withNeutral)

	want := marshalOutcome(t, baseline)
	if got := marshalOutcome(t, empty); got != want {
		t.Errorf("an empty context changed the decision:\n%s\nvs\n%s", got, want)
	}
	if got := marshalOutcome(t, neutral); got != want {
		t.Errorf("a not_applicable context changed the decision:\n%s\nvs\n%s", got, want)
	}
}

// TestExistingExactBreaksOnlyAnOtherwiseEquivalentTie: without context the
// stable specimen id decides; with context the exact-present candidate wins,
// and the reason says so rather than implying it is better.
func TestExistingExactBreaksOnlyAnOtherwiseEquivalentTie(t *testing.T) {
	candidates := []CandidateOption{
		eligibleOption(t, "fixture/alpha", "v1.0.0", "MIT"),
		eligibleOption(t, "fixture/zeta", "v1.0.0", "MIT"),
	}

	baseline := decideOrFail(t, qInput(candidates, permissivePolicy()))
	if baseline.Decision.Selected == nil || baseline.Decision.Selected.SpecimenID != "fixture/alpha" {
		t.Fatalf("baseline selection = %+v, want fixture/alpha by stable id", baseline.Decision.Selected)
	}
	if !strings.Contains(strings.Join(baseline.Decision.Resolution.Reasons, "\n"), "stable specimen ID") {
		t.Errorf("baseline reasons = %v", baseline.Decision.Resolution.Reasons)
	}

	withContext := qInput(candidates, permissivePolicy())
	withContext.ProjectID = "project/go/abc"
	withContext.Context = map[string]CandidateContext{
		"fixture/alpha": {DependencyFit: FitNewDependency},
		"fixture/zeta": {
			DependencyFit: FitExistingExact,
			ModulePath:    "example.com/zeta",
			ModuleVersion: "v1.0.0",
		},
	}
	decided := decideOrFail(t, withContext)

	if decided.Decision.Selected == nil || decided.Decision.Selected.SpecimenID != "fixture/zeta" {
		t.Fatalf("selection = %+v, want fixture/zeta via the exact-present tie-break", decided.Decision.Selected)
	}
	reasons := strings.Join(decided.Decision.Resolution.Reasons, "\n")
	if !strings.Contains(reasons, "because its exact module version is already present in project project/go/abc") {
		t.Errorf("project tie-break reason missing: %v", decided.Decision.Resolution.Reasons)
	}
	for _, forbidden := range []string{"better", "higher quality", "more secure"} {
		if strings.Contains(reasons, forbidden) {
			t.Errorf("tie-break reason used %q: %s", forbidden, reasons)
		}
	}
}

// TestExistingExactCannotOutrankPolicyEligibility proves the tie-break is
// late: a policy-blocked candidate never wins on project fit alone.
func TestExistingExactCannotOutrankPolicyEligibility(t *testing.T) {
	blocked := eligibleOption(t, "fixture/aaa", "v1.0.0", "GPL-3.0")
	allowed := eligibleOption(t, "fixture/zzz", "v1.0.0", "MIT")
	candidates := []CandidateOption{blocked, allowed}

	pol := permissivePolicy()
	pol.Licence.Deny = []string{"GPL-3.0"}

	in := qInput(candidates, pol)
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/aaa": {DependencyFit: FitExistingExact, ModulePath: "example.com/aaa", ModuleVersion: "v1.0.0"},
		"fixture/zzz": {DependencyFit: FitNewDependency},
	}
	decided := decideOrFail(t, in)

	if decided.Decision.Selected == nil || decided.Decision.Selected.SpecimenID != "fixture/zzz" {
		t.Fatalf("selection = %+v, want the policy-eligible candidate", decided.Decision.Selected)
	}
	if findAssessment(t, decided, "fixture/aaa").Disposition != Blocked {
		t.Errorf("an exact-present candidate bypassed a policy deny")
	}
}

// TestExistingExactCannotConvertUnknownBehaviourToSatisfied is the critical
// invariant: project context can never upgrade uncertainty.
func TestExistingExactCannotConvertUnknownBehaviourToSatisfied(t *testing.T) {
	candidates := []CandidateOption{unknownOption(t, "fixture/unknown", "v1.0.0")}

	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/unknown": {DependencyFit: FitExistingExact, ModulePath: "example.com/unknown", ModuleVersion: "v1.0.0"},
	}
	decided := decideOrFail(t, in)

	if decided.Decision.Status != StatusNeedsVerification {
		t.Fatalf("status = %q, want needs_verification", decided.Decision.Status)
	}
	if decided.Decision.Resolution != nil {
		t.Fatalf("project context persisted a resolution: %+v", decided.Decision.Resolution)
	}
	assessment := findAssessment(t, decided, "fixture/unknown")
	for _, requirement := range assessment.Behaviour.Requirements {
		if requirement.Status == RequirementSatisfied {
			t.Errorf("requirement %s was upgraded to satisfied", requirement.RequirementID)
		}
	}
	if assessment.Disposition != NeedsVerification {
		t.Errorf("disposition = %q, want needs_verification", assessment.Disposition)
	}
}

// TestVersionChangeRequiresReview: an otherwise eligible dependency with a
// different required version becomes needs_review rather than a silent
// upgrade.
func TestVersionChangeRequiresReview(t *testing.T) {
	candidates := []CandidateOption{eligibleOption(t, "fixture/needsreview", "v1.6.0", "MIT")}

	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/needsreview": {
			DependencyFit: FitExistingVersionChange,
			ModulePath:    "example.com/needsreview",
			ModuleVersion: "v1.4.0",
		},
	}
	decided := decideOrFail(t, in)

	assessment := findAssessment(t, decided, "fixture/needsreview")
	if assessment.Disposition != NeedsReview {
		t.Fatalf("disposition = %q, want needs_review", assessment.Disposition)
	}
	if decided.Decision.Status != StatusNeedsVerification || decided.Decision.Resolution != nil {
		t.Errorf("a review-blocked candidate must not resolve: %+v", decided.Decision)
	}
	assertProjectTradeoff(t, assessment,
		"project requires example.com/needsreview at v1.4.0; candidate is v1.6.0; version change requires review")
}

// TestReplacedDirectiveRequiresReview: a Go replace means upstream metadata
// may not describe what the project actually depends on.
func TestReplacedDirectiveRequiresReview(t *testing.T) {
	candidates := []CandidateOption{eligibleOption(t, "fixture/replaced", "v1.0.0", "MIT")}

	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/replaced": {
			DependencyFit: FitExistingReplaced,
			ModulePath:    "example.com/replaced",
			ModuleVersion: "v1.0.0",
		},
	}
	decided := decideOrFail(t, in)

	assessment := findAssessment(t, decided, "fixture/replaced")
	if assessment.Disposition != NeedsReview {
		t.Fatalf("disposition = %q, want needs_review", assessment.Disposition)
	}
	assertProjectTradeoff(t, assessment,
		"project replaces example.com/replaced; dependency semantics require review")
}

// TestUnknownBehaviourSurvivesVersionChangeContext proves project context
// never upgrades uncertainty into review or approval.
func TestUnknownBehaviourSurvivesVersionChangeContext(t *testing.T) {
	candidates := []CandidateOption{unknownOption(t, "fixture/staysunknown", "v1.6.0")}

	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/staysunknown": {
			DependencyFit: FitExistingVersionChange,
			ModulePath:    "example.com/staysunknown",
			ModuleVersion: "v1.4.0",
		},
	}
	decided := decideOrFail(t, in)

	assessment := findAssessment(t, decided, "fixture/staysunknown")
	if assessment.Disposition != NeedsVerification {
		t.Errorf("disposition = %q, want needs_verification", assessment.Disposition)
	}
	if decided.Decision.Status != StatusNeedsVerification {
		t.Errorf("status = %q, want needs_verification", decided.Decision.Status)
	}
}

// TestProjectTradeoffTextIsDeterministic runs the same decision twice and
// requires byte-identical output.
func TestProjectTradeoffTextIsDeterministic(t *testing.T) {
	candidates := []CandidateOption{eligibleOption(t, "fixture/deterministic", "v1.6.0", "MIT")}

	run := func() string {
		in := qInput(candidates, permissivePolicy())
		in.ProjectID = "project/go/abc"
		in.Context = map[string]CandidateContext{
			"fixture/deterministic": {
				DependencyFit: FitExistingVersionChange,
				ModulePath:    "example.com/deterministic",
				ModuleVersion: "v1.4.0",
			},
		}
		return marshalOutcome(t, decideOrFail(t, in))
	}
	first := run()
	second := run()
	if first != second {
		t.Error("project-aware decisions are not deterministic")
	}
}

// TestExactPresentTradeoffIsAPro records the positive integration fact
// without attaching an Evidence id: it is a project fact, not provider
// evidence.
func TestExactPresentTradeoffIsAPro(t *testing.T) {
	candidates := []CandidateOption{eligibleOption(t, "fixture/pro", "v1.0.0", "MIT")}

	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/pro": {DependencyFit: FitExistingExact, ModulePath: "example.com/pro", ModuleVersion: "v1.0.0"},
	}
	decided := decideOrFail(t, in)
	assessment := findAssessment(t, decided, "fixture/pro")

	assertProjectTradeoff(t, assessment,
		"exact candidate module version is already present in the project manifest")
	for _, tradeoff := range append(append([]Tradeoff{}, assessment.Pros...), assessment.Cons...) {
		if tradeoff.Dimension == DimensionProjectDependency && len(tradeoff.EvidenceIDs) != 0 {
			t.Errorf("project trade-off carries Evidence ids: %+v", tradeoff)
		}
	}
}

// TestProjectContextIntroducesNoScore keeps the standing rule: no numeric
// quality score exists anywhere in a decision.
func TestProjectContextIntroducesNoScore(t *testing.T) {
	candidates := []CandidateOption{
		eligibleOption(t, "fixture/score-a", "v1.0.0", "MIT"),
		eligibleOption(t, "fixture/score-b", "v1.6.0", "MIT"),
	}
	in := qInput(candidates, permissivePolicy())
	in.ProjectID = "project/go/abc"
	in.Context = map[string]CandidateContext{
		"fixture/score-a": {DependencyFit: FitExistingExact, ModulePath: "example.com/a", ModuleVersion: "v1.0.0"},
		"fixture/score-b": {DependencyFit: FitExistingVersionChange, ModulePath: "example.com/b", ModuleVersion: "v1.4.0"},
	}
	encoded := marshalOutcome(t, decideOrFail(t, in))
	for _, banned := range []string{"quality_score", "confidence", "overall_score", `"score"`} {
		if strings.Contains(encoded, banned) {
			t.Errorf("decision contains %q: %s", banned, encoded)
		}
	}
}

func assertProjectTradeoff(t *testing.T, assessment CandidateAssessment, want string) {
	t.Helper()
	for _, cons := range assessment.Cons {
		if cons.Dimension == DimensionProjectDependency && cons.Message == want {
			return
		}
	}
	for _, pro := range assessment.Pros {
		if pro.Dimension == DimensionProjectDependency && pro.Message == want {
			return
		}
	}
	t.Errorf("no project trade-off with message %q\ncons: %v\npros: %v", want, assessment.Cons, assessment.Pros)
}
