package resolver

import (
	"context"
	"errors"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// qualityRepository seeds four candidates with deliberately different evidence
// so the service tests can exercise each decision path.
func qualityRepository() *fakeRepository {
	contract := kernelContract()

	eligible := model.Specimen{
		ID:          "spec-eligible",
		PrimitiveID: contract.PrimitiveID,
		Source:      model.SourceRef{URL: "https://example.test/eligible", Revision: "v1.0.0"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	unknown := model.Specimen{
		ID:          "spec-unknown",
		PrimitiveID: contract.PrimitiveID,
		Source:      model.SourceRef{URL: "https://example.test/unknown", Revision: "v1.0.0"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	failing := model.Specimen{
		ID:          "spec-failing",
		PrimitiveID: contract.PrimitiveID,
		Source:      model.SourceRef{URL: "https://example.test/failing", Revision: "v1.0.0"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	reference := model.Specimen{
		ID:          "spec-reference",
		PrimitiveID: contract.PrimitiveID,
		Source:      model.SourceRef{URL: "https://example.test/reference"},
		ReuseMode:   []model.ReuseMode{model.ReuseReference},
	}

	relevance := model.Evidence{
		ID:          "ev/spec-reference/discovery_match",
		SubjectID:   reference.ID,
		Kind:        policy.KindDiscoveryMatch,
		Claim:       "a provider surfaced this candidate",
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://example.test/api"},
		ObservedAt:  kernelResolvedAt,
		Methodology: "quality service test",
	}

	return &fakeRepository{
		primitives: map[string]model.Primitive{contract.PrimitiveID: kernelPrimitive()},
		contracts:  map[string]model.Contract{contract.ID: contract},
		specimens: map[string]model.Specimen{
			eligible.ID:  eligible,
			unknown.ID:   unknown,
			failing.ID:   failing,
			reference.ID: reference,
		},
		evidence: map[string][]model.Evidence{
			eligible.ID: passEvidence(eligible.ID, contract),
			unknown.ID:  {},
			failing.ID: {
				evidenceFor(failing.ID, "bounds-stdout", "ev/spec-failing/bounds-stdout", model.EvidenceFail),
			},
			reference.ID: {relevance},
		},
		resolutions: map[int64]model.Resolution{},
		nextID:      1,
	}
}

func qualityRequest(candidates ...CandidateRef) QualityRequest {
	return QualityRequest{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates:  candidates,
	}
}

func TestQualityServicePersistsExactlyOneResolutionWhenResolved(t *testing.T) {
	repository := qualityRepository()
	repository.nextID = 7
	service := NewQualityService(repository, fixedClock())

	stored, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-eligible", ReuseMode: model.ReuseDependency},
	))
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if stored.ResolutionID != 7 {
		t.Errorf("resolution id = %d, want 7", stored.ResolutionID)
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want exactly 1", len(repository.inserted))
	}
	if stored.Outcome.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q", stored.Outcome.Decision.Resolution.Outcome)
	}

	reloaded, err := service.Resolution(context.Background(), stored.ResolutionID)
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if reloaded.PolicyID != permissivePolicy().ID {
		t.Errorf("policy id = %q, want %q", reloaded.PolicyID, permissivePolicy().ID)
	}
	if !sameResolution(reloaded, *stored.Outcome.Decision.Resolution) {
		t.Error("the reloaded resolution differs from the returned decision")
	}
}

// sameResolution compares the durable identity of two resolutions.
func sameResolution(a, b model.Resolution) bool {
	return a.PrimitiveID == b.PrimitiveID &&
		a.ContractID == b.ContractID &&
		a.Outcome == b.Outcome &&
		a.SpecimenID == b.SpecimenID &&
		a.PolicyID == b.PolicyID &&
		a.ResolvedAt.Equal(b.ResolvedAt) &&
		len(a.Rejected) == len(b.Rejected)
}

func TestQualityServicePersistsNothingForNeedsVerification(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	stored, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-unknown", ReuseMode: model.ReuseDependency},
	))
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if stored.ResolutionID != 0 {
		t.Errorf("resolution id = %d, want 0", stored.ResolutionID)
	}
	if stored.Outcome.Decision.Status != StatusNeedsVerification {
		t.Errorf("status = %q", stored.Outcome.Decision.Status)
	}
	if stored.Outcome.Decision.Resolution != nil {
		t.Errorf("resolution = %+v, want nil", stored.Outcome.Decision.Resolution)
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want none", len(repository.inserted))
	}
}

func TestQualityServicePersistsBuildLocallyExactlyOnce(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	stored, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-failing", ReuseMode: model.ReuseDependency},
	))
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if stored.Outcome.Decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q", stored.Outcome.Decision.Resolution.Outcome)
	}
	if stored.ResolutionID == 0 {
		t.Error("BUILD LOCALLY is a real decision and must be persisted")
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(repository.inserted))
	}
}

