package resolver

import (
	"errors"
	"fmt"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// CandidateOption couples a specimen with the reuse mode under consideration
// and the evidence about it. The caller states the mode explicitly: the kernel
// never invents a preference between modes.
type CandidateOption struct {
	Specimen  model.Specimen
	ReuseMode model.ReuseMode
	Evidence  []model.Evidence
}

// ResolveInput is the whole input to the pure resolver kernel.
//
// Candidates are considered in the supplied order. Packet 4 does not rank
// candidates: the first candidate satisfying the acceptance rule wins. A later
// packet owns richer evidence, policy and ranking behaviour.
type ResolveInput struct {
	Primitive  model.Primitive
	Contract   model.Contract
	Candidates []CandidateOption
	ResolvedAt time.Time
}

// CandidateDecision is what the kernel decided about one candidate option.
type CandidateDecision struct {
	SpecimenID       string              `json:"specimen_id"`
	ReuseMode        model.ReuseMode     `json:"reuse_mode"`
	Selected         bool                `json:"selected"`
	RejectionReasons []string            `json:"rejection_reasons,omitempty"`
	Evaluation       CandidateEvaluation `json:"evaluation"`
}

// Decision is the kernel output: the durable domain conclusion plus an
// inspectable record of every candidate actually considered.
type Decision struct {
	Resolution model.Resolution    `json:"resolution"`
	Considered []CandidateDecision `json:"considered"`
}

// Kernel validation errors. Malformed input is never converted into a
// decision, and never into BUILD LOCALLY.
var (
	ErrEmptyPrimitiveID           = errors.New("resolver: primitive ID is empty")
	ErrPrimitiveContractMismatch  = errors.New("resolver: primitive contract ID does not match contract")
	ErrContractPrimitiveMismatch  = errors.New("resolver: contract primitive ID does not match primitive")
	ErrZeroResolvedAt             = errors.New("resolver: resolved time is zero")
	ErrDuplicateCandidateID       = errors.New("resolver: duplicate candidate specimen ID")
	ErrCandidatePrimitiveMismatch = errors.New("resolver: candidate specimen does not belong to the primitive")
	ErrUnsupportedReuseMode       = errors.New("resolver: unsupported reuse mode")
	ErrReuseModeNotDeclared       = errors.New("resolver: reuse mode is not declared by the specimen")
)

// reuseModeOutcomes maps the explicitly requested reuse mode to the durable
// outcome. There is no quality ordering: the caller supplied the mode.
var reuseModeOutcomes = map[model.ReuseMode]model.Outcome{
	model.ReuseCopy:       model.OutcomeReuse,
	model.ReuseDependency: model.OutcomeDepend,
	model.ReuseAdapt:      model.OutcomeAdapt,
	model.ReuseReference:  model.OutcomeReference,
}

// Resolve deterministically evaluates ordered candidate options against a
// contract and returns the first acceptable candidate, or BUILD LOCALLY.
func Resolve(in ResolveInput) (Decision, error) {
	if err := validateResolveInput(in); err != nil {
		return Decision{}, err
	}

	considered := make([]CandidateDecision, 0, len(in.Candidates))
	evidenceIDs := newOrderedSet()
	unknowns := newOrderedSet()

	var (
		selectedOption    CandidateOption
		selectedCandidate CandidateEvaluation
		selected          bool
	)

	for _, option := range in.Candidates {
		evaluation, err := Evaluate(in.Contract, option.Specimen, option.Evidence)
		if err != nil {
			return Decision{}, err
		}

		decision := CandidateDecision{
			SpecimenID: option.Specimen.ID,
			ReuseMode:  option.ReuseMode,
			Evaluation: evaluation,
		}
		collectEvidenceIDs(evidenceIDs, evaluation)

		if evaluation.FullySatisfiesRequired(in.Contract) {
			decision.Selected = true
			considered = append(considered, decision)
			selectedOption, selectedCandidate, selected = option, evaluation, true
			// Stop here: candidates after the selected one are not considered
			// and must not appear as though they were evaluated.
			break
		}

		decision.RejectionReasons = rejectionReasons(in.Contract, evaluation)
		considered = append(considered, decision)
		collectRejectedUnknowns(unknowns, option.Specimen.ID, in.Contract, evaluation)
	}

	resolution := model.Resolution{
		PrimitiveID: in.Primitive.ID,
		ContractID:  in.Contract.ID,
		ResolvedAt:  in.ResolvedAt,
		Reasons:     []string{},
		Rejected:    []model.Rejection{},
		Unknowns:    []string{},
		EvidenceIDs: []string{},
	}

	if selected {
		resolution.Outcome = reuseModeOutcomes[selectedOption.ReuseMode]
		resolution.SpecimenID = selectedOption.Specimen.ID
		resolution.Reasons = append([]string{
			fmt.Sprintf("candidate %q satisfies all required contract requirements", selectedOption.Specimen.ID),
			fmt.Sprintf("candidate considered as reuse mode %q", selectedOption.ReuseMode),
		}, optionalKnownLimitations(in.Contract, selectedCandidate)...)
		collectSelectedUnknowns(unknowns, selectedOption.Specimen.ID, in.Contract, selectedCandidate)
	} else {
		resolution.Outcome = model.OutcomeBuildLocally
		if len(in.Candidates) == 0 {
			resolution.Reasons = []string{"no candidate options were supplied"}
		} else {
			resolution.Reasons = []string{"no candidate option satisfied all required contract requirements"}
		}
	}

	for _, decision := range considered {
		if decision.Selected {
			continue
		}
		resolution.Rejected = append(resolution.Rejected, model.Rejection{
			SpecimenID: decision.SpecimenID,
			Reasons:    decision.RejectionReasons,
		})
	}

	resolution.Unknowns = unknowns.values()
	resolution.EvidenceIDs = evidenceIDs.values()

	return Decision{Resolution: resolution, Considered: considered}, nil
}

func validateResolveInput(in ResolveInput) error {
	if in.Primitive.ID == "" {
		return ErrEmptyPrimitiveID
	}
	if in.Contract.ID == "" {
		return ErrEmptyContractID
	}
	if in.Primitive.ContractID != in.Contract.ID {
		return fmt.Errorf("%w: primitive %q declares %q", ErrPrimitiveContractMismatch, in.Primitive.ID, in.Primitive.ContractID)
	}
	if in.Contract.PrimitiveID != in.Primitive.ID {
		return fmt.Errorf("%w: contract %q declares %q", ErrContractPrimitiveMismatch, in.Contract.ID, in.Contract.PrimitiveID)
	}
	if in.ResolvedAt.IsZero() {
		return ErrZeroResolvedAt
	}
	if err := validateContract(in.Contract); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(in.Candidates))
	for _, option := range in.Candidates {
		if option.Specimen.ID == "" {
			return ErrEmptySpecimenID
		}
		if _, duplicate := seen[option.Specimen.ID]; duplicate {
			return fmt.Errorf("%w: %q", ErrDuplicateCandidateID, option.Specimen.ID)
		}
		seen[option.Specimen.ID] = struct{}{}

		if option.Specimen.PrimitiveID != in.Primitive.ID {
			return fmt.Errorf("%w: %q declares %q", ErrCandidatePrimitiveMismatch, option.Specimen.ID, option.Specimen.PrimitiveID)
		}
		if _, supported := reuseModeOutcomes[option.ReuseMode]; !supported {
			return fmt.Errorf("%w: %q", ErrUnsupportedReuseMode, option.ReuseMode)
		}
		if !declaresReuseMode(option.Specimen, option.ReuseMode) {
			return fmt.Errorf("%w: specimen %q does not declare %q", ErrReuseModeNotDeclared, option.Specimen.ID, option.ReuseMode)
		}
	}
	return nil
}

