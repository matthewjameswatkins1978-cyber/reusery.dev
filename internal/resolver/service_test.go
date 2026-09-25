package resolver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// errFakeLoad is returned by the fake repository to simulate a load failure.
var errFakeLoad = errors.New("fake repository: load failed")

type fakeRepository struct {
	primitives  map[string]model.Primitive
	contracts   map[string]model.Contract
	specimens   map[string]model.Specimen
	evidence    map[string][]model.Evidence
	resolutions map[int64]model.Resolution

	failSpecimenID string
	insertErr      error

	inserted []model.Resolution
	nextID   int64
}

func newFakeRepository() *fakeRepository {
	contract := kernelContract()

	good := kernelSpecimen("spec-good", model.ReuseDependency)
	bad := kernelSpecimen("spec-bad", model.ReuseCopy)

	return &fakeRepository{
		primitives: map[string]model.Primitive{contract.PrimitiveID: kernelPrimitive()},
		contracts:  map[string]model.Contract{contract.ID: contract},
		specimens: map[string]model.Specimen{
			good.ID: good,
			bad.ID:  bad,
		},
		evidence: map[string][]model.Evidence{
			good.ID: passEvidence(good.ID, contract),
			bad.ID: {
				evidenceFor(bad.ID, "bounds-stdout", "ev/bad/stdout", model.EvidenceFail),
				evidenceFor(bad.ID, "bounds-stderr", "ev/bad/stderr", model.EvidencePass),
				evidenceFor(bad.ID, "supports-timeout", "ev/bad/timeout", model.EvidencePass),
			},
		},
		resolutions: map[int64]model.Resolution{},
		nextID:      1,
	}
}

func (r *fakeRepository) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	primitive, ok := r.primitives[id]
	if !ok {
		return model.Primitive{}, errFakeLoad
	}
	return primitive, nil
}

func (r *fakeRepository) GetContract(_ context.Context, id string) (model.Contract, error) {
	contract, ok := r.contracts[id]
	if !ok {
		return model.Contract{}, errFakeLoad
	}
	return contract, nil
}

func (r *fakeRepository) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	if r.failSpecimenID != "" && r.failSpecimenID == id {
		return model.Specimen{}, errFakeLoad
	}
	specimen, ok := r.specimens[id]
	if !ok {
		return model.Specimen{}, errFakeLoad
	}
	return specimen, nil
}

func (r *fakeRepository) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	return r.evidence[id], nil
}

func (r *fakeRepository) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	if r.insertErr != nil {
		return 0, r.insertErr
	}
	id := r.nextID
	r.nextID++
	r.inserted = append(r.inserted, resolution)
	r.resolutions[id] = resolution
	return id, nil
}

func (r *fakeRepository) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	resolution, ok := r.resolutions[id]
	if !ok {
		return model.Resolution{}, errors.New("fake repository: resolution not found")
	}
	return resolution, nil
}

func fixedClock() Clock {
	return func() time.Time {
		return time.Date(2026, time.September, 25, 16, 0, 0, 0, time.FixedZone("test", 3600))
	}
}

func goodRequest() Request {
	return Request{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates: []CandidateRef{
			{SpecimenID: "spec-bad", ReuseMode: model.ReuseCopy},
			{SpecimenID: "spec-good", ReuseMode: model.ReuseDependency},
		},
	}
}

// A successful resolution is persisted exactly once and the storage ID is
// returned unchanged.
func TestServiceResolvePersistsExactlyOnce(t *testing.T) {
	repository := newFakeRepository()
	repository.nextID = 42
	service := NewService(repository, fixedClock())

	stored, err := service.Resolve(context.Background(), goodRequest())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if stored.ResolutionID != 42 {
		t.Errorf("resolution ID = %d, want 42", stored.ResolutionID)
	}
	if stored.Decision.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q, want depend", stored.Decision.Resolution.Outcome)
	}
	if stored.Decision.Resolution.SpecimenID != "spec-good" {
		t.Errorf("selected = %q, want spec-good", stored.Decision.Resolution.SpecimenID)
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(repository.inserted))
	}
	if !reflect.DeepEqual(repository.inserted[0], stored.Decision.Resolution) {
		t.Errorf("persisted resolution differs from returned decision")
	}

	// The kernel really ran: the unsuitable candidate is inspected first.
	if len(stored.Decision.Considered) != 2 {
		t.Fatalf("considered = %d, want 2", len(stored.Decision.Considered))
	}
	if stored.Decision.Considered[0].SpecimenID != "spec-bad" || stored.Decision.Considered[0].Selected {
		t.Errorf("first considered = %#v, want rejected spec-bad", stored.Decision.Considered[0])
	}
	if !stored.Decision.Considered[1].Selected {
		t.Error("second candidate was not selected")
	}
}

