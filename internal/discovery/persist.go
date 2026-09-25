package discovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Store is the persistence surface discovery needs. It is declared here so
// internal/discovery never imports a driver.
type Store interface {
	GetPrimitive(context.Context, string) (model.Primitive, error)
	GetContract(context.Context, string) (model.Contract, error)
	UpsertSpecimen(context.Context, model.Specimen) error
	FindEvidence(context.Context, string) (model.Evidence, bool, error)
	InsertEvidence(context.Context, model.Evidence) error
}

// ErrEvidenceConflict reports an evidence ID that already exists with
// different content.
var ErrEvidenceConflict = errors.New("discovery: evidence ID exists with different content")

// Persist writes discovered specimens and their observations using the
// existing registry tables.
//
// Deliberately not one giant transaction: public discovery produces
// independently useful observations, so persistence is resumable. Specimen
// writes are upserts and evidence writes are identity-checked append
// operations, so a run that fails part way through returns its error without
// claiming success and a later rerun safely continues.
func Persist(ctx context.Context, store Store, candidates []Candidate) error {
	for _, candidate := range candidates {
		if err := store.UpsertSpecimen(ctx, candidate.Specimen); err != nil {
			return fmt.Errorf("persist specimen %q: %w", candidate.Specimen.ID, err)
		}
		for _, evidence := range candidate.Evidence {
			existing, found, err := store.FindEvidence(ctx, evidence.ID)
			if err != nil {
				return fmt.Errorf("find evidence %q: %w", evidence.ID, err)
			}
			if found {
				if !sameObservation(existing, evidence) {
					return fmt.Errorf("%w: %q", ErrEvidenceConflict, evidence.ID)
				}
				continue
			}
			if err := store.InsertEvidence(ctx, evidence); err != nil {
				return fmt.Errorf("insert evidence %q: %w", evidence.ID, err)
			}
		}
	}
	return nil
}

// sameObservation compares two observations by content, treating timestamps
// as instants. It mirrors the catalogue's append-only rule for the discovery
// domain rather than reaching across into internal/catalog.
func sameObservation(a, b model.Evidence) bool {
	return a.ID == b.ID &&
		a.SubjectID == b.SubjectID &&
		a.Kind == b.Kind &&
		a.Claim == b.Claim &&
		a.Result == b.Result &&
		a.Source == b.Source &&
		a.AppliesTo == b.AppliesTo &&
		a.Methodology == b.Methodology &&
		a.Artifact == b.Artifact &&
		a.ObservedAt.Equal(b.ObservedAt)
}
