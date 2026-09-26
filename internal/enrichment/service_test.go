package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

type fakeStore struct {
	specimens map[string]model.Specimen
	evidence  map[string]model.Evidence
	order     []string

	insertErr error
	listErr   error
	getErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		specimens: map[string]model.Specimen{},
		evidence:  map[string]model.Evidence{},
	}
}

func (s *fakeStore) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	if s.getErr != nil {
		return model.Specimen{}, s.getErr
	}
	specimen, ok := s.specimens[id]
	if !ok {
		return model.Specimen{}, errors.New("fake store: specimen not found")
	}
	return specimen, nil
}

func (s *fakeStore) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	items := make([]model.Evidence, 0)
	for _, key := range s.order {
		if s.evidence[key].SubjectID == id {
			items = append(items, s.evidence[key])
		}
	}
	return items, nil
}

func (s *fakeStore) FindEvidence(_ context.Context, id string) (model.Evidence, bool, error) {
	item, ok := s.evidence[id]
	return item, ok, nil
}

func (s *fakeStore) InsertEvidence(_ context.Context, item model.Evidence) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.evidence[item.ID] = item
	s.order = append(s.order, item.ID)
	return nil
}

type fakeProvider struct {
	id       string
	supports func(model.Specimen) bool
	result   ProviderResult
	err      error

	calls    int
	requests []Request
}

func (p *fakeProvider) ID() string { return p.id }

func (p *fakeProvider) Supports(specimen model.Specimen) bool {
	if p.supports == nil {
		return true
	}
	return p.supports(specimen)
}

func (p *fakeProvider) Enrich(_ context.Context, request Request) (ProviderResult, error) {
	p.calls++
	p.requests = append(p.requests, request)
	return p.result, p.err
}

