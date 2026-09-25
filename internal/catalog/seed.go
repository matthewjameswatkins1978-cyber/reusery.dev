package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Store is the persistence surface seeding needs. It is defined here so the
// catalog package never imports a driver.
type Store interface {
	UpsertPrimitive(context.Context, model.Primitive) error
	UpsertContract(context.Context, model.Contract) error
	UpsertSpecimen(context.Context, model.Specimen) error
	InsertEvidence(context.Context, model.Evidence) error
	FindEvidence(context.Context, string) (model.Evidence, bool, error)
}

// ErrEvidenceConflict reports an evidence ID that already exists with
// different content.
var ErrEvidenceConflict = errors.New("catalog: evidence ID exists with different content")

// Seed applies a bundle to storage.
//
// Catalogue-like objects are upserted. Evidence stays append-only: an
// observation is inserted when new, skipped when it already matches, and
// rejected when the same ID carries different content. Applying the same
// bundle twice therefore succeeds and never duplicates evidence.
func Seed(ctx context.Context, bundle Bundle, store Store) error {
	if err := store.UpsertPrimitive(ctx, bundle.Primitive); err != nil {
		return fmt.Errorf("seed primitive %q: %w", bundle.Primitive.ID, err)
	}
	if err := store.UpsertContract(ctx, bundle.Contract); err != nil {
		return fmt.Errorf("seed contract %q: %w", bundle.Contract.ID, err)
	}
	for _, specimen := range bundle.Specimens {
		if err := store.UpsertSpecimen(ctx, specimen); err != nil {
			return fmt.Errorf("seed specimen %q: %w", specimen.ID, err)
		}
	}

	for _, evidence := range bundle.Evidence {
		existing, found, err := store.FindEvidence(ctx, evidence.ID)
		if err != nil {
			return fmt.Errorf("find evidence %q: %w", evidence.ID, err)
		}
		if found {
			if !sameEvidence(existing, evidence) {
				return fmt.Errorf("%w: %q", ErrEvidenceConflict, evidence.ID)
			}
			continue
		}
		if err := store.InsertEvidence(ctx, evidence); err != nil {
			return fmt.Errorf("insert evidence %q: %w", evidence.ID, err)
		}
	}
	return nil
}

// sameEvidence compares observations by content, treating timestamps as
// instants.
func sameEvidence(a, b model.Evidence) bool {
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
