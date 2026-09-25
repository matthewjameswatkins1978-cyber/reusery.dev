// Package resolver evaluates a specimen against a contract using evidence.
//
// Evaluation is deterministic and per-requirement: it never ranks candidates,
// never produces a confidence score and never treats missing evidence as
// proof. For every requirement it states whether the requirement is
// satisfied, failed, unknown or conflicting, together with the evidence that
// determined that conclusion.
package resolver

import (
	"errors"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// RequirementStatus is the deterministic evaluation of one contract
// requirement for one specimen. It is an observation, not a score.
type RequirementStatus string

const (
	// RequirementSatisfied means pass evidence exists and no fail evidence does.
	RequirementSatisfied RequirementStatus = "satisfied"
	// RequirementFailed means fail evidence exists and no pass evidence does.
	RequirementFailed RequirementStatus = "failed"
	// RequirementUnknown means no observation establishes or refutes the
	// requirement. Missing evidence is always unknown.
	RequirementUnknown RequirementStatus = "unknown"
	// RequirementConflicting means pass and fail evidence both exist.
	RequirementConflicting RequirementStatus = "conflicting"
)

// RequirementEvaluation records the status of one requirement and the
// evidence that determined it.
type RequirementEvaluation struct {
	RequirementID string            `json:"requirement_id"`
	Status        RequirementStatus `json:"status"`
	EvidenceIDs   []string          `json:"evidence_ids,omitempty"`
}

// CandidateEvaluation is a specimen's full requirement matrix.
type CandidateEvaluation struct {
	SpecimenID   string                  `json:"specimen_id"`
	Requirements []RequirementEvaluation `json:"requirements"`
}

// Validation errors returned by Evaluate.
var (
	ErrEmptySpecimenID        = errors.New("resolver: specimen ID is empty")
	ErrEmptyContractID        = errors.New("resolver: contract ID is empty")
	ErrEmptyRequirementID     = errors.New("resolver: contract requirement ID is empty")
	ErrDuplicateRequirementID = errors.New("resolver: duplicate contract requirement ID")
)

// Evaluate maps evidence onto every requirement of contract for specimen.
//
// Evidence applies to a requirement only when both:
//
//	Evidence.SubjectID == specimen.ID
//	Evidence.AppliesTo == requirement.ID
//
// Evidence for another specimen is ignored, and evidence that does not name a
// requirement via AppliesTo cannot satisfy one.
func Evaluate(contract model.Contract, specimen model.Specimen, evidence []model.Evidence) (CandidateEvaluation, error) {
	if specimen.ID == "" {
		return CandidateEvaluation{}, ErrEmptySpecimenID
	}
	if contract.ID == "" {
		return CandidateEvaluation{}, ErrEmptyContractID
	}
	if err := validateContract(contract); err != nil {
		return CandidateEvaluation{}, err
	}

	evaluation := CandidateEvaluation{
		SpecimenID:   specimen.ID,
		Requirements: make([]RequirementEvaluation, 0, len(contract.Requirements)),
	}
	for _, req := range contract.Requirements {
		status, ids := evaluateRequirement(evidence, specimen.ID, req.ID)
		evaluation.Requirements = append(evaluation.Requirements, RequirementEvaluation{
			RequirementID: req.ID,
			Status:        status,
			EvidenceIDs:   ids,
		})
	}
	return evaluation, nil
}

// validateContract rejects malformed contracts before any evaluation happens.
// The kernel shares it so a malformed contract can never manufacture a
// decision.
func validateContract(contract model.Contract) error {
	seen := make(map[string]struct{}, len(contract.Requirements))
	for _, req := range contract.Requirements {
		if req.ID == "" {
			return ErrEmptyRequirementID
		}
		if _, duplicate := seen[req.ID]; duplicate {
			return fmt.Errorf("%w: %q", ErrDuplicateRequirementID, req.ID)
		}
		seen[req.ID] = struct{}{}
	}
	return nil
}

// observation is one matching evidence item, kept in input order.
type observation struct {
	result model.EvidenceResult
	id     string
}

// evaluateRequirement applies the status rules to the evidence matching one
// specimen and requirement. Evidence IDs are returned in input order and only
// include the observations that determined the status.
func evaluateRequirement(evidence []model.Evidence, specimenID, requirementID string) (RequirementStatus, []string) {
	observations := make([]observation, 0)
	hasPass, hasFail := false, false
	for _, ev := range evidence {
		if ev.SubjectID != specimenID || ev.AppliesTo != requirementID {
			continue
		}
		observations = append(observations, observation{result: ev.Result, id: ev.ID})
		switch ev.Result {
		case model.EvidencePass:
			hasPass = true
		case model.EvidenceFail:
			hasFail = true
		}
	}

	status := RequirementUnknown
	switch {
	case hasPass && hasFail:
		status = RequirementConflicting
	case hasFail:
		status = RequirementFailed
	case hasPass:
		status = RequirementSatisfied
	}
	return status, determiningIDs(observations, status)
}

// determiningIDs selects the evidence IDs that produced the status, preserving
// input order. Info and unknown observations never determine satisfy/fail.
func determiningIDs(observations []observation, status RequirementStatus) []string {
	ids := make([]string, 0, len(observations))
	for _, obs := range observations {
		if observationDetermines(status, obs.result) {
			ids = append(ids, obs.id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func observationDetermines(status RequirementStatus, result model.EvidenceResult) bool {
	switch status {
	case RequirementSatisfied:
		return result == model.EvidencePass
	case RequirementFailed:
		return result == model.EvidenceFail
	case RequirementConflicting:
		return result == model.EvidencePass || result == model.EvidenceFail
	default: // RequirementUnknown
		return result == model.EvidenceInfo || result == model.EvidenceUnknown
	}
}

// HasFailures reports whether any requirement is explicitly failed.
func (e CandidateEvaluation) HasFailures() bool {
	for _, req := range e.Requirements {
		if req.Status == RequirementFailed {
			return true
		}
	}
	return false
}

// HasUnknowns reports whether any requirement remains unknown.
func (e CandidateEvaluation) HasUnknowns() bool {
	for _, req := range e.Requirements {
		if req.Status == RequirementUnknown {
			return true
		}
	}
	return false
}

// FullySatisfiesRequired reports whether every required requirement of contract
// is satisfied. Unknown, failed and conflicting requirements all prevent
// satisfaction; optional requirements never do.
func (e CandidateEvaluation) FullySatisfiesRequired(contract model.Contract) bool {
	statusByID := make(map[string]RequirementStatus, len(e.Requirements))
	for _, req := range e.Requirements {
		statusByID[req.RequirementID] = req.Status
	}
	for _, req := range contract.Requirements {
		if !req.Required {
			continue
		}
		if statusByID[req.ID] != RequirementSatisfied {
			return false
		}
	}
	return true
}
