package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// Repository is the persistence surface the project service needs. It embeds
// the existing resolver repository so a project-aware decision can reuse the
// Packet 7 persistence path instead of inventing a second one.
type Repository interface {
	resolver.Repository
	UpsertProject(ctx context.Context, project Project) error
	GetProject(ctx context.Context, id string) (Project, error)
	InsertFingerprint(ctx context.Context, fingerprint StoredFingerprint) (int64, error)
	GetFingerprintByHash(ctx context.Context, projectID, sha string) (StoredFingerprint, error)
	GetLatestFingerprint(ctx context.Context, projectID string) (StoredFingerprint, error)
	InsertPreference(ctx context.Context, preference Preference) (int64, error)
	GetPreference(ctx context.Context, projectID string, id int64) (Preference, error)
	ForgetPreference(ctx context.Context, projectID string, id int64, at time.Time) error
	ListPreferences(ctx context.Context, projectID string, limit int) ([]Preference, error)
	ListActivePreferences(ctx context.Context, projectID string, limit int) ([]Preference, error)
	UpsertContext(ctx context.Context, snapshot StoredContext) error
	GetContext(ctx context.Context, hash string) (StoredContext, error)
	ListResolutions(ctx context.Context, projectID string, limit int) ([]ProjectResolution, error)
}

// Options configures a project service. Tests inject an httptest-backed
// GitHub client; production uses the unauthenticated public client.
type Options struct {
	Clock func() time.Time
	// NewGitHubClient must return a client that cannot read private content.
	// Packet 13 owns private access; Packet 10 must not stumble into it.
	NewGitHubClient func() GitHubClient
}

// Service owns project scanning, remembered preferences and project-aware
// decisions. It depends on interfaces and never imports PostgreSQL.
type Service struct {
	repository      Repository
	clock           func() time.Time
	newGitHubClient func() GitHubClient
	quality         *resolver.QualityService
}

// NewService builds a project service over an existing repository boundary.
func NewService(repository Repository, options Options) *Service {
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	factory := options.NewGitHubClient
	if factory == nil {
		factory = func() GitHubClient { return nil }
	}
	return &Service{
		repository:      repository,
		clock:           clock,
		newGitHubClient: factory,
		// The Packet 7 quality service is reused, never reimplemented.
		quality: resolver.NewQualityService(repository, resolver.Clock(clock)),
	}
}