func TestQualityServiceZeroCandidatesPersistsBuildLocally(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	stored, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest())
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if stored.Outcome.Decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q", stored.Outcome.Decision.Resolution.Outcome)
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(repository.inserted))
	}
}

func TestQualityServiceSelectsReferenceWhenNoDirectCandidateIsEligible(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	stored, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-unknown", ReuseMode: model.ReuseDependency},
		CandidateRef{SpecimenID: "spec-reference", ReuseMode: model.ReuseReference},
	))
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	resolution := stored.Outcome.Decision.Resolution
	if resolution == nil || resolution.Outcome != model.OutcomeReference {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.SpecimenID != "spec-reference" {
		t.Errorf("selected = %q", resolution.SpecimenID)
	}
	if len(resolution.Unknowns) == 0 {
		t.Error("unresolved required requirements must be preserved")
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(repository.inserted))
	}
}

func TestQualityServiceLoadFailurePersistsNothing(t *testing.T) {
	repository := qualityRepository()
	repository.failSpecimenID = "spec-eligible"
	service := NewQualityService(repository, fixedClock())

	_, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-eligible", ReuseMode: model.ReuseDependency},
	))
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want none", len(repository.inserted))
	}
}

func TestQualityServicePersistenceFailureIsReported(t *testing.T) {
	repository := qualityRepository()
	repository.insertErr = errors.New("database down")
	service := NewQualityService(repository, fixedClock())

	_, err := service.Choose(context.Background(), permissivePolicy(), qualityRequest(
		CandidateRef{SpecimenID: "spec-eligible", ReuseMode: model.ReuseDependency},
	))
	if err == nil || !errors.Is(err, repository.insertErr) {
		t.Fatalf("err = %v", err)
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("a failed insert must not be reported as stored")
	}
}

func TestQualityServiceRejectsStructuralProblemsBeforePersisting(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	badPolicy := permissivePolicy()
	badPolicy.Selection.MaxOptions = 99

	if _, err := service.Choose(context.Background(), badPolicy, qualityRequest(
		CandidateRef{SpecimenID: "spec-eligible", ReuseMode: model.ReuseDependency},
	)); err == nil {
		t.Fatal("an invalid policy must be rejected")
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want none", len(repository.inserted))
	}

	if _, err := service.Choose(context.Background(), permissivePolicy(), QualityRequest{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates:  []CandidateRef{{SpecimenID: "spec-eligible", ReuseMode: "vendor"}},
	}); err == nil {
		t.Fatal("an unsupported reuse mode must be rejected")
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want none", len(repository.inserted))
	}
}

func TestQualityServiceFeedbackIsAppliedEndToEnd(t *testing.T) {
	repository := qualityRepository()
	service := NewQualityService(repository, fixedClock())

	request := qualityRequest(
		CandidateRef{SpecimenID: "spec-eligible", ReuseMode: model.ReuseDependency},
		CandidateRef{SpecimenID: "spec-reference", ReuseMode: model.ReuseReference},
	)
	request.Feedback = []policy.Feedback{{CandidateID: "spec-eligible", Reason: policy.FeedbackNotQuite}}

	stored, err := service.Choose(context.Background(), permissivePolicy(), request)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if stored.Outcome.Decision.Resolution == nil || stored.Outcome.Decision.Resolution.Outcome != model.OutcomeReference {
		t.Fatalf("resolution = %+v, want the reference candidate", stored.Outcome.Decision.Resolution)
	}
	found := false
	for _, rejection := range stored.Outcome.Decision.Resolution.Rejected {
		if rejection.SpecimenID == "spec-eligible" &&
			rejection.Reasons[0] == "user_feedback:not_quite" {
			found = true
		}
	}
	if !found {
		t.Errorf("negative knowledge was lost: %+v", stored.Outcome.Decision.Resolution.Rejected)
	}
	if len(stored.Outcome.AppliedFeedback) != 1 {
		t.Errorf("applied feedback = %+v", stored.Outcome.AppliedFeedback)
	}
}
