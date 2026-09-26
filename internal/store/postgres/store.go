package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres/sqlc"
)

// ErrNotFound reports that a requested row does not exist. It is the shared
// store sentinel so transport layers never import this package just to
// recognise a missing row.
var ErrNotFound = store.ErrNotFound

// Store persists the Reusery domain model in PostgreSQL.
//
// Catalogue-like objects (Primitive, Contract, Specimen) use explicit upsert
// semantics, Evidence and Resolution are append-oriented.
type Store struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

// NewStore builds a Store over an existing pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: sqlc.New(pool)}
}

// UpsertPrimitive inserts or updates a primitive and its tags.
func (s *Store) UpsertPrimitive(ctx context.Context, p model.Primitive) error {
	if _, err := s.queries.UpsertPrimitive(ctx, primitiveToUpsert(p)); err != nil {
		return fmt.Errorf("upsert primitive %q: %w", p.ID, err)
	}
	return nil
}

// GetPrimitive loads a primitive by ID.
func (s *Store) GetPrimitive(ctx context.Context, id string) (model.Primitive, error) {
	row, err := s.queries.GetPrimitive(ctx, id)
	if err != nil {
		return model.Primitive{}, mapLookupError(err)
	}
	return primitiveFromRow(row), nil
}

// UpsertContract atomically replaces a contract and all of its requirements.
// Existing requirements are replaced rather than duplicated, preserving order.
func (s *Store) UpsertContract(ctx context.Context, c model.Contract) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin contract tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	if _, err := q.UpsertContract(ctx, contractToUpsert(c)); err != nil {
		return fmt.Errorf("upsert contract %q: %w", c.ID, err)
	}
	if err := q.DeleteContractRequirements(ctx, c.ID); err != nil {
		return fmt.Errorf("delete requirements for %q: %w", c.ID, err)
	}
	for position, req := range c.Requirements {
		if err := q.InsertRequirement(ctx, requirementToInsert(c.ID, position, req)); err != nil {
			return fmt.Errorf("insert requirement %q: %w", req.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit contract tx: %w", err)
	}
	return nil
}

// GetContract loads a contract and its requirements in authored order.
func (s *Store) GetContract(ctx context.Context, id string) (model.Contract, error) {
	row, err := s.queries.GetContract(ctx, id)
	if err != nil {
		return model.Contract{}, mapLookupError(err)
	}
	requirements, err := s.queries.ListRequirementsByContract(ctx, id)
	if err != nil {
		return model.Contract{}, fmt.Errorf("list requirements for %q: %w", id, err)
	}
	return contractFromRows(row, requirements), nil
}

// UpsertSpecimen inserts or updates a specimen and its source reference.
func (s *Store) UpsertSpecimen(ctx context.Context, sp model.Specimen) error {
	if _, err := s.queries.UpsertSpecimen(ctx, specimenToUpsert(sp)); err != nil {
		return fmt.Errorf("upsert specimen %q: %w", sp.ID, err)
	}
	return nil
}

// GetSpecimen loads a specimen by ID.
func (s *Store) GetSpecimen(ctx context.Context, id string) (model.Specimen, error) {
	row, err := s.queries.GetSpecimen(ctx, id)
	if err != nil {
		return model.Specimen{}, mapLookupError(err)
	}
	return specimenFromRow(row), nil
}

// InsertEvidence appends one observation. A duplicate evidence ID is an error
// rather than a silent overwrite.
func (s *Store) InsertEvidence(ctx context.Context, e model.Evidence) error {
	if _, err := s.queries.InsertEvidence(ctx, evidenceToInsert(e)); err != nil {
		return fmt.Errorf("insert evidence %q: %w", e.ID, err)
	}
	return nil
}

// FindEvidence returns the observation with the given ID. The boolean reports
// whether it exists; absence is not an error.
func (s *Store) FindEvidence(ctx context.Context, id string) (model.Evidence, bool, error) {
	row, err := s.queries.GetEvidence(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Evidence{}, false, nil
	}
	if err != nil {
		return model.Evidence{}, false, fmt.Errorf("find evidence %q: %w", id, err)
	}
	return evidenceFromRow(row), true, nil
}

// ListEvidenceBySubject returns all evidence for a subject, oldest first.
func (s *Store) ListEvidenceBySubject(ctx context.Context, subjectID string) ([]model.Evidence, error) {
	rows, err := s.queries.ListEvidenceBySubject(ctx, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list evidence for %q: %w", subjectID, err)
	}
	items := make([]model.Evidence, 0, len(rows))
	for _, row := range rows {
		items = append(items, evidenceFromRow(row))
	}
	return items, nil
}

// ListEvidenceAfter returns at most limit observations for subjectID in
// (observed_at, id) order, continuing strictly after the supplied key.
//
// It backs the paginated public inspection API. A zero after and empty afterID
// start at the oldest observation. The evidence_subject_idx index on
// subject_id covers the leading predicate; the composite ordering columns are
// not indexed because the bounded page size makes a sort of the matching rows
// cheaper than maintaining a second index speculatively.
func (s *Store) ListEvidenceAfter(ctx context.Context, subjectID string, after time.Time, afterID string, limit int) ([]model.Evidence, error) {
	rows, err := s.queries.ListEvidencePage(ctx, sqlc.ListEvidencePageParams{
		SubjectID:       subjectID,
		AfterObservedAt: timeToPg(after),
		AfterID:         afterID,
		PageLimit:       int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list evidence page for %q: %w", subjectID, err)
	}
	items := make([]model.Evidence, 0, len(rows))
	for _, row := range rows {
		items = append(items, evidenceFromRow(row))
	}
	return items, nil
}

// InsertResolution atomically appends a resolution and its rejections. It
// returns the storage-internal identity key; the domain model has no
// resolution ID.
func (s *Store) InsertResolution(ctx context.Context, r model.Resolution) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin resolution tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	row, err := q.InsertResolution(ctx, resolutionToInsert(r))
	if err != nil {
		return 0, fmt.Errorf("insert resolution: %w", err)
	}
	for position, rejection := range r.Rejected {
		if err := q.InsertRejection(ctx, rejectionToInsert(row.ID, position, rejection)); err != nil {
			return 0, fmt.Errorf("insert rejection %q: %w", rejection.SpecimenID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit resolution tx: %w", err)
	}
	return row.ID, nil
}

// GetResolution loads a resolution and its rejections in authored order.
func (s *Store) GetResolution(ctx context.Context, id int64) (model.Resolution, error) {
	row, err := s.queries.GetResolution(ctx, id)
	if err != nil {
		return model.Resolution{}, mapLookupError(err)
	}
	rejections, err := s.queries.ListRejectionsByResolution(ctx, id)
	if err != nil {
		return model.Resolution{}, fmt.Errorf("list rejections for %d: %w", id, err)
	}
	return resolutionFromRows(row, rejections), nil
}

// CountResolutions returns how many resolutions are stored. It exists for
// append-only verification in tests.
func (s *Store) CountResolutions(ctx context.Context) (int64, error) {
	count, err := s.queries.CountResolutions(ctx)
	if err != nil {
		return 0, fmt.Errorf("count resolutions: %w", err)
	}
	return count, nil
}

func mapLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