// ScanLocal fingerprints an explicitly configured local root and stores the
// derived project facts. The root itself is never persisted.
func (s *Service) ScanLocal(ctx context.Context, root string) (ScanResult, error) {
	result, err := ScanLocal(ctx, root)
	if err != nil {
		return ScanResult{}, err
	}
	if err := s.persist(ctx, &result); err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

// ScanPublicGitHub fingerprints a public repository without cloning it and
// without a credential that could reveal private content.
func (s *Service) ScanPublicGitHub(ctx context.Context, repository, ref, subdir string) (ScanResult, error) {
	result, err := ScanPublicGitHub(ctx, s.newGitHubClient(), repository, ref, subdir)
	if err != nil {
		return ScanResult{}, err
	}
	if err := s.persist(ctx, &result); err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

// persist stores the project and its fingerprint idempotently: rescanning an
// unchanged project updates projects.updated_at and reuses the existing
// fingerprint row rather than creating a duplicate.
func (s *Service) persist(ctx context.Context, result *ScanResult) error {
	now := s.clock().UTC()
	project := result.Project
	project.CreatedAt = now
	project.UpdatedAt = now
	if err := s.repository.UpsertProject(ctx, project); err != nil {
		return fmt.Errorf("store project: %w", err)
	}

	_, err := s.repository.GetFingerprintByHash(ctx, project.ID, result.FingerprintSHA256)
	switch {
	case err == nil:
		result.Reused = true
		return nil
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("load fingerprint: %w", err)
	}

	if _, err := s.repository.InsertFingerprint(ctx, StoredFingerprint{
		ProjectID:      project.ID,
		SchemaVersion:  result.Fingerprint.SchemaVersion,
		SHA256:         result.FingerprintSHA256,
		Fingerprint:    result.Fingerprint,
		SourceRevision: result.SourceRevision,
		ObservedAt:     now,
	}); err != nil {
		return fmt.Errorf("store fingerprint: %w", err)
	}
	return nil
}

// View is the bounded inspection projection of a project.
type View struct {
	Project           Project
	Fingerprint       Fingerprint
	FingerprintSHA256 string
	SourceRevision    string
	// Preferences are the active memories that influence decisions.
	Preferences []Preference
	// Forgotten counts revoked memories, so reversibility stays visible.
	Forgotten int
	// Recent is newest-first decision history, bounded by the caller's limit.
	Recent []ProjectResolution
}

// Get returns a bounded view of one project.
func (s *Service) Get(ctx context.Context, projectID string, historyLimit int) (View, error) {
	project, fingerprint, sha, revision, err := s.load(ctx, projectID)
	if err != nil {
		return View{}, err
	}
	preferences, err := s.repository.ListActivePreferences(ctx, projectID, MaxActivePreferences+1)
	if err != nil {
		return View{}, fmt.Errorf("load preferences: %w", err)
	}
	all, err := s.repository.ListPreferences(ctx, projectID, MaxActivePreferences+1)
	if err != nil {
		return View{}, fmt.Errorf("load preferences: %w", err)
	}
	forgotten := 0
	for _, preference := range all {
		if !preference.Active() {
			forgotten++
		}
	}
	recent, err := s.repository.ListResolutions(ctx, projectID, boundedHistory(historyLimit))
	if err != nil {
		return View{}, fmt.Errorf("load history: %w", err)
	}
	return View{
		Project:           project,
		Fingerprint:       fingerprint,
		FingerprintSHA256: sha,
		SourceRevision:    revision,
		Preferences:       activePreferences(preferences),
		Forgotten:         forgotten,
		Recent:            recent,
	}, nil
}

// load fetches a project with its most recent fingerprint.
func (s *Service) load(ctx context.Context, projectID string) (Project, Fingerprint, string, string, error) {
	if projectID == "" {
		return Project{}, Fingerprint{}, "", "", ErrProjectNotFound
	}
	project, err := s.repository.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Project{}, Fingerprint{}, "", "", ErrProjectNotFound
		}
		return Project{}, Fingerprint{}, "", "", fmt.Errorf("load project: %w", err)
	}
	fingerprint, err := s.repository.GetLatestFingerprint(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Project{}, Fingerprint{}, "", "", ErrFingerprintNotFound
		}
		return Project{}, Fingerprint{}, "", "", fmt.Errorf("load fingerprint: %w", err)
	}
	return project, fingerprint.Fingerprint, fingerprint.SHA256, fingerprint.SourceRevision, nil
}

// RememberRequest asks Reusery to derive and store one explicit memory.
type RememberRequest struct {
	ProjectID          string
	PrimitiveID        string
	CandidateID        string
	Reason             policy.FeedbackReason
	SourceResolutionID int64
}

