package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
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

// ListPrimitives returns at most limit primitives in id order.
//
// It backs capability discovery: an agent asks what engineering behaviours
// Reusery knows before asking for a decision. Ordering by id keeps the list
// stable across calls so a paginating client never sees rows jump around.
func (s *Store) ListPrimitives(ctx context.Context, limit int) ([]model.Primitive, error) {
	if limit <= 0 {
		return []model.Primitive{}, nil
	}
	rows, err := s.queries.ListPrimitives(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list primitives: %w", err)
	}
	items := make([]model.Primitive, 0, len(rows))
	for _, row := range rows {
		items = append(items, primitiveFromRow(row))
	}
	return items, nil
}

// InsertFeedback appends one factual post-resolution event and returns its
// storage identity. It never touches the Resolution itself.
func (s *Store) InsertFeedback(ctx context.Context, f outcome.Feedback) (int64, error) {
	row, err := s.queries.InsertResolutionFeedback(ctx, sqlc.InsertResolutionFeedbackParams{
		ResolutionID: f.ResolutionID,
		Kind:         string(f.Kind),
		Note:         f.Note,
		RecordedAt:   timeToPg(f.RecordedAt),
	})
	if err != nil {
		return 0, fmt.Errorf("insert resolution feedback: %w", err)
	}
	return row.ID, nil
}

