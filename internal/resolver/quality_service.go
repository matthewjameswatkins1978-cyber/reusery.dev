package resolver

import (
	"context"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// QualityRequest is the structured `choose` request. Candidate order in this
// file is deliberately not quality order: the quality layer reorders
// everything through deterministic policy semantics.
type QualityRequest struct {
	PrimitiveID string            `json:"primitive_id"`
	ContractID  string            `json:"contract_id"`
	Candidates  []CandidateRef    `json:"candidates"`
	Feedback    []policy.Feedback `json:"feedback,omitempty"`
}

// StoredQualityDecision pairs the quality outcome with its storage identity.
// ResolutionID is zero whenever nothing was persisted, which is exactly the
// case for needs_verification.
type StoredQualityDecision struct {
	ResolutionID int64
	Outcome      QualityOutcome
}

// QualityService loads domain values, runs the pure quality layer and
// persists exactly one Resolution — and only when the decision actually
// resolved.
type QualityService struct {
	repository Repository
	clock      Clock
}

// NewQualityService builds a service over the existing repository boundary.
// The repository interface is shared with Packet 4: no new generic query
// surface is added for Packet 7.
func NewQualityService(repository Repository, clock Clock) *QualityService {
	return &QualityService{repository: repository, clock: clock}
}

// Choose loads the requested data, applies structured feedback, assesses every
// candidate against the supplied policy and persists a Resolution only when
// the decision is resolved.
//
// A needs_verification outcome persists nothing at all: absence of evidence
// must never be recorded as a decision, and no fake BUILD LOCALLY resolution
// is ever written. If loading fails, the kernel rejects the input or feedback
// cannot be applied, nothing is persisted. If persistence fails, the error is
// returned and success is not claimed.
func (s *QualityService) Choose(ctx context.Context, pol policy.Policy, request QualityRequest) (StoredQualityDecision, error) {
	primitive, err := s.repository.GetPrimitive(ctx, request.PrimitiveID)
	if err != nil {
		return StoredQualityDecision{}, fmt.Errorf("load primitive %q: %w", request.PrimitiveID, err)
	}

	contract, err := s.repository.GetContract(ctx, request.ContractID)
	if err != nil {
		return StoredQualityDecision{}, fmt.Errorf("load contract %q: %w", request.ContractID, err)
	}

	candidates := make([]CandidateOption, 0, len(request.Candidates))
	for _, ref := range request.Candidates {
		specimen, err := s.repository.GetSpecimen(ctx, ref.SpecimenID)
		if err != nil {
			return StoredQualityDecision{}, fmt.Errorf("load specimen %q: %w", ref.SpecimenID, err)
		}
		evidence, err := s.repository.ListEvidenceBySubject(ctx, ref.SpecimenID)
		if err != nil {
			return StoredQualityDecision{}, fmt.Errorf("load evidence for %q: %w", ref.SpecimenID, err)
		}
		candidates = append(candidates, CandidateOption{
			Specimen:  specimen,
			ReuseMode: ref.ReuseMode,
			Evidence:  evidence,
		})
	}

	outcome, err := Decide(QualityInput{
		Primitive:  primitive,
		Contract:   contract,
		Candidates: candidates,
		Policy:     pol,
		Feedback:   request.Feedback,
		Now:        s.clock().UTC(),
	})
	if err != nil {
		return StoredQualityDecision{}, err
	}
	if outcome.Decision.Resolution == nil {
		return StoredQualityDecision{Outcome: outcome}, nil
	}

	resolutionID, err := s.repository.InsertResolution(ctx, *outcome.Decision.Resolution)
	if err != nil {
		return StoredQualityDecision{}, fmt.Errorf("persist resolution: %w", err)
	}
	return StoredQualityDecision{ResolutionID: resolutionID, Outcome: outcome}, nil
}

// Resolution loads a previously persisted Resolution by storage ID.
func (s *QualityService) Resolution(ctx context.Context, id int64) (model.Resolution, error) {
	resolution, err := s.repository.GetResolution(ctx, id)
	if err != nil {
		return model.Resolution{}, fmt.Errorf("load resolution %d: %w", id, err)
	}
	return resolution, nil
}
