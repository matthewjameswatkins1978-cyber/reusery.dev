package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// fakeRepo is an in-memory project.Repository so the real project service and
// the real Packet 7 quality service can run without a database.
type fakeRepo struct {
	primitives   map[string]model.Primitive
	contracts    map[string]model.Contract
	specimens    map[string]model.Specimen
	evidence     map[string][]model.Evidence
	resolutions  []model.Resolution
	projects     map[string]Project
	fingerprints []StoredFingerprint
	preferences  []Preference
	contexts     map[string]StoredContext
	// failInsert makes persistence fail so error paths can be asserted.
	failInsert bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string][]model.Evidence{},
		projects:   map[string]Project{},
		contexts:   map[string]StoredContext{},
	}
}

// resolver.Repository

func (f *fakeRepo) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	if value, ok := f.primitives[id]; ok {
		return value, nil
	}
	return model.Primitive{}, store.ErrNotFound
}

func (f *fakeRepo) GetContract(_ context.Context, id string) (model.Contract, error) {
	if value, ok := f.contracts[id]; ok {
		return value, nil
	}
	return model.Contract{}, store.ErrNotFound
}

func (f *fakeRepo) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	if value, ok := f.specimens[id]; ok {
		return value, nil
	}
	return model.Specimen{}, store.ErrNotFound
}

func (f *fakeRepo) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	return append([]model.Evidence(nil), f.evidence[id]...), nil
}

func (f *fakeRepo) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	if f.failInsert {
		return 0, errors.New("insert failed")
	}
	f.resolutions = append(f.resolutions, resolution)
	return int64(len(f.resolutions)), nil
}

func (f *fakeRepo) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if id < 1 || id > int64(len(f.resolutions)) {
		return model.Resolution{}, store.ErrNotFound
	}
	return f.resolutions[id-1], nil
}

// project repository

func (f *fakeRepo) UpsertProject(_ context.Context, project Project) error {
	if existing, ok := f.projects[project.ID]; ok {
		project.CreatedAt = existing.CreatedAt
		f.projects[project.ID] = project
		return nil
	}
	f.projects[project.ID] = project
	return nil
}

func (f *fakeRepo) GetProject(_ context.Context, id string) (Project, error) {
	if value, ok := f.projects[id]; ok {
		return value, nil
	}
	return Project{}, store.ErrNotFound
}

func (f *fakeRepo) InsertFingerprint(_ context.Context, fingerprint StoredFingerprint) (int64, error) {
	fingerprint.ID = int64(len(f.fingerprints) + 1)
	f.fingerprints = append(f.fingerprints, fingerprint)
	return fingerprint.ID, nil
}

func (f *fakeRepo) GetFingerprintByHash(_ context.Context, projectID, sha string) (StoredFingerprint, error) {
	for _, fingerprint := range f.fingerprints {
		if fingerprint.ProjectID == projectID && fingerprint.SHA256 == sha {
			return fingerprint, nil
		}
	}
	return StoredFingerprint{}, store.ErrNotFound
}

func (f *fakeRepo) GetLatestFingerprint(_ context.Context, projectID string) (StoredFingerprint, error) {
	for index := len(f.fingerprints) - 1; index >= 0; index-- {
		if f.fingerprints[index].ProjectID == projectID {
			return f.fingerprints[index], nil
		}
	}
	return StoredFingerprint{}, store.ErrNotFound
}

func (f *fakeRepo) InsertPreference(_ context.Context, preference Preference) (int64, error) {
	preference.ID = int64(len(f.preferences) + 1)
	f.preferences = append(f.preferences, preference)
	return preference.ID, nil
}

func (f *fakeRepo) GetPreference(_ context.Context, projectID string, id int64) (Preference, error) {
	for _, preference := range f.preferences {
		if preference.ProjectID == projectID && preference.ID == id {
			return preference, nil
		}
	}
	return Preference{}, store.ErrNotFound
}

