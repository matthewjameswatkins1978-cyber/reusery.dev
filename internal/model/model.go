// Package model defines Reusery's core domain model.
package model

import "time"

// Primitive identifies a bounded engineering capability independent of any implementation.
type Primitive struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	ContractID  string   `json:"contract_id"`
}

// Contract describes observable behaviour and invariants for a primitive.
type Contract struct {
	ID           string        `json:"id"`
	PrimitiveID  string        `json:"primitive_id"`
	Version      string        `json:"version"`
	Summary      string        `json:"summary"`
	Requirements []Requirement `json:"requirements"`
}

// Requirement is one independently testable or inspectable contract claim.
type Requirement struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
}

// Specimen is a concrete implementation that may satisfy a Contract.
type Specimen struct {
	ID          string     `json:"id"`
	PrimitiveID string     `json:"primitive_id"`
	Name        string     `json:"name"`
	Source      SourceRef  `json:"source"`
	ReuseMode   []ReuseMode `json:"reuse_modes"`
}

// SourceRef pins evidence or code to an attributable source.
type SourceRef struct {
	URL      string `json:"url"`
	Revision string `json:"revision,omitempty"`
	Path     string `json:"path,omitempty"`
	License  string `json:"license,omitempty"`
}

// Evidence records one attributable observation. Evidence is not a score.
type Evidence struct {
	ID          string         `json:"id"`
	SubjectID   string         `json:"subject_id"`
	Kind        string         `json:"kind"`
	Claim       string         `json:"claim"`
	Result      EvidenceResult `json:"result"`
	Source      SourceRef      `json:"source"`
	ObservedAt  time.Time      `json:"observed_at"`
	AppliesTo   string         `json:"applies_to,omitempty"`
	Methodology string         `json:"methodology,omitempty"`
	Artifact    string         `json:"artifact,omitempty"`
}

// EvidenceResult keeps unknown distinct from pass/fail.
type EvidenceResult string

const (
	EvidencePass    EvidenceResult = "pass"
	EvidenceFail    EvidenceResult = "fail"
	EvidenceUnknown EvidenceResult = "unknown"
	EvidenceInfo    EvidenceResult = "info"
)

// ReuseMode describes how a specimen may reasonably be consumed.
type ReuseMode string

const (
	ReuseCopy      ReuseMode = "copy"
	ReuseDependency ReuseMode = "dependency"
	ReuseAdapt     ReuseMode = "adapt"
	ReuseReference ReuseMode = "reference"
)

// Outcome is a resolver conclusion, not a quality ranking.
type Outcome string

const (
	OutcomeReuse        Outcome = "reuse"
	OutcomeAdapt        Outcome = "adapt"
	OutcomeDepend       Outcome = "depend"
	OutcomeReference    Outcome = "reference"
	OutcomeBuildLocally Outcome = "build_locally"
)

// Resolution preserves both the conclusion and why it was reached.
type Resolution struct {
	PrimitiveID     string            `json:"primitive_id"`
	ContractID      string            `json:"contract_id"`
	Outcome         Outcome           `json:"outcome"`
	SpecimenID      string            `json:"specimen_id,omitempty"`
	Reasons         []string          `json:"reasons"`
	Rejected        []Rejection       `json:"rejected,omitempty"`
	Unknowns        []string          `json:"unknowns,omitempty"`
	EvidenceIDs     []string          `json:"evidence_ids,omitempty"`
	ResolvedAt      time.Time         `json:"resolved_at"`
}

// Rejection is negative knowledge worth preserving.
type Rejection struct {
	SpecimenID string   `json:"specimen_id"`
	Reasons    []string `json:"reasons"`
}