func declaresReuseMode(specimen model.Specimen, mode model.ReuseMode) bool {
	for _, declared := range specimen.ReuseMode {
		if declared == mode {
			return true
		}
	}
	return false
}

// rejectionReasons lists required requirements that are not satisfied, in
// contract order. Optional requirements never reject a candidate.
func rejectionReasons(contract model.Contract, evaluation CandidateEvaluation) []string {
	status := statusByRequirementID(evaluation)
	reasons := make([]string, 0)
	for _, req := range contract.Requirements {
		if !req.Required {
			continue
		}
		s := status[req.ID]
		if s == RequirementSatisfied {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("required requirement %q %s", req.ID, s))
	}
	return reasons
}

// optionalKnownLimitations surfaces optional FAIL results as known limitations
// for the selected candidate. A known failure is never described as unknown.
func optionalKnownLimitations(contract model.Contract, evaluation CandidateEvaluation) []string {
	status := statusByRequirementID(evaluation)
	reasons := make([]string, 0)
	for _, req := range contract.Requirements {
		if req.Required || status[req.ID] != RequirementFailed {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("optional requirement %q failed (known limitation)", req.ID))
	}
	return reasons
}

// collectRejectedUnknowns records required UNKNOWN and CONFLICTING states of a
// rejected candidate, identifying both specimen and requirement.
func collectRejectedUnknowns(unknowns *orderedSet, specimenID string, contract model.Contract, evaluation CandidateEvaluation) {
	status := statusByRequirementID(evaluation)
	for _, req := range contract.Requirements {
		if !req.Required {
			continue
		}
		s := status[req.ID]
		if s == RequirementUnknown || s == RequirementConflicting {
			unknowns.add(specimenUnknown(specimenID, req.ID, s))
		}
	}
}

