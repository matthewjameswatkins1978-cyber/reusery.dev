package discovery

import "github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"

// CandidateSet collects candidates while keeping one entry per specimen.
//
// A specimen may legitimately be matched by several queries. The first
// sighting defines identity; later sightings contribute only observations that
// are not already present. Re-discovering the same specimen ID with different
// identity or source data is a clear failure rather than a silent choice.
type CandidateSet struct {
	order []string
	byID  map[string]*Candidate
}

// NewCandidateSet returns an empty set.
func NewCandidateSet() *CandidateSet {
	return &CandidateSet{byID: make(map[string]*Candidate)}
}

// Add merges a candidate into the set.
func (s *CandidateSet) Add(candidate Candidate) error {
	if s.byID == nil {
		s.byID = make(map[string]*Candidate)
	}
	existing, ok := s.byID[candidate.Specimen.ID]
	if !ok {
		fresh := candidate
		fresh.Evidence = append([]model.Evidence(nil), candidate.Evidence...)
		s.byID[candidate.Specimen.ID] = &fresh
		s.order = append(s.order, candidate.Specimen.ID)
		return nil
	}
	if existing.ProviderID != candidate.ProviderID || !sameSpecimen(existing.Specimen, candidate.Specimen) {
		return &ConflictError{SpecimenID: candidate.Specimen.ID}
	}

	seen := make(map[string]struct{}, len(existing.Evidence))
	for _, evidence := range existing.Evidence {
		seen[evidence.ID] = struct{}{}
	}
	for _, evidence := range candidate.Evidence {
		if _, duplicate := seen[evidence.ID]; duplicate {
			continue
		}
		seen[evidence.ID] = struct{}{}
		existing.Evidence = append(existing.Evidence, evidence)
	}
	return nil
}

// List returns the set in first-seen order.
func (s *CandidateSet) List() []Candidate {
	candidates := make([]Candidate, 0, len(s.order))
	for _, id := range s.order {
		candidates = append(candidates, *s.byID[id])
	}
	return candidates
}

// Len returns how many unique specimens the set holds.
func (s *CandidateSet) Len() int { return len(s.order) }

// ConflictError reports the same specimen ID carrying incompatible identity or
// source data. Reusery fails loudly instead of silently choosing one.
type ConflictError struct {
	SpecimenID string
}

func (e *ConflictError) Error() string {
	return "discovery: specimen " + e.SpecimenID + " was discovered with conflicting identity or source data"
}

// Unwrap ties a conflicting identity back to the safety rule it violates.
func (e *ConflictError) Unwrap() error { return ErrUnsafeOutput }

func sameSpecimen(a, b model.Specimen) bool {
	if a.ID != b.ID || a.PrimitiveID != b.PrimitiveID || a.Name != b.Name || a.Source != b.Source {
		return false
	}
	if len(a.ReuseMode) != len(b.ReuseMode) {
		return false
	}
	for i := range a.ReuseMode {
		if a.ReuseMode[i] != b.ReuseMode[i] {
			return false
		}
	}
	return true
}
