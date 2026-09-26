package outcome

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// fakeRepository is an in-memory Repository with the optional behaviour the
// assertions care about.
type fakeRepository struct {
	resolutions map[int64]model.Resolution
	feedback    []StoredFeedback
	evidence    []model.Evidence
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{resolutions: map[int64]model.Resolution{
		1: {PrimitiveID: "p", ContractID: "c", Outcome: model.OutcomeDepend, ResolvedAt: time.Now()},
	}}
}

func (f *fakeRepository) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if value, ok := f.resolutions[id]; ok {
		return value, nil
	}
	return model.Resolution{}, store.ErrNotFound
}

func (f *fakeRepository) InsertFeedback(_ context.Context, value Feedback) (int64, error) {
	f.feedback = append(f.feedback, StoredFeedback{ID: int64(len(f.feedback) + 1), Feedback: value})
	return int64(len(f.feedback)), nil
}

func (f *fakeRepository) ListFeedback(_ context.Context, resolutionID int64, limit int) ([]StoredFeedback, error) {
	out := make([]StoredFeedback, 0, limit)
	for _, event := range f.feedback {
		if event.ResolutionID == resolutionID {
			out = append(out, event)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func TestKindsAreClosed(t *testing.T) {
	want := []Kind{
		KindAdopted, KindRejected, KindIntegrationSucceeded, KindIntegrationFailed, KindAbandoned,
	}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, kind := range want {
		if !ValidKind(kind) {
			t.Errorf("ValidKind(%q) = false", kind)
		}
	}
	if ValidKind(Kind("worked_great")) {
		t.Error("an invented kind was accepted")
	}
}

func TestValidateRejectsImpossibleReports(t *testing.T) {
	base := Feedback{ResolutionID: 1, Kind: KindAdopted, RecordedAt: time.Now()}

	cases := map[string]Feedback{
		"zero resolution":  {ResolutionID: 0, Kind: KindAdopted, RecordedAt: base.RecordedAt},
		"negative":         {ResolutionID: -1, Kind: KindAdopted, RecordedAt: base.RecordedAt},
		"unknown kind":     {ResolutionID: 1, Kind: Kind("nope"), RecordedAt: base.RecordedAt},
		"zero recorded_at": {ResolutionID: 1, Kind: KindAdopted},
	}
	for name, feedback := range cases {
		if err := Validate(feedback); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	overLimit := base
	overLimit.Note = strings.Repeat("a", MaxNoteLength+1)
	if err := Validate(overLimit); err == nil {
		t.Error("a note over the limit was accepted")
	}

	invalidUTF8 := base
	invalidUTF8.Note = string([]byte{0xff, 0xfe})
	if err := Validate(invalidUTF8); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

// TestNoteLimitCountsCharactersNotBytes pins the deterministic unit: the
// database constraint is char_length(note) <= 1000, and PostgreSQL's
// char_length counts characters.
func TestNoteLimitCountsCharactersNotBytes(t *testing.T) {
	feedback := Feedback{
		ResolutionID: 1,
		Kind:         KindAdopted,
		Note:         strings.Repeat("é", MaxNoteLength),
		RecordedAt:   time.Now(),
	}
	if err := Validate(feedback); err != nil {
		t.Errorf("a %d character note was rejected: %v", MaxNoteLength, err)
	}
	if len(feedback.Note) <= MaxNoteLength {
		t.Fatalf("the fixture should be over the limit in bytes (%d)", len(feedback.Note))
	}

	feedback.Note += "é"
	if err := Validate(feedback); err == nil {
		t.Error("a note of 1001 characters was accepted")
	}
}

func TestReportRequiresAKnownResolution(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, time.Now)

	_, err := service.Report(context.Background(), 99, KindAdopted, "")
	if !errors.Is(err, ErrResolutionNotFound) {
		t.Errorf("error = %v, want ErrResolutionNotFound", err)
	}
	if len(repo.feedback) != 0 {
		t.Errorf("recorded %d event(s) for a missing resolution", len(repo.feedback))
	}
}

func TestReportAppendsChronologicallyWithoutMutatingTheResolution(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, func() time.Time {
		return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	})
	before := repo.resolutions[1]

	for _, kind := range []Kind{KindAdopted, KindIntegrationSucceeded} {
		stored, err := service.Report(context.Background(), 1, kind, "actual result")
		if err != nil {
			t.Fatalf("report %s: %v", kind, err)
		}
		if stored.ID == 0 {
			t.Error("stored feedback has no identity")
		}
		if stored.Kind != kind {
			t.Errorf("kind = %q, want %q", stored.Kind, kind)
		}
	}

	if len(repo.feedback) != 2 {
		t.Fatalf("recorded %d event(s), want 2", len(repo.feedback))
	}
	if repo.feedback[0].Kind != KindAdopted || repo.feedback[1].Kind != KindIntegrationSucceeded {
		t.Errorf("events are out of order: %v", repo.feedback)
	}

	// The Resolution itself is untouched: outcome feedback is not Evidence,
	// not policy and never triggers a re-resolution.
	if repo.resolutions[1].Outcome != before.Outcome ||
		repo.resolutions[1].ResolvedAt != before.ResolvedAt ||
		repo.resolutions[1].PolicyID != before.PolicyID {
		t.Errorf("resolution changed: %+v", repo.resolutions[1])
	}
	if len(repo.evidence) != 0 {
		t.Errorf("reporting created %d evidence record(s)", len(repo.evidence))
	}
}

func TestListIsBoundedAndValidated(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, time.Now)

	if _, err := service.Report(context.Background(), 1, KindAdopted, ""); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := service.List(context.Background(), 1, 0); err != nil {
		t.Errorf("a zero limit should select the default: %v", err)
	}
	if _, err := service.List(context.Background(), 1, MaxListLimit+1); !errors.Is(err, ErrInvalidLimit) {
		t.Errorf("error = %v, want ErrInvalidLimit", err)
	}
	if _, err := service.List(context.Background(), 0, 10); !errors.Is(err, ErrInvalidResolutionID) {
		t.Errorf("error = %v, want ErrInvalidResolutionID", err)
	}

	events, err := service.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events = %d, want 1", len(events))
	}
}