// BUILD LOCALLY is a successful resolution and is persisted too.
func TestServiceResolveBuildLocallyPersisted(t *testing.T) {
	repository := newFakeRepository()
	service := NewService(repository, fixedClock())

	request := goodRequest()
	request.Candidates = request.Candidates[:1] // only the unsuitable candidate

	stored, err := service.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("Resolve must not error on BUILD LOCALLY: %v", err)
	}
	if stored.Decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", stored.Decision.Resolution.Outcome)
	}
	if len(repository.inserted) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(repository.inserted))
	}
}

// A repository load failure persists nothing.
func TestServiceLoadFailurePersistsNothing(t *testing.T) {
	repository := newFakeRepository()
	repository.failSpecimenID = "spec-good"
	service := NewService(repository, fixedClock())

	if _, err := service.Resolve(context.Background(), goodRequest()); err == nil {
		t.Fatal("expected an error when a specimen cannot be loaded")
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want 0", len(repository.inserted))
	}
}

// Malformed input is rejected by the kernel and persists nothing.
func TestServiceMalformedInputPersistsNothing(t *testing.T) {
	repository := newFakeRepository()
	service := NewService(repository, fixedClock())

	request := goodRequest()
	// Duplicate candidate IDs are malformed.
	request.Candidates = append(request.Candidates, request.Candidates[1])

	if _, err := service.Resolve(context.Background(), request); !errors.Is(err, ErrDuplicateCandidateID) {
		t.Fatalf("error = %v, want %v", err, ErrDuplicateCandidateID)
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("persisted %d resolutions, want 0", len(repository.inserted))
	}
}

// A persistence failure is returned and success is not claimed.
func TestServicePersistenceErrorReturned(t *testing.T) {
	repository := newFakeRepository()
	repository.insertErr = errors.New("database is down")
	service := NewService(repository, fixedClock())

	_, err := service.Resolve(context.Background(), goodRequest())
	if err == nil {
		t.Fatal("expected an error when persistence fails")
	}
	if !strings.Contains(err.Error(), "persist resolution") {
		t.Errorf("error = %v, want it to mention persistence", err)
	}
	if !strings.Contains(err.Error(), "database is down") {
		t.Errorf("error = %v, want the underlying cause preserved", err)
	}
}

// The injected clock reaches Resolution.ResolvedAt, normalised to UTC.
func TestServiceInjectedClockReachesResolution(t *testing.T) {
	repository := newFakeRepository()
	service := NewService(repository, fixedClock())

	stored, err := service.Resolve(context.Background(), goodRequest())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got := stored.Decision.Resolution.ResolvedAt
	want := fixedClock()()
	if !got.Equal(want) {
		t.Errorf("resolvedAt = %v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("resolvedAt location = %v, want UTC", got.Location())
	}
	if repository.inserted[0].ResolvedAt != got {
		t.Errorf("persisted timestamp = %v, want %v", repository.inserted[0].ResolvedAt, got)
	}
}

// Resolution loads the persisted decision without re-evaluating anything.
func TestServiceResolutionLookup(t *testing.T) {
	repository := newFakeRepository()
	service := NewService(repository, fixedClock())

	stored, err := service.Resolve(context.Background(), goodRequest())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	loaded, err := service.Resolution(context.Background(), stored.ResolutionID)
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if !reflect.DeepEqual(loaded, stored.Decision.Resolution) {
		t.Errorf("loaded = %#v, want %#v", loaded, stored.Decision.Resolution)
	}

	if _, err := service.Resolution(context.Background(), 999); err == nil {
		t.Error("expected an error for an unknown resolution ID")
	}
}
