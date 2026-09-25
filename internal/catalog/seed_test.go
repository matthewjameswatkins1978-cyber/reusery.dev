package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// seedStore is an in-memory Store with realistic append-only evidence
// semantics: a duplicate evidence ID is an error, exactly like PostgreSQL.
type seedStore struct {
	primitives map[string]model.Primitive
	contracts  map[string]model.Contract
	specimens  map[string]model.Specimen
	evidence   map[string]model.Evidence

	evidenceInserts int
}

func newSeedStore() *seedStore {
	return &seedStore{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string]model.Evidence{},
	}
}

func (s *seedStore) UpsertPrimitive(_ context.Context, value model.Primitive) error {
	s.primitives[value.ID] = value
	return nil
}

func (s *seedStore) UpsertContract(_ context.Context, value model.Contract) error {
	s.contracts[value.ID] = value
	return nil
}

func (s *seedStore) UpsertSpecimen(_ context.Context, value model.Specimen) error {
	s.specimens[value.ID] = value
	return nil
}

func (s *seedStore) InsertEvidence(_ context.Context, value model.Evidence) error {
	if _, exists := s.evidence[value.ID]; exists {
		return errors.New("evidence already exists")
	}
	s.evidence[value.ID] = value
	s.evidenceInserts++
	return nil
}

func (s *seedStore) FindEvidence(_ context.Context, id string) (model.Evidence, bool, error) {
	value, ok := s.evidence[id]
	return value, ok, nil
}

func sampleBundle() Bundle {
	return Bundle{
		Primitive: model.Primitive{
			ID:         "test/primitive",
			Name:       "Test primitive",
			Tags:       []string{"one", "two"},
			ContractID: "test/primitive/v1",
		},
		Contract: model.Contract{
			ID:          "test/primitive/v1",
			PrimitiveID: "test/primitive",
			Version:     "1",
			Requirements: []model.Requirement{
				{ID: "req-first", Required: true},
				{ID: "req-second", Required: true},
				{ID: "req-third", Required: false},
			},
		},
		Specimens: []model.Specimen{
			{ID: "specimen-a", PrimitiveID: "test/primitive", Name: "A", ReuseMode: []model.ReuseMode{model.ReuseCopy}},
			{ID: "specimen-b", PrimitiveID: "test/primitive", Name: "B", ReuseMode: []model.ReuseMode{model.ReuseDependency}},
		},
		Evidence: []model.Evidence{
			{
				ID:          "ev-one",
				SubjectID:   "specimen-a",
				Result:      model.EvidencePass,
				AppliesTo:   "req-first",
				Claim:       "original claim",
				ObservedAt:  time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				Source:      model.SourceRef{Path: "catalogue/specimens.yaml"},
				Methodology: "fixture",
			},
			{
				ID:          "ev-two",
				SubjectID:   "specimen-b",
				Result:      model.EvidenceUnknown,
				AppliesTo:   "req-second",
				Claim:       "second observation",
				ObservedAt:  time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				Source:      model.SourceRef{Path: "catalogue/specimens.yaml"},
				Methodology: "fixture",
			},
		},
	}
}

func TestSeedUpsertsCatalogueObjects(t *testing.T) {
	bundle := sampleBundle()
	store := newSeedStore()

	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	primitive, ok := store.primitives["test/primitive"]
	if !ok {
		t.Fatal("primitive was not upserted")
	}
	if len(primitive.Tags) != 2 || primitive.ContractID != "test/primitive/v1" {
		t.Errorf("primitive = %#v, want tags and contract preserved", primitive)
	}

	contract, ok := store.contracts["test/primitive/v1"]
	if !ok {
		t.Fatal("contract was not upserted")
	}
	if len(contract.Requirements) != len(bundle.Contract.Requirements) {
		t.Fatalf("requirements = %d, want %d", len(contract.Requirements), len(bundle.Contract.Requirements))
	}
	for i, want := range bundle.Contract.Requirements {
		if contract.Requirements[i].ID != want.ID || contract.Requirements[i].Required != want.Required {
			t.Errorf("requirement[%d] = %#v, want %#v (order must be preserved)", i, contract.Requirements[i], want)
		}
	}

	if len(store.specimens) != 2 {
		t.Errorf("specimens = %d, want 2", len(store.specimens))
	}
	if len(store.evidence) != len(bundle.Evidence) {
		t.Errorf("evidence = %d, want %d", len(store.evidence), len(bundle.Evidence))
	}
}

func TestSeedInsertsEvidenceAppendOnly(t *testing.T) {
	bundle := sampleBundle()
	store := newSeedStore()

	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if store.evidenceInserts != len(bundle.Evidence) {
		t.Errorf("evidence inserts = %d, want %d", store.evidenceInserts, len(bundle.Evidence))
	}
	if stored, ok := store.evidence["ev-one"]; !ok || stored.Claim != "original claim" {
		t.Errorf("stored evidence = %#v, want the original claim", stored)
	}
}

func TestSeedIsRepeatable(t *testing.T) {
	bundle := sampleBundle()
	store := newSeedStore()

	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("first Seed: %v", err)
	}
	firstInserts := store.evidenceInserts

	// Applying the identical bundle again must succeed without re-inserting.
	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("second Seed must be idempotent: %v", err)
	}
	if store.evidenceInserts != firstInserts {
		t.Errorf("evidence inserts grew from %d to %d", firstInserts, store.evidenceInserts)
	}
	if len(store.evidence) != len(bundle.Evidence) {
		t.Errorf("evidence count = %d, want %d", len(store.evidence), len(bundle.Evidence))
	}
	if len(store.specimens) != 2 || len(store.primitives) != 1 || len(store.contracts) != 1 {
		t.Errorf("catalogue objects duplicated: %d/%d/%d",
			len(store.primitives), len(store.contracts), len(store.specimens))
	}
}

func TestSeedRejectsChangedEvidence(t *testing.T) {
	bundle := sampleBundle()
	store := newSeedStore()

	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("first Seed: %v", err)
	}

	changed := sampleBundle()
	changed.Evidence[0].Claim = "a different claim for the same ID"

	err := Seed(context.Background(), changed, store)
	if !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("error = %v, want %v", err, ErrEvidenceConflict)
	}
	if stored := store.evidence["ev-one"]; stored.Claim != "original claim" {
		t.Errorf("existing evidence was overwritten: %#v", stored)
	}
}

func TestSeedAcceptsIdenticalContent(t *testing.T) {
	bundle := sampleBundle()
	store := newSeedStore()

	if err := Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("first Seed: %v", err)
	}
	// A freshly decoded copy of the same bytes must count as "already seeded",
	// even though it is a different in-memory value.
	if err := Seed(context.Background(), sampleBundle(), store); err != nil {
		t.Fatalf("Seed with identical content: %v", err)
	}
	if store.evidenceInserts != len(bundle.Evidence) {
		t.Errorf("evidence inserts = %d, want %d", store.evidenceInserts, len(bundle.Evidence))
	}
}
