package project

import (
	"context"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// DecideRequest is a project-aware decision.
//
// BasePolicy is optional: nil means the built-in public baseline, exactly as
// the MCP resolver tool does when no policy is supplied.
type DecideRequest struct {
	ProjectID   string
	PrimitiveID string
	ContractID  string
	Candidates  []resolver.CandidateRef
	BasePolicy  *policy.Policy
	Feedback    []policy.Feedback
}

// DecideResult is the normal Packet 7 outcome plus the bounded project effects
// that helped produce it.
type DecideResult struct {
	Decision resolver.StoredQualityDecision
	// ProjectID and ContextHash are also written onto the persisted
	// Resolution, so a historical decision keeps naming what influenced it.
	ProjectID         string
	ContextHash       string
	FingerprintSHA256 string
	// Effects reports the dependency fit of every candidate, which is how a
	// caller learns exactly which project context mattered.
	Effects []ProjectEffect
	// ActivePreferences is how many remembered memories were applied.
	ActivePreferences int
}

// Decide runs a project-aware resolution through the existing Packet 7
// quality service.
//
// There is no second resolver: this method loads project context, overlays
// preferences onto a copy of the base policy, derives bounded dependency-fit
// facts, and then delegates. Behavioural evaluation, dispositions, ordering
// and persistence all stay in Packet 7.
func (s *Service) Decide(ctx context.Context, request DecideRequest) (DecideResult, error) {
	if len(request.Candidates) > MaxCandidatesPerDecision {
		return DecideResult{}, fmt.Errorf("%w: %d candidates, limit %d",
			ErrBoundsExceeded, len(request.Candidates), MaxCandidatesPerDecision)
	}
	project, fingerprint, sha, revision, err := s.load(ctx, request.ProjectID)
	if err != nil {
		return DecideResult{}, err
	}
	active, err := s.repository.ListActivePreferences(ctx, request.ProjectID, MaxActivePreferences+1)
	if err != nil {
		return DecideResult{}, fmt.Errorf("load preferences: %w", err)
	}
	if len(active) > MaxActivePreferences {
		return DecideResult{}, fmt.Errorf("%w: more than %d active preferences",
			ErrBoundsExceeded, MaxActivePreferences)
	}
	active = activePreferences(active)

	snapshot := BuildContext(project, fingerprint, sha, revision, active)
	contextHash, err := ContextHash(snapshot)
	if err != nil {
		return DecideResult{}, err
	}
	fingerprintID, err := s.currentFingerprintID(ctx, request.ProjectID)
	if err != nil {
		return DecideResult{}, err
	}
	// The snapshot is persisted before the Resolution that references it, so
	// the foreign key always resolves. A decision that ends without a
	// Resolution leaves an unused immutable snapshot behind rather than a
	// dangling reference, which is the cheaper mistake.
	if err := s.repository.UpsertContext(ctx, StoredContext{
		Hash:          contextHash,
		ProjectID:     request.ProjectID,
		FingerprintID: fingerprintID,
		Context:       snapshot,
		CreatedAt:     s.clock().UTC(),
	}); err != nil {
		return DecideResult{}, fmt.Errorf("store project context: %w", err)
	}

	base := policy.PublicGoBaseline()
	if request.BasePolicy != nil {
		base = *request.BasePolicy
	}
	effective := ApplyPreferences(base, active)
	effective.ID = EffectivePolicyID(base.ID, contextHash)
	if err := effective.Validate(); err != nil {
		return DecideResult{}, err
	}

	specimens, err := s.loadSpecimens(ctx, request.Candidates)
	if err != nil {
		return DecideResult{}, err
	}
	fits := DependencyFits(fingerprint, request.Candidates, specimens)
	effects := make([]ProjectEffect, 0, len(request.Candidates))
	for _, candidate := range request.Candidates {
		fit := fits[candidate.SpecimenID]
		effects = append(effects, ProjectEffect{
			SpecimenID:    candidate.SpecimenID,
			DependencyFit: fit.DependencyFit,
			ModulePath:    fit.ModulePath,
			ModuleVersion: fit.ModuleVersion,
			Tradeoff: resolver.ProjectTradeoffMessage(fit.DependencyFit,
				fit.ModulePath, fit.ModuleVersion, specimens[candidate.SpecimenID].Source.Revision),
		})
	}

	present := make(map[string]bool, len(request.Candidates))
	for _, candidate := range request.Candidates {
		present[candidate.SpecimenID] = true
	}
	feedback := append(append([]policy.Feedback(nil), request.Feedback...),
		ExclusionFeedback(active, present)...)

	decision, err := s.quality.Choose(ctx, effective, resolver.QualityRequest{
		PrimitiveID:        request.PrimitiveID,
		ContractID:         request.ContractID,
		Candidates:         request.Candidates,
		Feedback:           feedback,
		ProjectID:          request.ProjectID,
		ProjectContextHash: contextHash,
	}.WithProjectContext(fits))
	if err != nil {
		return DecideResult{}, err
	}

	return DecideResult{
		Decision:          decision,
		ProjectID:         request.ProjectID,
		ContextHash:       contextHash,
		FingerprintSHA256: sha,
		Effects:           effects,
		ActivePreferences: len(active),
	}, nil
}

// Refine is Decide with structured feedback. Refinement and resolution share
// one path so feedback can never behave differently depending on the route.
func (s *Service) Refine(ctx context.Context, request DecideRequest) (DecideResult, error) {
	if len(request.Feedback) == 0 {
		return DecideResult{}, fmt.Errorf("%w: refine requires at least one feedback item", ErrInvalidRequest)
	}
	return s.Decide(ctx, request)
}

// currentFingerprintID resolves the storage identity of the latest
// fingerprint so a context snapshot can reference it.
func (s *Service) currentFingerprintID(ctx context.Context, projectID string) (int64, error) {
	fingerprint, err := s.repository.GetLatestFingerprint(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("load fingerprint: %w", err)
	}
	return fingerprint.ID, nil
}

// loadSpecimens fetches the candidate specimens needed to derive dependency
// fit. Only Source.Path and Source.Revision are consulted: no evidence is
// loaded, because project context is not evidence.
func (s *Service) loadSpecimens(ctx context.Context, candidates []resolver.CandidateRef) (map[string]model.Specimen, error) {
	out := make(map[string]model.Specimen, len(candidates))
	for _, candidate := range candidates {
		if specimen, ok := out[candidate.SpecimenID]; ok {
			_ = specimen
			continue
		}
		specimen, err := s.repository.GetSpecimen(ctx, candidate.SpecimenID)
		if err != nil {
			return nil, fmt.Errorf("load specimen %q: %w", candidate.SpecimenID, err)
		}
		out[candidate.SpecimenID] = specimen
	}
	return out, nil
}
