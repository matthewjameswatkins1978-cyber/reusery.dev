package discovery

import (
	"context"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// fakeStore is an in-memory Store for discovery tests: no PostgreSQL, no
// network. Evidence behaves like the real registry — append-only, with a
// duplicate ID being an error.
type fakeStore struct {
	primitives map[string]model.Primitive
	contracts  map[string]model.Contract
	specimens  map[string]model.Specimen
	evidence   map[string]model.Evidence

	upsertSpecimenErr error
	findEvidenceErr   error
	insertEvidenceErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string]model.Evidence{},
	}
}

// newSeededStore returns a store already holding the bounded-subprocess
// primitive and contract, which every discovery test runs against.
func newSeededStore() *fakeStore {
	store := newFakeStore()
	primitive := model.Primitive{
		ID:          "process/bounded-subprocess",
		Name:        "Bounded subprocess execution",
		ContractID:  "process/bounded-subprocess/v1",
		Description: "Execute a child process while bounding captured output and lifetime.",
	}
	contract := model.Contract{
		ID:          "process/bounded-subprocess/v1",
		PrimitiveID: "process/bounded-subprocess",
		Version:     "1",
		Summary:     "Safely execute a subprocess without unbounded output or lifetime.",
		Requirements: []model.Requirement{
			{ID: "starts-requested-program", Kind: "behavior", Required: true},
			{ID: "supports-cancellation", Kind: "lifecycle", Required: true},
			{ID: "bounds-stdout", Kind: "resource", Required: true},
		},
	}
	store.primitives[primitive.ID] = primitive
	store.contracts[contract.ID] = contract
	return store
}

func (f *fakeStore) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	value, ok := f.primitives[id]
	if !ok {
		return model.Primitive{}, fmt.Errorf("primitive %q not found", id)
	}
	return value, nil
}

func (f *fakeStore) GetContract(_ context.Context, id string) (model.Contract, error) {
	value, ok := f.contracts[id]
	if !ok {
		return model.Contract{}, fmt.Errorf("contract %q not found", id)
	}
	return value, nil
}

func (f *fakeStore) UpsertSpecimen(_ context.Context, value model.Specimen) error {
	if f.upsertSpecimenErr != nil {
		return f.upsertSpecimenErr
	}
	f.specimens[value.ID] = value
	return nil
}

func (f *fakeStore) FindEvidence(_ context.Context, id string) (model.Evidence, bool, error) {
	if f.findEvidenceErr != nil {
		return model.Evidence{}, false, f.findEvidenceErr
	}
	value, ok := f.evidence[id]
	return value, ok, nil
}

func (f *fakeStore) InsertEvidence(_ context.Context, value model.Evidence) error {
	if f.insertEvidenceErr != nil {
		return f.insertEvidenceErr
	}
	if _, exists := f.evidence[value.ID]; exists {
		return fmt.Errorf("evidence %q already exists", value.ID)
	}
	f.evidence[value.ID] = value
	return nil
}