// ListFeedback returns stored post-resolution events in chronological order.
func (s *Store) ListFeedback(ctx context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error) {
	rows, err := s.queries.ListResolutionFeedback(ctx, sqlc.ListResolutionFeedbackParams{
		ResolutionID: resolutionID,
		Limit:        int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list resolution feedback: %w", err)
	}
	items := make([]outcome.StoredFeedback, 0, len(rows))
	for _, row := range rows {
		items = append(items, outcome.StoredFeedback{
			ID: row.ID,
			Feedback: outcome.Feedback{
				ResolutionID: row.ResolutionID,
				Kind:         outcome.Kind(row.Kind),
				Note:         row.Note,
				RecordedAt:   row.RecordedAt.Time,
			},
		})
	}
	return items, nil
}

// ------------------------------------------------------------ project context

// UpsertProject stores a project's durable identity. A local project's
// source_locator is always empty by construction; the schema enforces it.
func (s *Store) UpsertProject(ctx context.Context, p project.Project) error {
	if _, err := s.queries.UpsertProject(ctx, projectToUpsert(p)); err != nil {
		return fmt.Errorf("upsert project: %w", err)
	}
	return nil
}

// GetProject loads one project by its derived identity.
func (s *Store) GetProject(ctx context.Context, id string) (project.Project, error) {
	row, err := s.queries.GetProject(ctx, id)
	if err != nil {
		return project.Project{}, mapLookupError(err)
	}
	return projectFromRow(row), nil
}

// InsertFingerprint stores one immutable fingerprint for a project.
func (s *Store) InsertFingerprint(ctx context.Context, fingerprint project.StoredFingerprint) (int64, error) {
	params, err := fingerprintToInsert(fingerprint)
	if err != nil {
		return 0, err
	}
	row, err := s.queries.InsertProjectFingerprint(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("insert project fingerprint: %w", err)
	}
	return row.ID, nil
}

// GetFingerprintByHash returns the stored fingerprint with this digest, or
// ErrNotFound when the project has never been seen with these manifest facts.
func (s *Store) GetFingerprintByHash(ctx context.Context, projectID, sha string) (project.StoredFingerprint, error) {
	row, err := s.queries.GetProjectFingerprintByHash(ctx, sqlc.GetProjectFingerprintByHashParams{
		ProjectID:         projectID,
		FingerprintSha256: sha,
	})
	if err != nil {
		return project.StoredFingerprint{}, mapLookupError(err)
	}
	return fingerprintFromRow(row)
}

// GetLatestFingerprint returns the newest fingerprint, or ErrNotFound
// when the project has none.
func (s *Store) GetLatestFingerprint(ctx context.Context, projectID string) (project.StoredFingerprint, error) {
	row, err := s.queries.GetLatestProjectFingerprint(ctx, projectID)
	if err != nil {
		return project.StoredFingerprint{}, mapLookupError(err)
	}
	return fingerprintFromRow(row)
}

// InsertPreference stores one explicit, reversible project memory.
func (s *Store) InsertPreference(ctx context.Context, preference project.Preference) (int64, error) {
	row, err := s.queries.InsertProjectPreference(ctx, preferenceToInsert(preference))
	if err != nil {
		return 0, fmt.Errorf("insert project preference: %w", err)
	}
	return row.ID, nil
}

// GetPreference loads one preference scoped to its project.
func (s *Store) GetPreference(ctx context.Context, projectID string, id int64) (project.Preference, error) {
	row, err := s.queries.GetProjectPreference(ctx, sqlc.GetProjectPreferenceParams{
		ID: id, ProjectID: projectID,
	})
	if err != nil {
		return project.Preference{}, mapLookupError(err)
	}
	return preferenceFromRow(row), nil
}

// ForgetPreference stamps forgotten_at. The row is never deleted, so history
// is preserved while the effect stops applying. Forgetting twice is a no-op.
func (s *Store) ForgetPreference(ctx context.Context, projectID string, id int64, at time.Time) error {
	if err := s.queries.ForgetProjectPreference(ctx, sqlc.ForgetProjectPreferenceParams{
		ID: id, ProjectID: projectID, ForgottenAt: timeToPg(at),
	}); err != nil {
		return fmt.Errorf("forget project preference: %w", err)
	}
	return nil
}

// ListPreferences returns stored memories in id order, active and forgotten.
func (s *Store) ListPreferences(ctx context.Context, projectID string, limit int) ([]project.Preference, error) {
	if limit <= 0 {
		return []project.Preference{}, nil
	}
	rows, err := s.queries.ListProjectPreferences(ctx, sqlc.ListProjectPreferencesParams{
		ProjectID: projectID, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list project preferences: %w", err)
	}
	items := make([]project.Preference, 0, len(rows))
	for _, row := range rows {
		items = append(items, preferenceFromRow(row))
	}
	return items, nil
}

// ListActivePreferences returns only the memories that still influence
// decisions, in id order.
func (s *Store) ListActivePreferences(ctx context.Context, projectID string, limit int) ([]project.Preference, error) {
	if limit <= 0 {
		return []project.Preference{}, nil
	}
	rows, err := s.queries.ListActiveProjectPreferences(ctx, sqlc.ListActiveProjectPreferencesParams{
		ProjectID: projectID, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list active project preferences: %w", err)
	}
	items := make([]project.Preference, 0, len(rows))
	for _, row := range rows {
		items = append(items, preferenceFromRow(row))
	}
	return items, nil
}

// UpsertContext stores an immutable project-context snapshot. Identical
// content maps to an identical hash, so this never rewrites history.
func (s *Store) UpsertContext(ctx context.Context, snapshot project.StoredContext) error {
	params, err := contextToUpsert(snapshot)
	if err != nil {
		return err
	}
	if _, err := s.queries.UpsertProjectContext(ctx, params); err != nil {
		return fmt.Errorf("upsert project context: %w", err)
	}
	return nil
}

// GetContext loads one immutable project-context snapshot by its hash.
func (s *Store) GetContext(ctx context.Context, hash string) (project.StoredContext, error) {
	row, err := s.queries.GetProjectContext(ctx, hash)
	if err != nil {
		return project.StoredContext{}, mapLookupError(err)
	}
	return contextFromRow(row)
}

// ListResolutions returns bounded, newest-first decision history.
//
// Rejections are loaded per resolution so negative knowledge survives into
// history. The bound is what keeps that cost predictable.
func (s *Store) ListResolutions(ctx context.Context, projectID string, limit int) ([]project.ProjectResolution, error) {
	if limit <= 0 {
		return []project.ProjectResolution{}, nil
	}
	rows, err := s.queries.ListResolutionsByProject(ctx, sqlc.ListResolutionsByProjectParams{
		ProjectID: optionalString(projectID), Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list resolutions for project: %w", err)
	}
	items := make([]project.ProjectResolution, 0, len(rows))
	for _, row := range rows {
		rejections, err := s.queries.ListRejectionsByResolution(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("list rejections for resolution %d: %w", row.ID, err)
		}
		items = append(items, project.ProjectResolution{
			ID:         row.ID,
			Resolution: resolutionFromRows(row, rejections),
		})
	}
	return items, nil
}

func mapLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
