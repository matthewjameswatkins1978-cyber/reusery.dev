package resolver

import (
	"context"
	"fmt"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Repository is the storage boundary the application service depends on. It is
// defined here, outside PostgreSQL, so the service never imports a driver.
type Repository interface {
	GetPrimitive(context.Context, string) (model.Primitive, error)
	GetContract(context.Context, string) (model.Contract, error)
	GetSpecimen(context.Context, string) (model.Specimen, error)
	ListEvidenceBySubject(context.Context, string) ([]model.Evidence, error)
	InsertResolution(context.Context, model.Resolution) (int64, error)
	GetResolution(context.Context, int64) (model.Resolution, error)
}

// Clock supplies the resolution timestamp. Deterministic resolver logic never
// calls time.Now itself.
type Clock func() time.Time

// CandidateRef names one candidate in a machine-readable request.
type CandidateRef struct {
	SpecimenID string          `json:"specimen_id"`
	ReuseMode  model.ReuseMode `json:"reuse_mode"`
}

// Request is a structured resolution request. There is no natural-language
// intent here: that arrives in a later packet.
type Request struct {
	PrimitiveID string         `json:"primitive_id"`
	ContractID  string         `json:"contract_id"`
	Candidates  []CandidateRef `json:"candidates"`
}

// StoredDecision pairs the durable conclusion with its storage identity.
type StoredDecision struct {
	ResolutionID int64
	Decision     Decision
}

// Service loads domain values, runs the pure kernel and persists the outcome.
type Service struct {
	repository Repository
	clock      Clock
}

// NewService builds a service over an injected repository and clock.
func NewService(repository Repository, clock Clock) *Service {
	return &Service{repository: repository, clock: clock}
}

// Resolve loads the requested data, evaluates it with the pure kernel and
// persists exactly one Resolution — including BUILD LOCALLY.
//
// If loading fails or the kernel rejects the input, nothing is persisted. If
// persistence fails, the error is returned and success is not claimed.
func (s *Service) Resolve(ctx context.Context, request Request) (StoredDecision, error) {
	primitive, err := s.repository.GetPrimitive(ctx, request.PrimitiveID)
	if err != nil {
		return StoredDecision{}, fmt.Errorf("load primitive %q: %w", request.PrimitiveID, err)
	}

	contract, err := s.repository.GetContract(ctx, request.ContractID)
	if err != nil {
		return StoredDecision{}, fmt.Errorf("load contract %q: %w", request.ContractID, err)
	}

	candidates := make([]CandidateOption, 0, len(request.Candidates))
	for _, ref := range request.Candidates {
		specimen, err := s.repository.GetSpecimen(ctx, ref.SpecimenID)
		if err != nil {
			return StoredDecision{}, fmt.Errorf("load specimen %q: %w", ref.SpecimenID, err)
		}
		evidence, err := s.repository.ListEvidenceBySubject(ctx, ref.SpecimenID)
		if err != nil {
			return StoredDecision{}, fmt.Errorf("load evidence for %q: %w", ref.SpecimenID, err)
		}
		candidates = append(candidates, CandidateOption{
			Specimen:  specimen,
			ReuseMode: ref.ReuseMode,
			Evidence:  evidence,
		})
	}

	decision, err := Resolve(ResolveInput{
		Primitive:  primitive,
		Contract:   contract,
		Candidates: candidates,
		ResolvedAt: s.clock().UTC(),
	})
	if err != nil {
		return StoredDecision{}, err
	}

	resolutionID, err := s.repository.InsertResolution(ctx, decision.Resolution)
	if err != nil {
		return StoredDecision{}, fmt.Errorf("persist resolution: %w", err)
	}

	return StoredDecision{ResolutionID: resolutionID, Decision: decision}, nil
}

// Resolution loads a previously persisted Resolution by storage ID. The stored
// decision is the remembered history; evidence is not re-evaluated.
func (s *Service) Resolution(ctx context.Context, id int64) (model.Resolution, error) {
	resolution, err := s.repository.GetResolution(ctx, id)
	if err != nil {
		return model.Resolution{}, fmt.Errorf("load resolution %d: %w", id, err)
	}
	return resolution, nil
}