// Remember derives a preference from stored facts and records it.
//
// The returned bool is false when an identical active memory already existed,
// so remembering twice is idempotent rather than duplicative. Nothing here is
// inferred: the caller must ask explicitly, and a refine call or an outcome
// event never reaches this method.
func (s *Service) Remember(ctx context.Context, request RememberRequest) (Preference, bool, error) {
	if request.ProjectID == "" {
		return Preference{}, false, ErrProjectNotFound
	}
	if _, _, _, _, err := s.load(ctx, request.ProjectID); err != nil {
		return Preference{}, false, err
	}
	specimen, err := s.repository.GetSpecimen(ctx, request.CandidateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Preference{}, false, ErrPreferenceUnsupported
		}
		return Preference{}, false, fmt.Errorf("load specimen: %w", err)
	}
	evidence, err := s.repository.ListEvidenceBySubject(ctx, request.CandidateID)
	if err != nil {
		return Preference{}, false, fmt.Errorf("load evidence: %w", err)
	}
	if specimen.PrimitiveID != request.PrimitiveID {
		return Preference{}, false, fmt.Errorf("%w: candidate %q does not belong to primitive %q",
			ErrPreferenceUnsupported, request.CandidateID, request.PrimitiveID)
	}

	preference, err := DerivePreference(request.ProjectID, request.PrimitiveID, request.CandidateID,
		request.Reason, policy.ExtractFacts(specimen, evidence), request.SourceResolutionID, s.clock().UTC())
	if err != nil {
		return Preference{}, false, err
	}

	active, err := s.repository.ListActivePreferences(ctx, request.ProjectID, MaxActivePreferences+1)
	if err != nil {
		return Preference{}, false, fmt.Errorf("load preferences: %w", err)
	}
	for _, existing := range active {
		if samePreference(existing, preference) {
			return existing, false, nil
		}
	}
	if len(active) >= MaxActivePreferences {
		return Preference{}, false, fmt.Errorf("%w: more than %d active preferences", ErrBoundsExceeded, MaxActivePreferences)
	}

	id, err := s.repository.InsertPreference(ctx, preference)
	if err != nil {
		return Preference{}, false, fmt.Errorf("store preference: %w", err)
	}
	preference.ID = id
	return preference, true, nil
}

// samePreference reports whether two memories express the same active effect.
// Provenance fields are deliberately ignored: remembering the same thing
// twice from a different resolution must not create a duplicate.
func samePreference(a, b Preference) bool {
	if a.Kind != b.Kind || a.PrimitiveID != b.PrimitiveID || a.CandidateID != b.CandidateID ||
		a.TextValue != b.TextValue {
		return false
	}
	if (a.IntValue == nil) != (b.IntValue == nil) {
		return false
	}
	if a.IntValue != nil && *a.IntValue != *b.IntValue {
		return false
	}
	return true
}

// Forget revokes a preference without deleting it. Calling it twice is safe.
func (s *Service) Forget(ctx context.Context, projectID string, preferenceID int64) (Preference, error) {
	if projectID == "" {
		return Preference{}, ErrProjectNotFound
	}
	preference, err := s.repository.GetPreference(ctx, projectID, preferenceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Preference{}, ErrPreferenceNotFound
		}
		return Preference{}, fmt.Errorf("load preference: %w", err)
	}
	if !preference.Active() {
		return preference, nil
	}
	if err := s.repository.ForgetPreference(ctx, projectID, preferenceID, s.clock().UTC()); err != nil {
		return Preference{}, fmt.Errorf("forget preference: %w", err)
	}
	preference.ForgottenAt = s.clock().UTC()
	return preference, nil
}

// ListPreferences returns bounded stored memories, newest-independent id order.
func (s *Service) ListPreferences(ctx context.Context, projectID string, limit int) ([]Preference, error) {
	if projectID == "" {
		return nil, ErrProjectNotFound
	}
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		return nil, fmt.Errorf("%w: %d exceeds %d", ErrBoundsExceeded, limit, MaxHistoryLimit)
	}
	preferences, err := s.repository.ListPreferences(ctx, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("load preferences: %w", err)
	}
	return preferences, nil
}

// ListHistory returns bounded, newest-first decision history for a project.
func (s *Service) ListHistory(ctx context.Context, projectID string, limit int) ([]ProjectResolution, error) {
	if projectID == "" {
		return nil, ErrProjectNotFound
	}
	history, err := s.repository.ListResolutions(ctx, projectID, boundedHistory(limit))
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	return history, nil
}

// LoadContext resolves a stored context snapshot by its hash. A historical
// Resolution keeps pointing at the snapshot it actually saw, even after the
// project's fingerprint or preferences later change.
func (s *Service) LoadContext(ctx context.Context, hash string) (StoredContext, error) {
	if hash == "" {
		return StoredContext{}, ErrInvalidRequest
	}
	snapshot, err := s.repository.GetContext(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return StoredContext{}, ErrInvalidRequest
		}
		return StoredContext{}, fmt.Errorf("load project context: %w", err)
	}
	return snapshot, nil
}

func boundedHistory(limit int) int {
	if limit <= 0 {
		return DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		return MaxHistoryLimit
	}
	return limit
}
