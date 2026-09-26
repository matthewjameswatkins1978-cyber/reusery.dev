package policy

import (
	"errors"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

func feedbackFacts(dependencyCount int, dependencyKnown bool, licence string, licenceStatus FactStatus, archived bool) Facts {
	return Facts{
		Dependency: DependencyFact{
			Status:           FactKnown,
			DirectCount:      dependencyCount,
			DirectCountKnown: dependencyKnown,
		},
		Licence: LicenceFact{
			Status: licenceStatus,
			Values: func() []string {
				if licence == "" {
					return nil
				}
				return []string{licence}
			}(),
		},
		Archived: ArchivedFact{
			Status:   FactKnown,
			Known:    true,
			Archived: archived,
		},
	}
}

func TestSupportedFeedbackVocabularyIsExactlyWhatThisPacketCanActOn(t *testing.T) {
	want := []FeedbackReason{
		FeedbackNotQuite,
		FeedbackTooManyDependencies,
		FeedbackLicenceNotAllowed,
		FeedbackAvoidDependency,
		FeedbackAvoidReference,
		FeedbackArchivedProject,
	}
	got := FeedbackReasons()
	if len(got) != len(want) {
		t.Fatalf("reasons = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("reasons[%d] = %q, want %q", index, got[index], want[index])
		}
		if !SupportedFeedback(got[index]) {
			t.Errorf("%q must be supported", got[index])
		}
	}
	for _, unsupported := range []FeedbackReason{"high_integration_cost", "wrong_architectural_style", "prefer_stdlib", "too_much_framework", "best"} {
		if SupportedFeedback(unsupported) {
			t.Errorf("%q must not be advertised: it cannot change behaviour yet", unsupported)
		}
	}
}

func TestUnknownCandidateIsAStructuredError(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(4, true, "MIT", FactKnown, false)}
	_, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "ghost", Reason: FeedbackNotQuite}})
	if !errors.Is(err, ErrUnknownFeedbackCandidate) {
		t.Errorf("err = %v, want ErrUnknownFeedbackCandidate", err)
	}
}

func TestUnsupportedReasonIsRejected(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(4, true, "MIT", FactKnown, false)}
	_, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: "prefer_stdlib"}})
	if !errors.Is(err, ErrUnsupportedFeedback) {
		t.Errorf("err = %v, want ErrUnsupportedFeedback", err)
	}
}

func TestNotQuiteOnlyExcludes(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(4, true, "MIT", FactKnown, false)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackNotQuite}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if outcome.Policy.Dependencies.MaxDirect != nil {
		t.Error("not_quite must not infer a dependency rule")
	}
	if len(outcome.Policy.Licence.Deny) != 0 {
		t.Error("not_quite must not infer a licence rule")
	}
	if len(outcome.Excluded) != 1 || outcome.Excluded[0].CandidateID != "a" {
		t.Errorf("excluded = %+v", outcome.Excluded)
	}
	if outcome.Excluded[0].RejectionReason() != "user_feedback:not_quite" {
		t.Errorf("rejection reason = %q", outcome.Excluded[0].RejectionReason())
	}
	if outcome.Applied[0].Refinement != "" {
		t.Errorf("refinement = %q, want empty", outcome.Applied[0].Refinement)
	}
}

func TestTooManyDependenciesRefinesTheMaximum(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(12, true, "MIT", FactKnown, false)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackTooManyDependencies}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if outcome.Policy.Dependencies.MaxDirect == nil || *outcome.Policy.Dependencies.MaxDirect != 11 {
		t.Errorf("max_direct = %v, want 11", outcome.Policy.Dependencies.MaxDirect)
	}
	if outcome.Applied[0].Refinement != "dependencies.max_direct set to 11" {
		t.Errorf("refinement = %q", outcome.Applied[0].Refinement)
	}
	if len(outcome.Excluded) != 1 {
		t.Errorf("excluded = %+v", outcome.Excluded)
	}
}

