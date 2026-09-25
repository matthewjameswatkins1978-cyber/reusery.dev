package discovery

import (
	"errors"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

func TestPersistWritesSpecimensAndObservations(t *testing.T) {
	store := newFakeStore()
	candidate := validCandidate()

	if err := Persist(t.Context(), store, []Candidate{candidate}); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	stored, ok := store.specimens[candidate.Specimen.ID]
	if !ok {
		t.Fatal("specimen was not persisted")
	}
	if stored.PrimitiveID != candidate.Specimen.PrimitiveID || stored.Source != candidate.Specimen.Source {
		t.Errorf("stored specimen = %#v, want the discovered specimen", stored)
	}
	if len(store.evidence) != len(candidate.Evidence) {
		t.Errorf("evidence = %d, want %d", len(store.evidence), len(candidate.Evidence))
	}
	for _, evidence := range candidate.Evidence {
		if _, ok := store.evidence[evidence.ID]; !ok {
			t.Errorf("evidence %q was not persisted", evidence.ID)
		}
	}
}

func TestPersistIsRepeatable(t *testing.T) {
	store := newFakeStore()
	candidate := validCandidate()

	if err := Persist(t.Context(), store, []Candidate{candidate}); err != nil {
		t.Fatalf("first Persist: %v", err)
	}
	if err := Persist(t.Context(), store, []Candidate{candidate}); err != nil {
		t.Fatalf("second Persist must be idempotent: %v", err)
	}
	if len(store.evidence) != len(candidate.Evidence) {
		t.Errorf("evidence grew to %d, want %d", len(store.evidence), len(candidate.Evidence))
	}
	if len(store.specimens) != 1 {
		t.Errorf("specimens = %d, want 1", len(store.specimens))
	}
}

func TestPersistRejectsConflictingEvidence(t *testing.T) {
	store := newFakeStore()
	candidate := validCandidate()

	// Same ID, different content: the deterministic ID cannot change, so the
	// stored observation must not be silently overwritten.
	tampered := candidate.Evidence[0]
	tampered.Claim = "a claim the digest was computed over"
	store.evidence[tampered.ID] = tampered

	err := Persist(t.Context(), store, []Candidate{candidate})
	if !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("error = %v, want ErrEvidenceConflict", err)
	}
	if store.evidence[tampered.ID].Claim != tampered.Claim {
		t.Errorf("existing evidence was overwritten: %#v", store.evidence[tampered.ID])
	}
	// Discovery persistence is deliberately not one giant transaction: the
	// specimen upsert already happened and is harmless, while the conflicting
	// observation is reported so a corrected rerun can continue.
	if len(store.specimens) != 1 {
		t.Errorf("specimens = %d, want 1 (upserts are resumable)", len(store.specimens))
	}
}

func TestPersistPropagatesStoreFailures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*fakeStore)
	}{
		{"specimen upsert", func(s *fakeStore) { s.upsertSpecimenErr = errors.New("database is down") }},
		{"evidence lookup", func(s *fakeStore) { s.findEvidenceErr = errors.New("database is down") }},
		{"evidence insert", func(s *fakeStore) { s.insertEvidenceErr = errors.New("database is down") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			tt.prepare(store)
			if err := Persist(t.Context(), store, []Candidate{validCandidate()}); err == nil {
				t.Fatal("Persist succeeded, want an error")
			}
		})
	}
}

func TestPersistKeepsObservationHistoryAcrossRuns(t *testing.T) {
	store := newFakeStore()
	candidate := validCandidate()
	if err := Persist(t.Context(), store, []Candidate{candidate}); err != nil {
		t.Fatalf("first Persist: %v", err)
	}

	// A later real discovery run observes at a new time, producing a new
	// observation identity rather than rewriting history.
	next := candidate
	next.Evidence = []model.Evidence{NewObservation(ObservationSpec{
		ProviderID:  next.ProviderID,
		SubjectID:   next.Specimen.ID,
		Kind:        "discovery_match",
		Claim:       next.Evidence[0].Claim,
		Result:      model.EvidenceInfo,
		Source:      next.Evidence[0].Source,
		ObservedAt:  testObservedAt.Add(time.Hour),
		Methodology: next.Evidence[0].Methodology,
		Artifact:    next.Evidence[0].Artifact,
	})}
	if err := Persist(t.Context(), store, []Candidate{next}); err != nil {
		t.Fatalf("second Persist: %v", err)
	}
	if len(store.evidence) != 2 {
		t.Errorf("evidence = %d, want 2 distinct observations", len(store.evidence))
	}
}