func (f *fakeRepo) ForgetPreference(_ context.Context, projectID string, id int64, at time.Time) error {
	for index := range f.preferences {
		if f.preferences[index].ProjectID == projectID && f.preferences[index].ID == id {
			f.preferences[index].ForgottenAt = at
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeRepo) ListPreferences(_ context.Context, projectID string, limit int) ([]Preference, error) {
	out := make([]Preference, 0, limit)
	for _, preference := range f.preferences {
		if preference.ProjectID == projectID {
			out = append(out, preference)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) ListActivePreferences(_ context.Context, projectID string, limit int) ([]Preference, error) {
	out := make([]Preference, 0, limit)
	for _, preference := range f.preferences {
		if preference.ProjectID == projectID && preference.Active() {
			out = append(out, preference)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) UpsertContext(_ context.Context, snapshot StoredContext) error {
	f.contexts[snapshot.Hash] = snapshot
	return nil
}

func (f *fakeRepo) GetContext(_ context.Context, hash string) (StoredContext, error) {
	if value, ok := f.contexts[hash]; ok {
		return value, nil
	}
	return StoredContext{}, store.ErrNotFound
}

func (f *fakeRepo) ListResolutions(_ context.Context, projectID string, limit int) ([]ProjectResolution, error) {
	matched := make([]ProjectResolution, 0, len(f.resolutions))
	for index, resolution := range f.resolutions {
		if resolution.ProjectID == projectID {
			matched = append(matched, ProjectResolution{ID: int64(index + 1), Resolution: resolution})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if !matched[i].Resolution.ResolvedAt.Equal(matched[j].Resolution.ResolvedAt) {
			return matched[i].Resolution.ResolvedAt.After(matched[j].Resolution.ResolvedAt)
		}
		return matched[i].Resolution.PrimitiveID > matched[j].Resolution.PrimitiveID
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// ---------------------------------------------------------------- fixtures

func serviceRepo() *fakeRepo {
	repo := newFakeRepo()
	repo.primitives["process/bounded-subprocess"] = model.Primitive{
		ID: "process/bounded-subprocess", Name: "Bounded subprocess",
		ContractID: "process/bounded-subprocess/v1",
	}
	repo.contracts["process/bounded-subprocess/v1"] = model.Contract{
		ID: "process/bounded-subprocess/v1", PrimitiveID: "process/bounded-subprocess",
		Version: "v1", Requirements: []model.Requirement{
			{ID: "supports-timeout", Kind: "lifecycle", Required: true},
		},
	}
	repo.specimens["example.com/dep@v1.0.0"] = model.Specimen{
		ID: "example.com/dep@v1.0.0", PrimitiveID: "process/bounded-subprocess",
		Name: "dep", ReuseMode: []model.ReuseMode{model.ReuseDependency},
		Source: model.SourceRef{URL: "https://example.invalid/dep", Path: "github.com/example/dep", Revision: "v1.0.0", License: "MIT"},
	}
	repo.evidence["example.com/dep@v1.0.0"] = append(
		[]model.Evidence{
			{
				ID: "ev/relevance", SubjectID: "example.com/dep@v1.0.0",
				Kind: "discovery_match", Result: model.EvidenceInfo, ObservedAt: time.Now(),
			},
			{
				ID: "ev/behaviour", SubjectID: "example.com/dep@v1.0.0",
				Kind: "test", Result: model.EvidencePass, AppliesTo: "supports-timeout",
				Claim: "behavioural test satisfied supports-timeout", ObservedAt: time.Now(),
			},
		},
		factEvidence("example.com/dep@v1.0.0", "source_license", "MIT"),
		factEvidence("example.com/dep@v1.0.0", "known_advisory_count", 0),
		factEvidence("example.com/dep@v1.0.0", "repository_archived", false),
		factEvidence("example.com/dep@v1.0.0", "package_deprecated", false),
		factEvidence("example.com/dep@v1.0.0", "source_revision", "v1.0.0"),
	)
	return repo
}

func serviceFixture(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	repo := serviceRepo()
	clock := func() time.Time { return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC) }
	return NewService(repo, Options{Clock: clock}), repo
}

func decideRequest(projectID string, candidates ...resolver.CandidateRef) DecideRequest {
	return DecideRequest{
		ProjectID:   projectID,
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Candidates:  candidates,
	}
}

var _ = policy.FeedbackNotQuite

// seedStoredProject installs a project and its first fingerprint so a
// project-aware decision has something to reference.
func seedStoredProject(t *testing.T, repo *fakeRepo) Project {
	t.Helper()
	fingerprint := Fingerprint{
		SchemaVersion: SchemaVersion,
		Language:      LanguageGo,
		Modules: []GoModule{{
			ModulePath: "example.com/widget",
			GoVersion:  "1.27.1",
			Requirements: []GoRequirement{
				{ModulePath: "github.com/example/dep", Version: "v1.0.0"},
			},
		}},
	}
	sha, err := FingerprintHash(fingerprint)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	project := Project{ID: ProjectID(fingerprint), Name: DisplayName(fingerprint), SourceKind: SourceLocal}
	repo.projects[project.ID] = project
	repo.fingerprints = append(repo.fingerprints, StoredFingerprint{
		ID: 1, ProjectID: project.ID, SchemaVersion: SchemaVersion,
		SHA256: sha, Fingerprint: fingerprint, ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	})
	return project
}

func TestRememberIsIdempotent(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	request := RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackAvoidDependency,
	}
	first, created, err := service.Remember(context.Background(), request)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !created || first.ID == 0 {
		t.Fatalf("first remember = %+v, created=%v", first, created)
	}
	second, created, err := service.Remember(context.Background(), request)
	if err != nil {
		t.Fatalf("remember twice: %v", err)
	}
	if created {
		t.Error("remembering the same thing twice created a duplicate")
	}
	if second.ID != first.ID {
		t.Errorf("ids = %d vs %d, want the existing memory", second.ID, first.ID)
	}
	if len(repo.preferences) != 1 {
		t.Errorf("stored %d preferences, want 1", len(repo.preferences))
	}
}

func TestForgetIsIdempotentAndPreservesHistory(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	stored, _, err := service.Remember(context.Background(), RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackAvoidReference,
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}

	forgotten, err := service.Forget(context.Background(), project.ID, stored.ID)
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if forgotten.Active() {
		t.Fatal("forget did not mark the memory revoked")
	}
	// The row survives: history is not rewritten.
	if len(repo.preferences) != 1 {
		t.Fatalf("stored %d preferences, want the row preserved", len(repo.preferences))
	}
	again, err := service.Forget(context.Background(), project.ID, stored.ID)
	if err != nil {
		t.Fatalf("forget twice: %v", err)
	}
	if again.Active() {
		t.Error("forgetting twice must be safe")
	}
	if len(repo.preferences) != 1 {
		t.Errorf("stored %d preferences after a second forget", len(repo.preferences))
	}
}

func TestReRememberAfterForgetCreatesActiveMemory(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)
	request := RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackAvoidDependency,
	}

	first, _, err := service.Remember(context.Background(), request)
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, err := service.Forget(context.Background(), project.ID, first.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}

	active, err := repo.ListActivePreferences(context.Background(), project.ID, 10)
	if err != nil || len(active) != 0 {
		t.Fatalf("active = %+v err=%v, want none", active, err)
	}

	second, created, err := service.Remember(context.Background(), request)
	if err != nil {
		t.Fatalf("re-remember: %v", err)
	}
	if !created {
		t.Error("re-remembering after a forget must create a new active memory")
	}
	if second.ID == first.ID {
		t.Error("the revoked row was reused instead of a new one being created")
	}
	active, err = repo.ListActivePreferences(context.Background(), project.ID, 10)
	if err != nil || len(active) != 1 {
		t.Fatalf("active = %+v err=%v, want 1", active, err)
	}
}

func TestRememberDerivesOnlyFromStoredFacts(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	// The stored licence is MIT, so a licence memory must be MIT. A caller
	// cannot supply its own value.
	stored, created, err := service.Remember(context.Background(), RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackLicenceNotAllowed,
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !created || stored.Kind != PrefDenyLicence || stored.TextValue != "MIT" {
		t.Fatalf("preference = %+v", stored)
	}

	// An unknown candidate cannot produce a memory at all.
	if _, _, err := service.Remember(context.Background(), RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "missing/candidate", Reason: policy.FeedbackNotQuite,
	}); err == nil {
		t.Error("an unknown candidate must not produce a memory")
	}

	// A candidate from a different primitive is refused rather than scoped
	// wrongly.
	if _, _, err := service.Remember(context.Background(), RememberRequest{
		ProjectID: project.ID, PrimitiveID: "other/primitive",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackNotQuite,
	}); err == nil {
		t.Error("a mismatched primitive scope must be refused")
	}
}

// TestDecideNeverPersistsAPreference proves calling refine-style feedback
// through the decision path does not create memory on its own.
func TestDecideNeverPersistsAPreference(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	request := decideRequest(project.ID,
		resolver.CandidateRef{SpecimenID: "example.com/dep@v1.0.0", ReuseMode: model.ReuseDependency})
	request.Feedback = []policy.Feedback{
		{CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackNotQuite},
	}
	if _, err := service.Decide(context.Background(), request); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(repo.preferences) != 0 {
		t.Errorf("a decision created %d preference(s); memory must be explicit", len(repo.preferences))
	}
}

// TestProjectAwareResolutionStoresProjectIdentity is the linkage rule: the
// persisted decision records both the project and the immutable context it
// saw.
func TestProjectAwareResolutionStoresProjectIdentity(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	decision, err := service.Decide(context.Background(), decideRequest(project.ID,
		resolver.CandidateRef{SpecimenID: "example.com/dep@v1.0.0", ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Decision.Outcome.Decision.Status != resolver.StatusResolved {
		t.Fatalf("status = %q, want resolved", decision.Decision.Outcome.Decision.Status)
	}
	resolution := decision.Decision.Outcome.Decision.Resolution
	if resolution.ProjectID != project.ID {
		t.Errorf("resolution project id = %q, want %q", resolution.ProjectID, project.ID)
	}
	if resolution.ProjectContextHash != decision.ContextHash {
		t.Errorf("resolution context hash = %q, want %q", resolution.ProjectContextHash, decision.ContextHash)
	}
	if decision.ContextHash == "" || decision.FingerprintSHA256 == "" {
		t.Errorf("decision context = %q fingerprint = %q", decision.ContextHash, decision.FingerprintSHA256)
	}
	if len(decision.Effects) != 1 || decision.Effects[0].DependencyFit != resolver.FitExistingExact {
		t.Errorf("effects = %+v, want the exact-present dependency fit", decision.Effects)
	}

	history, err := service.ListHistory(context.Background(), project.ID, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].Resolution.ProjectID != project.ID {
		t.Errorf("history = %+v", history)
	}

	// A decision made without project context is never part of that history.
	generic := repo.resolutions[len(repo.resolutions)-1]
	generic.ProjectID = ""
	generic.ProjectContextHash = ""
	repo.resolutions[len(repo.resolutions)-1] = generic
	history, err = service.ListHistory(context.Background(), project.ID, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("a generic resolution appeared in project history: %+v", history)
	}
}

func TestProjectHistoryIsBoundedAndNewestFirst(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	for index := 0; index < 7; index++ {
		repo.resolutions = append(repo.resolutions, model.Resolution{
			PrimitiveID: "process/bounded-subprocess",
			ContractID:  "process/bounded-subprocess/v1",
			Outcome:     model.OutcomeBuildLocally,
			ProjectID:   project.ID,
			ResolvedAt:  time.Date(2026, time.January, 1+index, 0, 0, 0, 0, time.UTC),
		})
	}

	history, err := service.ListHistory(context.Background(), project.ID, 3)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %d entries, want the bound of 3", len(history))
	}
	if !history[0].Resolution.ResolvedAt.After(history[1].Resolution.ResolvedAt) {
		t.Errorf("history is not newest-first: %v then %v",
			history[0].Resolution.ResolvedAt, history[1].Resolution.ResolvedAt)
	}
	all, err := service.ListHistory(context.Background(), project.ID, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(all) != 7 {
		t.Errorf("history = %d, want 7", len(all))
	}
}

func TestDecisionBoundsAreEnforced(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	tooMany := make([]resolver.CandidateRef, 0, MaxCandidatesPerDecision+1)
	for index := 0; index <= MaxCandidatesPerDecision; index++ {
		tooMany = append(tooMany, resolver.CandidateRef{
			SpecimenID: "missing/" + string(rune('a'+index)),
			ReuseMode:  model.ReuseDependency,
		})
	}
	request := decideRequest(project.ID, tooMany...)
	if _, err := service.Decide(context.Background(), request); !errors.Is(err, ErrBoundsExceeded) {
		t.Errorf("err = %v, want ErrBoundsExceeded for %d candidates", err, len(tooMany))
	}

	// Exceeding the active-preference bound fails loudly rather than
	// silently dropping memories.
	for index := 0; index <= MaxActivePreferences; index++ {
		repo.preferences = append(repo.preferences, Preference{
			ID: int64(index + 1), ProjectID: project.ID, Kind: PrefAvoidDependency,
		})
	}
	single := decideRequest(project.ID,
		resolver.CandidateRef{SpecimenID: "example.com/dep@v1.0.0", ReuseMode: model.ReuseDependency})
	if _, err := service.Decide(context.Background(), single); !errors.Is(err, ErrBoundsExceeded) {
		t.Errorf("err = %v, want ErrBoundsExceeded for %d active preferences",
			err, MaxActivePreferences+1)
	}
}

// TestHistoricalContextSurvivesLaterPreferenceChanges proves a stored
// Resolution keeps resolving to the snapshot it actually saw: historical
// decisions never reinterpret themselves under today's project state.
func TestHistoricalContextSurvivesLaterPreferenceChanges(t *testing.T) {
	service, repo := serviceFixture(t)
	project := seedStoredProject(t, repo)

	decision, err := service.Decide(context.Background(), decideRequest(project.ID,
		resolver.CandidateRef{SpecimenID: "example.com/dep@v1.0.0", ReuseMode: model.ReuseDependency}))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Decision.Outcome.Decision.Resolution == nil {
		t.Fatal("expected a persisted resolution")
	}
	recordedHash := decision.Decision.Outcome.Decision.Resolution.ProjectContextHash

	// Change the project: add a remembered preference and scan a new
	// fingerprint so the current context differs from the recorded one.
	if _, _, err := service.Remember(context.Background(), RememberRequest{
		ProjectID: project.ID, PrimitiveID: "process/bounded-subprocess",
		CandidateID: "example.com/dep@v1.0.0", Reason: policy.FeedbackAvoidReference,
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	changed := repo.fingerprints[0].Fingerprint
	changed.Modules = append(changed.Modules, GoModule{ModulePath: "example.com/extra"})
	changedSHA, err := FingerprintHash(changed)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	repo.fingerprints = append(repo.fingerprints, StoredFingerprint{
		ID: 2, ProjectID: project.ID, SchemaVersion: SchemaVersion,
		SHA256: changedSHA, Fingerprint: changed, ObservedAt: time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
	})

	// The old context still resolves, unchanged.
	snapshot, err := service.LoadContext(context.Background(), recordedHash)
	if err != nil {
		t.Fatalf("load historical context: %v", err)
	}
	if snapshot.Hash != recordedHash || snapshot.ProjectID != project.ID {
		t.Errorf("snapshot = %+v", snapshot)
	}
	if len(snapshot.Context.Preferences) != 0 {
		t.Errorf("the historical snapshot now shows preferences: %+v", snapshot.Context.Preferences)
	}

	// The stored Resolution still names the old snapshot.
	reloaded, err := repo.GetResolution(context.Background(), decision.Decision.ResolutionID)
	if err != nil {
		t.Fatalf("reload resolution: %v", err)
	}
	if reloaded.ProjectContextHash != recordedHash {
		t.Errorf("resolution context hash = %q, want %q", reloaded.ProjectContextHash, recordedHash)
	}
}

// TestOutcomePackageNeverReferencesProjectPreferences guards the boundary
// between factual outcome history and preference memory.
func TestOutcomePackageNeverReferencesProjectPreferences(t *testing.T) {
	entries, err := os.ReadDir("../outcome")
	if err != nil {
		t.Fatalf("read outcome package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../outcome", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		source := string(data)
		if strings.Contains(source, "internal/project") || strings.Contains(source, "Preference") {
			t.Errorf("%s references project preference memory; outcome history must stay separate", entry.Name())
		}
	}
}