func TestTooManyDependenciesKeepsAnExistingStricterMaximum(t *testing.T) {
	policy := basePolicy()
	stricter := 5
	policy.Dependencies.MaxDirect = &stricter

	known := map[string]Facts{"a": feedbackFacts(12, true, "MIT", FactKnown, false)}
	outcome, err := ApplyFeedback(policy, known, []Feedback{{CandidateID: "a", Reason: FeedbackTooManyDependencies}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if *outcome.Policy.Dependencies.MaxDirect != 5 {
		t.Errorf("max_direct = %d, want the stricter existing 5", *outcome.Policy.Dependencies.MaxDirect)
	}
}

func TestTooManyDependenciesErrorPaths(t *testing.T) {
	t.Run("unknown count", func(t *testing.T) {
		known := map[string]Facts{"a": feedbackFacts(0, false, "", FactUnknown, false)}
		_, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackTooManyDependencies}})
		if !errors.Is(err, ErrDependencyCountUnknown) {
			t.Errorf("err = %v, want ErrDependencyCountUnknown", err)
		}
	})

	t.Run("zero count", func(t *testing.T) {
		known := map[string]Facts{"a": feedbackFacts(0, true, "", FactUnknown, false)}
		_, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackTooManyDependencies}})
		if !errors.Is(err, ErrDependencyCountZero) {
			t.Errorf("err = %v, want ErrDependencyCountZero", err)
		}
	})
}

func TestLicenceNotAllowedDeniesTheExactKnownExpression(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(1, true, "GPL-3.0", FactKnown, false)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackLicenceNotAllowed}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if len(outcome.Policy.Licence.Deny) != 1 || outcome.Policy.Licence.Deny[0] != "GPL-3.0" {
		t.Errorf("deny list = %v", outcome.Policy.Licence.Deny)
	}
	if outcome.Applied[0].Refinement != `licence "GPL-3.0" added to the deny list` {
		t.Errorf("refinement = %q", outcome.Applied[0].Refinement)
	}
	if outcome.Applied[0].Warning != "" {
		t.Errorf("warning = %q, want none", outcome.Applied[0].Warning)
	}
}

func TestLicenceNotAllowedWithUnknownLicenceExcludesWithoutGeneralising(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(1, true, "", FactUnknown, false)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackLicenceNotAllowed}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if len(outcome.Policy.Licence.Deny) != 0 {
		t.Errorf("deny list = %v, want no generalisation", outcome.Policy.Licence.Deny)
	}
	if outcome.Applied[0].Warning == "" {
		t.Error("the inability to refine must be reported, not hidden")
	}
	if len(outcome.Excluded) != 1 {
		t.Errorf("the candidate must still be excluded: %+v", outcome.Excluded)
	}
}

func TestLicenceNotAllowedWithMultipleLicencesDoesNotGeneralise(t *testing.T) {
	facts := feedbackFacts(1, true, "", FactKnown, false)
	facts.Licence = LicenceFact{Status: FactMultiple, Values: []string{"MIT", "Apache-2.0"}}
	known := map[string]Facts{"a": facts}

	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackLicenceNotAllowed}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if len(outcome.Policy.Licence.Deny) != 0 {
		t.Errorf("deny list = %v", outcome.Policy.Licence.Deny)
	}
	if outcome.Applied[0].Warning == "" {
		t.Error("a multiple-licence candidate must report that no broader rule could be derived")
	}
}

func TestAvoidDependencyAndAvoidReferenceRemoveOnlyTheirMode(t *testing.T) {
	known := map[string]Facts{
		"a": feedbackFacts(1, true, "MIT", FactKnown, false),
		"b": feedbackFacts(1, true, "MIT", FactKnown, false),
	}

	withoutDependency, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackAvoidDependency}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if withoutDependency.Policy.ModeAllowed(model.ReuseDependency) {
		t.Error("dependency must be removed from the allowed set")
	}
	for _, mode := range []model.ReuseMode{model.ReuseCopy, model.ReuseAdapt, model.ReuseReference} {
		if !withoutDependency.Policy.ModeAllowed(mode) {
			t.Errorf("%q must stay allowed", mode)
		}
	}

	withoutReference, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "b", Reason: FeedbackAvoidReference}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if withoutReference.Policy.ModeAllowed(model.ReuseReference) {
		t.Error("reference must be removed from the allowed set")
	}
	if !withoutReference.Policy.ModeAllowed(model.ReuseDependency) {
		t.Error("dependency must stay allowed")
	}
}