// collectSelectedUnknowns records optional UNKNOWN and CONFLICTING states of
// the selected candidate.
func collectSelectedUnknowns(unknowns *orderedSet, specimenID string, contract model.Contract, evaluation CandidateEvaluation) {
	status := statusByRequirementID(evaluation)
	for _, req := range contract.Requirements {
		if req.Required {
			continue
		}
		s := status[req.ID]
		if s == RequirementUnknown || s == RequirementConflicting {
			unknowns.add(specimenUnknown(specimenID, req.ID, s))
		}
	}
}

// collectEvidenceIDs records determining evidence in candidate consideration
// order, then contract requirement order, then evaluator evidence order.
//
// evaluation.Requirements is in contract requirement order because Evaluate
// walks Contract.Requirements in order, so the requirement lookup is implicit.
func collectEvidenceIDs(into *orderedSet, evaluation CandidateEvaluation) {
	for _, reqEval := range evaluation.Requirements {
		for _, evidenceID := range reqEval.EvidenceIDs {
			into.add(evidenceID)
		}
	}
}

func specimenUnknown(specimenID, requirementID string, status RequirementStatus) string {
	return specimenID + ": " + requirementID + " " + string(status)
}

func statusByRequirementID(evaluation CandidateEvaluation) map[string]RequirementStatus {
	status := make(map[string]RequirementStatus, len(evaluation.Requirements))
	for _, reqEval := range evaluation.Requirements {
		status[reqEval.RequirementID] = reqEval.Status
	}
	return status
}

// orderedSet is an order-preserving de-duplicator for deterministic output.
type orderedSet struct {
	order []string
	seen  map[string]struct{}
}

func newOrderedSet() *orderedSet {
	return &orderedSet{order: []string{}, seen: map[string]struct{}{}}
}

func (s *orderedSet) add(value string) {
	if _, exists := s.seen[value]; exists {
		return
	}
	s.seen[value] = struct{}{}
	s.order = append(s.order, value)
}

func (s *orderedSet) values() []string {
	return s.order
}
