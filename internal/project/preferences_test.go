package project

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

func factsFor(values map[string]any) policy.Facts {
	// Build facts the way the extractor would: from observations with
	// machine-readable artifacts.
	specimen := model.Specimen{ID: "fixture/candidate", Source: model.SourceRef{URL: "https://example.invalid/c"}}
	var evidence []model.Evidence
	for kind, value := range values {
		evidence = append(evidence, model.Evidence{
			ID:         "ev/" + kind,
			SubjectID:  specimen.ID,
			Kind:       kind,
			Result:     model.EvidenceInfo,
			Artifact:   factArtifact(value),
			ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		})
	}
	return policy.ExtractFacts(specimen, evidence)
}

func TestDeriveNotQuiteExcludesOnlyThatCandidate(t *testing.T) {
	facts := factsFor(map[string]any{"source_license": "MIT"})
	preference, err := DerivePreference("project/go/x", "process/bounded-subprocess",
		"fixture/candidate", policy.FeedbackNotQuite, facts, 7, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefExcludeCandidate {
		t.Errorf("kind = %q, want exclude_candidate", preference.Kind)
	}
	if preference.PrimitiveID != "process/bounded-subprocess" || preference.CandidateID != "fixture/candidate" {
		t.Errorf("scope = %+v", preference)
	}
	if preference.SourceReason != policy.FeedbackNotQuite || preference.SourceResolutionID != 7 {
		t.Errorf("provenance = %+v", preference)
	}
	if preference.TextValue != "" || preference.IntValue != nil {
		t.Errorf("a poor fit must not become a broader rule: %+v", preference)
	}
}

func TestDeriveTooManyDependenciesUsesTheKnownCount(t *testing.T) {
	facts := factsFor(map[string]any{"direct_dependency_count": 8})
	preference, err := DerivePreference("p", "pr", "c", policy.FeedbackTooManyDependencies, facts, 0, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefMaxDirectDeps || preference.IntValue == nil || *preference.IntValue != 7 {
		t.Errorf("preference = %+v, want max_direct_dependencies = 7", preference)
	}
}

func TestDeriveTooManyDependenciesRejectsUnknownCount(t *testing.T) {
	facts := factsFor(map[string]any{})
	if _, err := DerivePreference("p", "pr", "c", policy.FeedbackTooManyDependencies, facts, 0, time.Now()); err == nil {
		t.Fatal("an unknown dependency count must not produce a memory")
	}
}

func TestDeriveTooManyDependenciesRejectsZeroCount(t *testing.T) {
	facts := factsFor(map[string]any{"direct_dependency_count": 0})
	_, err := DerivePreference("p", "pr", "c", policy.FeedbackTooManyDependencies, facts, 0, time.Now())
	if err == nil {
		t.Fatal("a zero dependency count cannot produce max_direct = -1")
	}
}

func TestDeriveLicenceUsesTheEstablishedExpression(t *testing.T) {
	facts := factsFor(map[string]any{"source_license": "Apache-2.0"})
	preference, err := DerivePreference("p", "pr", "c", policy.FeedbackLicenceNotAllowed, facts, 0, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefDenyLicence || preference.TextValue != "Apache-2.0" {
		t.Errorf("preference = %+v", preference)
	}
}

func TestDeriveLicenceFallsBackToCandidateExclusion(t *testing.T) {
	unknown := factsFor(map[string]any{})
	preference, err := DerivePreference("p", "pr", "c", policy.FeedbackLicenceNotAllowed, unknown, 0, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefExcludeCandidate || preference.TextValue != "" {
		t.Errorf("an unknown licence must not become a general rule: %+v", preference)
	}

	multiple := factsWithMultipleLicences(t)
	preference, err = DerivePreference("p", "pr", "c", policy.FeedbackLicenceNotAllowed, multiple, 0, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefExcludeCandidate {
		t.Errorf("a multiple/conflicting licence must not become a general rule: %+v", preference)
	}
}

func factsWithMultipleLicences(t *testing.T) policy.Facts {
	t.Helper()
	specimen := model.Specimen{ID: "fixture/candidate", Source: model.SourceRef{URL: "https://example.invalid/c"}}
	evidence := []model.Evidence{
		{ID: "ev/1", SubjectID: specimen.ID, Kind: "source_license", Result: model.EvidenceInfo, Artifact: factArtifact("MIT")},
		{ID: "ev/2", SubjectID: specimen.ID, Kind: "source_license", Result: model.EvidenceInfo, Artifact: factArtifact("Apache-2.0")},
		{ID: "ev/3", SubjectID: specimen.ID, Kind: "licence_relationship", Result: model.EvidenceUnknown},
	}
	return policy.ExtractFacts(specimen, evidence)
}

func TestDeriveAvoidModesAndArchived(t *testing.T) {
	for reason, want := range map[policy.FeedbackReason]PreferenceKind{
		policy.FeedbackAvoidDependency: PrefAvoidDependency,
		policy.FeedbackAvoidReference:  PrefAvoidReference,
	} {
		preference, err := DerivePreference("p", "pr", "c", reason, factsFor(map[string]any{}), 0, time.Now())
		if err != nil {
			t.Fatalf("derive %s: %v", reason, err)
		}
		if preference.Kind != want {
			t.Errorf("%s -> %q, want %q", reason, preference.Kind, want)
		}
	}
}

func TestDeriveArchivedRequiresAnArchivedObservation(t *testing.T) {
	_, err := DerivePreference("p", "pr", "c", policy.FeedbackArchivedProject,
		factsFor(map[string]any{"repository_archived": false}), 0, time.Now())
	if err == nil {
		t.Fatal("a non-archived candidate must not produce deny_archived")
	}
	_, err = DerivePreference("p", "pr", "c", policy.FeedbackArchivedProject,
		factsFor(map[string]any{}), 0, time.Now())
	if err == nil {
		t.Fatal("an unknown archive state must not produce deny_archived")
	}
	preference, err := DerivePreference("p", "pr", "c", policy.FeedbackArchivedProject,
		factsFor(map[string]any{"repository_archived": true}), 0, time.Now())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if preference.Kind != PrefDenyArchived {
		t.Errorf("kind = %q, want deny_archived", preference.Kind)
	}
}

func TestDeriveRejectsUnknownReason(t *testing.T) {
	if _, err := DerivePreference("p", "pr", "c", policy.FeedbackReason("prefer_stdlib"),
		factsFor(map[string]any{}), 0, time.Now()); err == nil {
		t.Fatal("an invented reason must be rejected")
	}
}

// ---------------------------------------------------------------- overlay

func basePolicy() policy.Policy {
	return policy.Policy{
		SchemaVersion: 1,
		ID:            "test/base",
		Reuse: policy.ReusePolicy{
			Allowed: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseReference},
		},
		Licence:      policy.LicencePolicy{Unknown: policy.ActionReview, Multiple: policy.ActionReview, Unlisted: policy.ActionAllow},
		Security:     policy.SecurityPolicy{KnownAdvisory: policy.ActionReview, Unknown: policy.ActionReview},
		Dependencies: policy.DependencyPolicy{Unknown: policy.ActionReview},
		Maintenance:  policy.MaintenancePolicy{Archived: policy.ActionReview, Deprecated: policy.ActionReview, Stale: policy.ActionReview, Unknown: policy.ActionReview},
		Source:       policy.SourcePolicy{MissingRevision: policy.ActionReview},
		Selection:    policy.SelectionPolicy{MaxOptions: 3},
	}
}

func intRef(value int) *int { return &value }

func TestApplyPreferencesNeverMutatesTheBasePolicy(t *testing.T) {
	base := basePolicy()
	original := clonePolicy(base)

	ApplyPreferences(base, []Preference{
		{Kind: PrefAvoidDependency},
		{Kind: PrefDenyLicence, TextValue: "GPL-3.0"},
		{Kind: PrefMaxDirectDeps, IntValue: intRef(3)},
		{Kind: PrefDenyArchived},
	})

	if !policiesEqual(original, base) {
		t.Errorf("the stored base policy was mutated:\nbefore %+v\nafter  %+v", original, base)
	}
	if base.ID != "test/base" {
		t.Errorf("base policy id changed to %q", base.ID)
	}
}

func TestApplyPreferencesAppliesEachEffect(t *testing.T) {
	effective := ApplyPreferences(basePolicy(), []Preference{
		{Kind: PrefAvoidDependency},
		{Kind: PrefAvoidReference},
		{Kind: PrefDenyLicence, TextValue: "GPL-3.0"},
		{Kind: PrefMaxDirectDeps, IntValue: intRef(3)},
		{Kind: PrefDenyArchived},
	})

	if containsMode(effective.Reuse.Allowed, model.ReuseDependency) {
		t.Errorf("avoid_dependency did not remove dependency: %v", effective.Reuse.Allowed)
	}
	if containsMode(effective.Reuse.Allowed, model.ReuseReference) {
		t.Errorf("avoid_reference did not remove reference: %v", effective.Reuse.Allowed)
	}
	if !containsMode(effective.Reuse.Allowed, model.ReuseCopy) {
		t.Errorf("an unrelated mode was removed: %v", effective.Reuse.Allowed)
	}
	if !contains(effective.Licence.Deny, "GPL-3.0") {
		t.Errorf("licence deny list = %v", effective.Licence.Deny)
	}
	if effective.Dependencies.MaxDirect == nil || *effective.Dependencies.MaxDirect != 3 {
		t.Errorf("max_direct = %v, want 3", effective.Dependencies.MaxDirect)
	}
	if effective.Maintenance.Archived != policy.ActionDeny {
		t.Errorf("archived action = %q, want deny", effective.Maintenance.Archived)
	}
}

func TestMaxDirectPrefersTheStricterValue(t *testing.T) {
	stricter := ApplyPreferences(basePolicy(), []Preference{{Kind: PrefMaxDirectDeps, IntValue: intRef(2)}})
	if stricter.Dependencies.MaxDirect == nil || *stricter.Dependencies.MaxDirect != 2 {
		t.Fatalf("max_direct = %v, want 2", stricter.Dependencies.MaxDirect)
	}
	looser := ApplyPreferences(stricter, []Preference{{Kind: PrefMaxDirectDeps, IntValue: intRef(9)}})
	if *looser.Dependencies.MaxDirect != 2 {
		t.Errorf("a looser memory must not relax the ceiling: got %d, want 2", *looser.Dependencies.MaxDirect)
	}
}

func TestAvoidModeOnAnUnrestrictedPolicyStillRestricts(t *testing.T) {
	unrestricted := basePolicy()
	unrestricted.Reuse.Allowed = nil // empty means "everything is permitted"

	effective := ApplyPreferences(unrestricted, []Preference{{Kind: PrefAvoidDependency}})
	if containsMode(effective.Reuse.Allowed, model.ReuseDependency) {
		t.Errorf("avoid_dependency had no effect on an unrestricted policy: %v", effective.Reuse.Allowed)
	}
	if len(effective.Reuse.Allowed) == 0 {
		t.Error("an unrestricted policy must become a specific restriction")
	}
}

func TestExclusionFeedbackOnlyCoversCandidatesInPlay(t *testing.T) {
	preferences := []Preference{
		{Kind: PrefExcludeCandidate, CandidateID: "fixture/in-request", SourceReason: policy.FeedbackNotQuite},
		{Kind: PrefExcludeCandidate, CandidateID: "fixture/elsewhere", SourceReason: policy.FeedbackNotQuite},
		{Kind: PrefAvoidDependency, CandidateID: "fixture/elsewhere"},
	}
	present := map[string]bool{"fixture/in-request": true}

	feedback := ExclusionFeedback(preferences, present)
	if len(feedback) != 1 || feedback[0].CandidateID != "fixture/in-request" {
		t.Fatalf("feedback = %+v", feedback)
	}
	if feedback[0].Reason != policy.FeedbackNotQuite {
		t.Errorf("reason = %q", feedback[0].Reason)
	}
}

func TestForgottenPreferenceIsNoLongerEffective(t *testing.T) {
	active := []Preference{{Kind: PrefAvoidDependency}}
	effective := ApplyPreferences(basePolicy(), active)
	if containsMode(effective.Reuse.Allowed, model.ReuseDependency) {
		t.Fatal("avoid_dependency should apply while active")
	}

	revoked := active
	revoked[0].ForgottenAt = time.Now()
	stillActive := activePreferences(revoked)
	if len(stillActive) != 0 {
		t.Fatalf("active preferences = %+v, want none", stillActive)
	}
	after := ApplyPreferences(basePolicy(), stillActive)
	if !containsMode(after.Reuse.Allowed, model.ReuseDependency) {
		t.Errorf("a forgotten preference still applies: %v", after.Reuse.Allowed)
	}
}

func TestContextHashIsDeterministicAndSensitive(t *testing.T) {
	project := Project{ID: "project/go/abc", SourceKind: SourceLocal}
	fingerprint := Fingerprint{SchemaVersion: SchemaVersion, Language: LanguageGo, Modules: []GoModule{{ModulePath: "example.com/a"}}}
	preferences := []Preference{{ID: 1, Kind: PrefAvoidDependency}, {ID: 2, Kind: PrefDenyArchived}}

	first := BuildContext(project, fingerprint, strings.Repeat("a", 64), "", preferences)
	second := BuildContext(project, fingerprint, strings.Repeat("a", 64), "", preferences)
	hashA, err := ContextHash(first)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	hashB, err := ContextHash(second)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hashA != hashB {
		t.Error("identical context produced different hashes")
	}

	changed := BuildContext(project, fingerprint, strings.Repeat("a", 64), "", preferences[:1])
	hashC, err := ContextHash(changed)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hashC == hashA {
		t.Error("removing a preference did not change the context hash")
	}
	if first.ProjectID != "project/go/abc" {
		t.Errorf("context project id = %q", first.ProjectID)
	}
}

func TestEffectivePolicyIDIsDerivedNotRandom(t *testing.T) {
	got := EffectivePolicyID("public-go-baseline/v1", strings.Repeat("b", 64))
	if got != "public-go-baseline/v1+project:"+strings.Repeat("b", 64) {
		t.Errorf("effective policy id = %q", got)
	}
	first := EffectivePolicyID("x", "y")
	second := EffectivePolicyID("x", "y")
	if first != second {
		t.Error("effective policy id is not deterministic")
	}
}

func containsMode(values []model.ReuseMode, want model.ReuseMode) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func policiesEqual(a, b policy.Policy) bool {
	return mustJSONTest(a) == mustJSONTest(b)
}

func mustJSONTest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
