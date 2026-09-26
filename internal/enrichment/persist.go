package enrichment

import (
	"context"
	"errors"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Store is the persistence surface enrichment needs. It is declared here so
// internal/enrichment never imports a driver.
type Store interface {
	GetSpecimen(context.Context, string) (model.Specimen, error)
	ListEvidenceBySubject(context.Context, string) ([]model.Evidence, error)
	FindEvidence(context.Context, string) (model.Evidence, bool, error)
	InsertEvidence(context.Context, model.Evidence) error
}

// ErrEvidenceConflict reports an evidence ID that already exists with
// different content.
var ErrEvidenceConflict = errors.New("enrichment: evidence ID exists with different content")

// Persist writes new observations using the existing evidence table.
//
// Deliberately not one giant transaction: enrichment produces independently
// useful observations, so persistence is resumable. Writes are identity-checked
// append operations, so repeating a run with the same observation instant is
// idempotent, a run that fails part way through returns its error without
// claiming success, and a later rerun safely continues.
func Persist(ctx context.Context, store Store, evidence []model.Evidence) error {
	for _, item := range evidence {
		existing, found, err := store.FindEvidence(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("find evidence %q: %w", item.ID, err)
		}
		if found {
			if !sameObservation(existing, item) {
				return fmt.Errorf("%w: %q", ErrEvidenceConflict, item.ID)
			}
			continue
		}
		if err := store.InsertEvidence(ctx, item); err != nil {
			return fmt.Errorf("insert evidence %q: %w", item.ID, err)
		}
	}
	return nil
}

// sameObservation compares two observations by content, treating timestamps as
// instants.
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