func TestAvoidModeWorksOnAnUnrestrictedPolicy(t *testing.T) {
	policy := basePolicy()
	policy.Reuse.Allowed = nil // no restriction at all

	outcome, err := ApplyFeedback(policy, map[string]Facts{
		"a": feedbackFacts(1, true, "MIT", FactKnown, false),
	}, []Feedback{{CandidateID: "a", Reason: FeedbackAvoidDependency}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if outcome.Policy.ModeAllowed(model.ReuseDependency) {
		t.Error("removing a mode from an unrestricted policy must still mean something")
	}
	for _, mode := range []model.ReuseMode{model.ReuseCopy, model.ReuseAdapt, model.ReuseReference} {
		if !outcome.Policy.ModeAllowed(mode) {
			t.Errorf("%q must stay allowed", mode)
		}
	}
}

func TestArchivedProjectRequiresAnArchivedObservation(t *testing.T) {
	archived := map[string]Facts{"a": feedbackFacts(1, true, "MIT", FactKnown, true)}
	outcome, err := ApplyFeedback(basePolicy(), archived, []Feedback{{CandidateID: "a", Reason: FeedbackArchivedProject}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if outcome.Policy.Maintenance.Archived != ActionDeny {
		t.Errorf("archived action = %q, want deny", outcome.Policy.Maintenance.Archived)
	}

	active := map[string]Facts{"a": feedbackFacts(1, true, "MIT", FactKnown, false)}
	if _, err := ApplyFeedback(basePolicy(), active, []Feedback{{CandidateID: "a", Reason: FeedbackArchivedProject}}); !errors.Is(err, ErrNotArchived) {
		t.Errorf("err = %v, want ErrNotArchived", err)
	}
}

func TestDuplicateFeedbackIsAppliedOnce(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(12, true, "GPL-3.0", FactKnown, false)}
	duplicate := []Feedback{
		{CandidateID: "a", Reason: FeedbackLicenceNotAllowed},
		{CandidateID: "a", Reason: FeedbackLicenceNotAllowed},
	}
	outcome, err := ApplyFeedback(basePolicy(), known, duplicate)
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if len(outcome.Applied) != 1 {
		t.Errorf("applied = %+v, want one entry", outcome.Applied)
	}
	if len(outcome.Excluded) != 1 {
		t.Errorf("excluded = %+v, want one entry", outcome.Excluded)
	}
	if len(outcome.Policy.Licence.Deny) != 1 {
		t.Errorf("deny list = %v", outcome.Policy.Licence.Deny)
	}
}

func TestDifferentFeedbackForTheSameCandidateBothApply(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(12, true, "GPL-3.0", FactKnown, true)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{
		{CandidateID: "a", Reason: FeedbackLicenceNotAllowed},
		{CandidateID: "a", Reason: FeedbackArchivedProject},
	})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if len(outcome.Excluded) != 2 {
		t.Errorf("excluded = %+v", outcome.Excluded)
	}
	if outcome.Policy.Dependencies.MaxDirect != nil {
		t.Error("no dependency rule was requested")
	}
	if len(outcome.Policy.Licence.Deny) != 1 || outcome.Policy.Maintenance.Archived != ActionDeny {
		t.Errorf("policy = %+v", outcome.Policy)
	}
}

func TestExclusionRejectionReasonCarriesFactualContext(t *testing.T) {
	known := map[string]Facts{"a": feedbackFacts(12, true, "GPL-3.0", FactKnown, false)}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{{CandidateID: "a", Reason: FeedbackTooManyDependencies}})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	reason := outcome.Excluded[0].RejectionReason()
	if reason != "user_feedback:too_many_dependencies: dependencies.max_direct set to 11" {
		t.Errorf("rejection reason = %q", reason)
	}
}

func TestFeedbackIsProcessedInAuthoredOrder(t *testing.T) {
	known := map[string]Facts{
		"a": feedbackFacts(12, true, "GPL-3.0", FactKnown, false),
		"b": feedbackFacts(3, true, "MIT", FactKnown, false),
	}
	outcome, err := ApplyFeedback(basePolicy(), known, []Feedback{
		{CandidateID: "b", Reason: FeedbackNotQuite},
		{CandidateID: "a", Reason: FeedbackTooManyDependencies},
	})
	if err != nil {
		t.Fatalf("ApplyFeedback: %v", err)
	}
	if outcome.Excluded[0].CandidateID != "b" || outcome.Excluded[1].CandidateID != "a" {
		t.Errorf("exclusion order = %+v", outcome.Excluded)
	}
}