func publicSpecimen(id string) model.Specimen {
	return model.Specimen{
		ID:          id,
		PrimitiveID: "process/bounded-subprocess",
		Name:        id,
		Source:      model.SourceRef{URL: "https://example.test/" + id, Revision: "v1"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
}

func storeWith(specimens ...model.Specimen) *fakeStore {
	store := newFakeStore()
	for _, specimen := range specimens {
		store.specimens[specimen.ID] = specimen
	}
	return store
}

func clock() time.Time { return observedAt }

func TestEnrichRejectsMalformedSpecimenLists(t *testing.T) {
	store := storeWith(publicSpecimen("a"))
	service := NewService(store, clock, nil)

	cases := map[string][]string{
		"empty": nil,
		"blank": {""},
		"dupe":  {"a", "a"},
		"too many": func() []string {
			ids := make([]string, 0, DefaultBudget().MaxSpecimens+1)
			for i := 0; i <= DefaultBudget().MaxSpecimens; i++ {
				ids = append(ids, "a")
			}
			return ids
		}(),
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Enrich(context.Background(), ids); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestEnrichPassesBoundedContextToProviders(t *testing.T) {
	specimen := publicSpecimen("public/pkg.go.dev/example.test/pkg@v1.0.0")
	existing := model.Evidence{ID: "ev/1", SubjectID: specimen.ID, Kind: "package_module"}
	store := storeWith(specimen)
	store.evidence[existing.ID] = existing
	store.order = []string{existing.ID}

	provider := &fakeProvider{id: ProviderDepsDev}
	service := NewService(store, clock, []Provider{provider})

	if _, err := service.Enrich(context.Background(), []string{specimen.ID}); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("calls = %d, want 1", provider.calls)
	}

	request := provider.requests[0]
	if request.Specimen.ID != specimen.ID {
		t.Errorf("specimen = %q", request.Specimen.ID)
	}
	if !request.ObservedAt.Equal(observedAt) {
		t.Errorf("observed at = %v", request.ObservedAt)
	}
	if len(request.ExistingEvidence) != 1 || request.ExistingEvidence[0].ID != existing.ID {
		t.Errorf("existing evidence = %+v", request.ExistingEvidence)
	}
	if request.Budget.MaxHTTPRequests != DefaultBudget().MaxHTTPRequests {
		t.Errorf("budget = %+v", request.Budget)
	}
}

func TestOneProviderFailingDoesNotEraseAnother(t *testing.T) {
	specimen := publicSpecimen("a")
	failing := &fakeProvider{id: ProviderDepsDev, err: errors.New("rate limited")}
	succeeding := &fakeProvider{id: ProviderGitHubMetadata, result: ProviderResult{
		Evidence: []model.Evidence{NewObservation(ObservationSpec{
			ProviderID:  ProviderGitHubMetadata,
			SubjectID:   specimen.ID,
			Kind:        KindRepositoryArchived,
			Claim:       "repository archived=false",
			Result:      model.EvidenceInfo,
			Source:      model.SourceRef{URL: "https://example.test/a"},
			ObservedAt:  observedAt,
			Methodology: MethodologyGitHubMetadata,
		})},
		Requests: 1,
	}}

	store := storeWith(specimen)
	service := NewService(store, clock, []Provider{failing, succeeding})
	result, err := service.Enrich(context.Background(), []string{specimen.ID})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(result.Evidence) != 1 {
		t.Fatalf("evidence = %+v", result.Evidence)
	}
	if len(store.evidence) != 1 {
		t.Errorf("persisted = %d, want 1", len(store.evidence))
	}
	if result.Specimens[0].Providers[0].Succeeded {
		t.Error("the failing provider must be reported as failed")
	}
	if len(result.Specimens[0].Providers[0].Issues) == 0 {
		t.Error("the failure must be inspectable")
	}
}

func TestEveryApplicableProviderFailingIsAnExecutionFailure(t *testing.T) {
	specimen := publicSpecimen("a")
	first := &fakeProvider{id: ProviderDepsDev, err: errors.New("boom")}
	second := &fakeProvider{id: ProviderGitHubMetadata, err: errors.New("boom")}

	store := storeWith(specimen)
	service := NewService(store, clock, []Provider{first, second})
	result, err := service.Enrich(context.Background(), []string{specimen.ID})
	if err == nil {
		t.Fatal("expected an execution failure")
	}
	var failure *ProvidersFailedError
	if !errors.As(err, &failure) {
		t.Errorf("err = %v, want ProvidersFailedError", err)
	}
	if len(result.Specimens) != 1 {
		t.Errorf("provider reports must survive the failure: %+v", result.Specimens)
	}
	if len(store.evidence) != 0 {
		t.Errorf("nothing should be persisted: %d", len(store.evidence))
	}
}

func TestUnsupportedSpecimenProducesNoManufacturedEvidence(t *testing.T) {
	specimen := publicSpecimen("fixture/process/bounded-subprocess/complete-dependency")
	store := storeWith(specimen)
	provider := &fakeProvider{id: ProviderDepsDev, supports: func(model.Specimen) bool { return false }}
	service := NewService(store, clock, []Provider{provider})

	result, err := service.Enrich(context.Background(), []string{specimen.ID})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	report := result.Specimens[0]
	if report.Supported {
		t.Error("supported = true, want false")
	}
	if provider.calls != 0 {
		t.Errorf("provider calls = %d, want 0", provider.calls)
	}
	if len(report.Providers) != 1 || report.Providers[0].Issues[0].Kind != IssueUnsupported {
		t.Errorf("report = %+v", report)
	}
	if len(result.Evidence) != 0 {
		t.Errorf("evidence = %+v, want none", result.Evidence)
	}
}

func TestUnsafeProviderOutputIsRejectedBeforePersistence(t *testing.T) {
	specimen := publicSpecimen("a")
	unsafe := &fakeProvider{id: ProviderDepsDev, result: ProviderResult{Evidence: []model.Evidence{{
		ID:         "enrichment/deps.dev/bad",
		SubjectID:  specimen.ID,
		Kind:       "behavioural",
		Claim:      "it passes",
		Result:     model.EvidencePass,
		Source:     model.SourceRef{URL: "https://example.test/a"},
		ObservedAt: observedAt,
	}}}}
	store := storeWith(specimen)
	service := NewService(store, clock, []Provider{unsafe})

	if _, err := service.Enrich(context.Background(), []string{specimen.ID}); !errors.Is(err, ErrUnsafeOutput) {
		t.Errorf("err = %v, want ErrUnsafeOutput", err)
	}
	if len(store.evidence) != 0 {
		t.Errorf("unsafe evidence was persisted: %d", len(store.evidence))
	}
}

func TestPersistenceIsIdempotentForTheSameObservationInstant(t *testing.T) {
	specimen := publicSpecimen("a")
	evidence := NewObservation(ObservationSpec{
		ProviderID:  ProviderDepsDev,
		SubjectID:   specimen.ID,
		Kind:        KindPackageVersion,
		Claim:       `deps.dev reported version "v1.0.0"`,
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://example.test/a"},
		ObservedAt:  observedAt,
		Methodology: MethodologyDepsDev,
	})
	store := storeWith(specimen)
	provider := &fakeProvider{id: ProviderDepsDev, result: ProviderResult{Evidence: []model.Evidence{evidence}}}
	service := NewService(store, clock, []Provider{provider})

	for run := 0; run < 2; run++ {
		if _, err := service.Enrich(context.Background(), []string{specimen.ID}); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if len(store.evidence) != 1 {
		t.Errorf("stored = %d records, want 1", len(store.evidence))
	}
}

func TestConflictingContentForTheSameEvidenceIDIsAnError(t *testing.T) {
	specimen := publicSpecimen("a")
	evidence := NewObservation(ObservationSpec{
		ProviderID:  ProviderDepsDev,
		SubjectID:   specimen.ID,
		Kind:        KindPackageVersion,
		Claim:       `deps.dev reported version "v1.0.0"`,
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://example.test/a"},
		ObservedAt:  observedAt,
		Methodology: MethodologyDepsDev,
	})
	store := storeWith(specimen)
	store.evidence[evidence.ID] = evidence
	store.order = []string{evidence.ID}

	changed := evidence
	changed.Claim = "different claim"
	if err := Persist(context.Background(), store, []model.Evidence{changed}); !errors.Is(err, ErrEvidenceConflict) {
		t.Errorf("err = %v, want ErrEvidenceConflict", err)
	}
}

func TestProviderBudgetBoundsAreThePacketDefaults(t *testing.T) {
	budget := DefaultBudget()
	if budget.MaxSpecimens != 24 || budget.MaxProviders != 2 || budget.MaxHTTPRequests != 3 {
		t.Errorf("budget = %+v", budget)
	}
	if budget.Timeout != 10*time.Second || budget.RunTimeout != 30*time.Second {
		t.Errorf("timeouts = %v / %v", budget.Timeout, budget.RunTimeout)
	}
	if budget.MaxResponseBytes != 2<<20 {
		t.Errorf("max response bytes = %d", budget.MaxResponseBytes)
	}
}

func TestKnownProviderIDsAreExactlyTheTwoThisPacketBuilds(t *testing.T) {
	ids := KnownProviderIDs()
	if len(ids) != 2 || ids[0] != ProviderDepsDev || ids[1] != ProviderGitHubMetadata {
		t.Errorf("ids = %v", ids)
	}
}

func TestStorageFailureIsReported(t *testing.T) {
	specimen := publicSpecimen("a")
	store := storeWith(specimen)
	store.getErr = errors.New("database down")
	service := NewService(store, clock, nil)

	if _, err := service.Enrich(context.Background(), []string{specimen.ID}); err == nil {
		t.Fatal("expected an error")
	}
}
