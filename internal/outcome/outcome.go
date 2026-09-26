// Package outcome records what actually happened after a Resolution reached
// an agent or a human.
//
// This is factual outcome feedback. It is deliberately NOT behavioural
// Evidence: it never satisfies or fails a contract requirement, never changes
// a policy, never re-runs a decision and never becomes a ranking signal.
// Recording an outcome only appends a fact to a log. Packet 10 may later
// decide how remembered project preferences learn from this history; Packet 9
// only records the facts.
package outcome

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// Kind is one factual post-resolution event.
type Kind string

// The closed feedback vocabulary. Adding a kind is a schema change, never an
// open-ended string.
const (
	// KindAdopted means the caller decided to use the resolved route.
	KindAdopted Kind = "adopted"
	// KindRejected means the caller ultimately did not use the resolved route.
	KindRejected Kind = "rejected"
	// KindIntegrationSucceeded means implementation or integration completed
	// successfully. It must not be reported merely because code was written.
	KindIntegrationSucceeded Kind = "integration_succeeded"
	// KindIntegrationFailed means an attempted integration failed.
	KindIntegrationFailed Kind = "integration_failed"
	// KindAbandoned means work stopped without a success or failure conclusion.
	KindAbandoned Kind = "abandoned"
)

// MaxNoteLength is the note limit in Unicode characters (runes).
//
// Characters, not bytes: the database constraint is char_length(note) <= 1000,
// and PostgreSQL's char_length counts characters. Counting runes here keeps the
// domain and the database agreeing instead of one silently truncating the
// other.
const MaxNoteLength = 1000

// Bounds for listing stored feedback.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Validation and lookup failures.
var (
	ErrInvalidResolutionID = errors.New("outcome: resolution id must be positive")
	ErrInvalidKind         = errors.New("outcome: unsupported feedback kind")
	ErrNoteTooLong         = errors.New("outcome: note exceeds the length limit")
	ErrNoteNotUTF8         = errors.New("outcome: note is not valid UTF-8")
	ErrInvalidLimit        = errors.New("outcome: limit is out of range")
	ErrResolutionNotFound  = errors.New("outcome: resolution does not exist")
)

// Kinds returns the supported kinds in their authored order.
func Kinds() []Kind {
	return []Kind{
		KindAdopted,
		KindRejected,
		KindIntegrationSucceeded,
		KindIntegrationFailed,
		KindAbandoned,
	}
}

// ValidKind reports whether a kind is part of the closed vocabulary.
func ValidKind(kind Kind) bool {
	for _, supported := range Kinds() {
		if kind == supported {
			return true
		}
	}
	return false
}

// Feedback is one post-resolution fact, not yet stored.
type Feedback struct {
	ResolutionID int64
	Kind         Kind
	Note         string
	RecordedAt   time.Time
}

// StoredFeedback is one persisted post-resolution fact.
type StoredFeedback struct {
	ID int64
	Feedback
}

// Repository is the persistence boundary. It is satisfied by the PostgreSQL
// store; no transport package needs to know that storage exists.
type Repository interface {
	// GetResolution returns ErrResolutionNotFound when the id is unknown.
	GetResolution(ctx context.Context, id int64) (model.Resolution, error)
	InsertFeedback(ctx context.Context, f Feedback) (int64, error)
	ListFeedback(ctx context.Context, resolutionID int64, limit int) ([]StoredFeedback, error)
}

// Validate rejects a structurally impossible report before it reaches storage.
func Validate(f Feedback) error {
	if f.ResolutionID <= 0 {
		return ErrInvalidResolutionID
	}
	if !ValidKind(f.Kind) {
		return fmt.Errorf("%w %q", ErrInvalidKind, string(f.Kind))
	}
	if !utf8.ValidString(f.Note) {
		return ErrNoteNotUTF8
	}
	if utf8.RuneCountInString(f.Note) > MaxNoteLength {
		return fmt.Errorf("%w: %d characters, limit %d", ErrNoteTooLong,
			utf8.RuneCountInString(f.Note), MaxNoteLength)
	}
	if f.RecordedAt.IsZero() {
		return errors.New("outcome: recorded_at is zero")
	}
	return nil
}

// Service appends outcome facts for stored Resolutions.
type Service struct {
	repository Repository
	clock      func() time.Time
}

// NewService builds the outcome service over an existing repository boundary.
func NewService(repository Repository, clock func() time.Time) *Service {
	return &Service{repository: repository, clock: clock}
}

// Report records one fact about an already persisted Resolution.
//
// It refuses unknown Resolution ids so a typo cannot silently create a log
// that nothing can ever be attached to. Reporting never modifies the
// Resolution, never creates Evidence and never re-resolves anything.
func (s *Service) Report(ctx context.Context, resolutionID int64, kind Kind, note string) (StoredFeedback, error) {
	feedback := Feedback{
		ResolutionID: resolutionID,
		Kind:         kind,
		Note:         note,
		RecordedAt:   s.clock().UTC(),
	}
	if err := Validate(feedback); err != nil {
		return StoredFeedback{}, err
	}
	if _, err := s.repository.GetResolution(ctx, resolutionID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return StoredFeedback{}, fmt.Errorf("%w: %d", ErrResolutionNotFound, resolutionID)
		}
		return StoredFeedback{}, err
	}
	id, err := s.repository.InsertFeedback(ctx, feedback)
	if err != nil {
		return StoredFeedback{}, fmt.Errorf("store outcome feedback: %w", err)
	}
	return StoredFeedback{ID: id, Feedback: feedback}, nil
}

// List returns stored feedback for one Resolution in chronological order.
func (s *Service) List(ctx context.Context, resolutionID int64, limit int) ([]StoredFeedback, error) {
	if resolutionID <= 0 {
		return nil, ErrInvalidResolutionID
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		return nil, fmt.Errorf("%w: %d exceeds the maximum of %d", ErrInvalidLimit, limit, MaxListLimit)
	}
	stored, err := s.repository.ListFeedback(ctx, resolutionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list outcome feedback: %w", err)
	}
	return stored, nil
}
