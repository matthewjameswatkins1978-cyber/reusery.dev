package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// projectFixture builds a project service over the in-memory repository with
// two behaviourally eligible dependency candidates that differ only in how
// they relate to the project manifest.
type projectFixture struct {
	deps  Dependencies
	repo  *projectRepo
	alpha string
	zeta  string
}

func newProjectFixture(t *testing.T, projectRoot string) *projectFixture {
	t.Helper()
	repo := newProjectRepo()

	repo.primitives[fixturePrimitive] = model.Primitive{
		ID: fixturePrimitive, Name: "Bounded subprocess",
		Description: "Run child processes.", ContractID: fixtureContract,
	}
	repo.contracts[fixtureContract] = model.Contract{
		ID: fixtureContract, PrimitiveID: fixturePrimitive, Version: "v1",
		Requirements: []model.Requirement{
			{ID: requirementIDs[0], Description: requirementIDs[0], Kind: "behavior", Required: true},
		},
	}

	alpha := "fixture/alpha"
	zeta := "fixture/zeta"
	// alpha points at a module the project does not require; zeta points at
	// one it requires at exactly the candidate revision.
	addProjectSpecimen(repo, alpha, "github.com/example/other", "v1.0.0")
	addProjectSpecimen(repo, zeta, "github.com/example/dep", "v1.0.0")

	// The fixture project already exists, exactly as a stored project does
	// after a scan.
	fingerprint := widgetFingerprint()
	sha, err := project.FingerprintHash(fingerprint)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	projectID := project.ProjectID(fingerprint)
	repo.projects[projectID] = project.Project{
		ID: projectID, Name: "widget", SourceKind: project.SourceLocal,
	}
	repo.fingerprints = append(repo.fingerprints, project.StoredFingerprint{
		ID: 1, ProjectID: projectID, SchemaVersion: project.SchemaVersion,
		SHA256: sha, Fingerprint: fingerprint,
		ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	})

	clock := func() time.Time { return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC) }
	return &projectFixture{
		repo: repo,
		deps: Dependencies{
			Catalog:     repo,
			Inspector:   repo,
			Projects:    project.NewService(repo, project.Options{Clock: clock}),
			Resolver:    resolver.NewQualityService(repo, resolver.Clock(clock)),
			Outcomes:    app.NewOutcomeRecorder(repo, clock),
			ProjectRoot: projectRoot,
		},
		alpha: alpha,
		zeta:  zeta,
	}
}

// widgetFingerprint is the manifest fact set the fixture project derives from.
func widgetFingerprint() project.Fingerprint {
	return project.Fingerprint{
		SchemaVersion: project.SchemaVersion,
		Language:      project.LanguageGo,
		Modules: []project.GoModule{{
			ModulePath:   "example.com/widget",
			GoVersion:    "1.27.1",
			Requirements: []project.GoRequirement{{ModulePath: "github.com/example/dep", Version: "v1.0.0"}},
		}},
	}
}

// addProjectSpecimen installs a behaviourally complete dependency candidate.
func addProjectSpecimen(repo *projectRepo, id, modulePath, revision string) {
	repo.specimens[id] = model.Specimen{
		ID: id, PrimitiveID: fixturePrimitive, Name: id,
		ReuseMode: []model.ReuseMode{model.ReuseDependency},
		Source:    model.SourceRef{URL: "https://example.invalid/" + id, Path: modulePath, Revision: revision, License: "MIT"},
	}
	evidence := []model.Evidence{{
		ID: "ev/" + id + "/relevance", SubjectID: id, Kind: "discovery_match",
		Result: model.EvidenceInfo, ObservedAt: fixtureClock(),
	}}
	for kind, value := range map[string]any{
		"source_license": "MIT", "known_advisory_count": 0,
		"repository_archived": false, "package_deprecated": false,
		"source_revision": revision,
	} {
		evidence = append(evidence, fact(id, kind, value))
	}
	evidence = append(evidence, model.Evidence{
		ID: "ev/" + id + "/pass/" + requirementIDs[0], SubjectID: id, Kind: "test",
		Claim: "behavioural test satisfied " + requirementIDs[0], Result: model.EvidencePass,
		AppliesTo: requirementIDs[0], ObservedAt: fixtureClock(),
	})
	repo.evidence[id] = evidence
}

// ------------------------------------------------------------------ scanning

func TestProjectScanLocalUsesTheConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/widget\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	fixture := newProjectFixture(t, root)

	_, result, err := fixture.deps.handleProjectScan(context.Background(), nil, ProjectScanInput{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.SourceKind != project.SourceLocal {
		t.Errorf("source kind = %q", result.SourceKind)
	}
	if result.SourceLocator != "" {
		t.Errorf("a local scan must never record a path, got %q", result.SourceLocator)
	}
	if result.Counts.Modules != 1 {
		t.Errorf("modules = %d, want 1", result.Counts.Modules)
	}
	if !strings.HasPrefix(result.ProjectID, "project/go/") {
		t.Errorf("project id = %q", result.ProjectID)
	}
	if strings.Contains(mustJSON(t, result), root) {
		t.Error("the scan result contains the local root")
	}
}

func TestProjectScanWithoutRootIsUnconfigured(t *testing.T) {
	fixture := newProjectFixture(t, "")
	_, _, err := fixture.deps.handleProjectScan(context.Background(), nil, ProjectScanInput{})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeProjectRootUnconfigured {
		t.Fatalf("err = %v, want project_root_unconfigured", err)
	}
}

func TestProjectScanRejectsUnknownSource(t *testing.T) {
	fixture := newProjectFixture(t, t.TempDir())
	_, _, err := fixture.deps.handleProjectScan(context.Background(), nil, ProjectScanInput{Source: "svn"})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeInvalidRequest {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}

func TestProjectScanGitHubRequiresExternalOperations(t *testing.T) {
	fixture := newProjectFixture(t, "")
	fixture.deps.ExternalOperationsEnabled = false
	_, _, err := fixture.deps.handleProjectScan(context.Background(), nil,
		ProjectScanInput{Source: projectSourceGitHubPublic, Repository: "acme/widget"})
	toolErr, ok := AsToolError(err)
	if !ok || toolErr.Code != CodeExternalOperations {
		t.Fatalf("err = %v, want external_operations_disabled", err)
	}
}

// -------------------------------------------------------------------- context

func TestProjectContextReturnsABoundedView(t *testing.T) {
	fixture := newProjectFixture(t, "")

	projectID := project.ProjectID(widgetFingerprint())
	_, _, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		ProjectID:   projectID,
		Candidates: []CandidateRef{
			{SpecimenID: fixture.alpha, ReuseMode: model.ReuseDependency},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := fixture.repo.resolutions[0].ProjectID; got != projectID {
		t.Fatalf("resolution project id = %q, want %q", got, projectID)
	}

	view, err := fixture.deps.Projects.Get(context.Background(), projectID, project.DefaultMCPHistory)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Project.ID != projectID {
		t.Errorf("view project = %q", view.Project.ID)
	}
	if len(view.Recent) != 1 {
		t.Errorf("recent decisions = %d, want 1", len(view.Recent))
	}
	if len(view.Preferences) != 0 {
		t.Errorf("preferences = %v, want none", view.Preferences)
	}
	if strings.Contains(mustJSON(t, view), os.TempDir()) {
		t.Error("the context view contains a local filesystem path")
	}

	if _, _, err := fixture.deps.Projects.Remember(context.Background(), project.RememberRequest{
		ProjectID: "project/go/missing", PrimitiveID: fixturePrimitive,
		CandidateID: fixture.alpha, Reason: "not_quite",
	}); err == nil {
		t.Fatal("expected a missing project to be rejected")
	}
}

// ------------------------------------------------------------------ decisions

// TestResolveWithoutProjectIDIsPacket9Exactly proves the additive rule: the
// project fields are absent and the stable-id tie-break decides.
func TestResolveWithoutProjectIDIsPacket9Exactly(t *testing.T) {
	fixture := newProjectFixture(t, "")
	_, result, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: fixture.alpha, ReuseMode: model.ReuseDependency},
			{SpecimenID: fixture.zeta, ReuseMode: model.ReuseDependency},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ProjectID != "" || result.ProjectContextHash != "" || result.ProjectEffects != nil {
		t.Errorf("project fields present without project_id: %+v", result)
	}
	if result.Selected == nil || result.Selected.SpecimenID != fixture.alpha {
		t.Fatalf("selection = %+v, want the stable-id answer", result.Selected)
	}
	if !strings.Contains(strings.Join(result.Reasons, "\n"), "stable specimen ID") {
		t.Errorf("reasons = %v", result.Reasons)
	}
}

// TestResolveWithProjectIDUsesContextAndExplainsIt proves the same need and
// candidate set produce a different decision purely because of project
// context, and that the decision says why.
func TestResolveWithProjectIDUsesContextAndExplainsIt(t *testing.T) {
	fixture := newProjectFixture(t, "")
	_, without, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: fixture.alpha, ReuseMode: model.ReuseDependency},
			{SpecimenID: fixture.zeta, ReuseMode: model.ReuseDependency},
		},
	})
	if err != nil {
		t.Fatalf("resolve without project: %v", err)
	}

	projectID := project.ProjectID(widgetFingerprint())
	_, with, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		ProjectID:   projectID,
		Candidates: []CandidateRef{
			{SpecimenID: fixture.alpha, ReuseMode: model.ReuseDependency},
			{SpecimenID: fixture.zeta, ReuseMode: model.ReuseDependency},
		},
	})
	if err != nil {
		t.Fatalf("resolve with project: %v", err)
	}

	if with.Selected == nil || with.Selected.SpecimenID != fixture.zeta {
		t.Fatalf("selection with project context = %+v, want the exact-present candidate", with.Selected)
	}
	if with.Selected.SpecimenID == without.Selected.SpecimenID {
		t.Error("project context did not change the decision")
	}
	if with.ProjectID != projectID || with.ProjectContextHash == "" {
		t.Errorf("project linkage = %q / %q", with.ProjectID, with.ProjectContextHash)
	}
	if len(with.ProjectEffects) != 2 {
		t.Fatalf("project effects = %+v, want one per candidate", with.ProjectEffects)
	}
	fits := map[string]resolver.DependencyFit{}
	for _, effect := range with.ProjectEffects {
		fits[effect.SpecimenID] = effect.DependencyFit
	}
	if fits[fixture.zeta] != resolver.FitExistingExact {
		t.Errorf("zeta fit = %q, want existing_exact", fits[fixture.zeta])
	}
	if fits[fixture.alpha] != resolver.FitNewDependency {
		t.Errorf("alpha fit = %q, want new_dependency", fits[fixture.alpha])
	}
	reasons := strings.Join(with.Reasons, "\n")
	if !strings.Contains(reasons, "because its exact module version is already present in project "+projectID) {
		t.Errorf("the decision does not explain which project context mattered: %v", with.Reasons)
	}
}

// TestRefineDoesNotRememberAnything proves the standing rule: calling refine
// with structured feedback never creates project preference memory.
func TestRefineDoesNotRememberAnything(t *testing.T) {
	fixture := newProjectFixture(t, "")
	projectID := project.ProjectID(widgetFingerprint())
	_, _, err := fixture.deps.handleRefine(context.Background(), nil, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		ProjectID:   projectID,
		Candidates: []CandidateRef{
			{SpecimenID: fixture.alpha, ReuseMode: model.ReuseDependency},
		},
		Feedback: []policyFeedback{{CandidateID: fixture.alpha, Reason: feedbackNotQuite}},
	})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if len(fixture.repo.preferences) != 0 {
		t.Errorf("refine created %d preference(s); memory must be explicit", len(fixture.repo.preferences))
	}
}

// TestProjectRememberAndForgetRoundTrip covers the explicit memory lifecycle
// through the tools: derive, see it, revoke it, and see it leave the active
// set while the row survives.
func TestProjectRememberAndForgetRoundTrip(t *testing.T) {
	fixture := newProjectFixture(t, "")
	projectID := project.ProjectID(widgetFingerprint())

	_, remembered, err := fixture.deps.handleProjectRemember(context.Background(), nil, ProjectRememberInput{
		ProjectID: projectID, PrimitiveID: fixturePrimitive,
		CandidateID: fixture.alpha, Reason: "not_quite",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if remembered.ID == 0 || !strings.Contains(remembered.Effect, "excluded") {
		t.Fatalf("remembered = %+v", remembered)
	}
	if !remembered.Created {
		t.Error("the first remember must create")
	}

	_, again, err := fixture.deps.handleProjectRemember(context.Background(), nil, ProjectRememberInput{
		ProjectID: projectID, PrimitiveID: fixturePrimitive,
		CandidateID: fixture.alpha, Reason: "not_quite",
	})
	if err != nil {
		t.Fatalf("remember twice: %v", err)
	}
	if again.Created {
		t.Error("remembering the same thing twice must reuse the active memory")
	}
	if len(fixture.repo.preferences) != 1 {
		t.Errorf("stored %d preferences, want 1", len(fixture.repo.preferences))
	}

	_, forgotten, err := fixture.deps.handleProjectForget(context.Background(), nil,
		ProjectForgetInput{ProjectID: projectID, PreferenceID: remembered.ID})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if forgotten.Active {
		t.Error("a forgotten preference must not be active")
	}
	if len(fixture.repo.preferences) != 1 {
		t.Error("forgetting must not delete the historical row")
	}
	if _, _, err := fixture.deps.handleProjectForget(context.Background(), nil,
		ProjectForgetInput{ProjectID: projectID, PreferenceID: remembered.ID}); err != nil {
		t.Errorf("forget twice: %v", err)
	}
}

// TestOutcomeEventsNeverBecomePreferences covers the other half of the
// boundary: reporting an integration outcome must not create memory.
func TestOutcomeEventsNeverBecomePreferences(t *testing.T) {
	fixture := newProjectFixture(t, "")
	projectID := project.ProjectID(widgetFingerprint())
	repo := fixture.repo
	repo.projects[projectID] = project.Project{ID: projectID, Name: "widget", SourceKind: project.SourceLocal}
	repo.resolutions = append(repo.resolutions, model.Resolution{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		Outcome: model.OutcomeBuildLocally, ProjectID: projectID, ResolvedAt: time.Now(),
	})

	for _, kind := range []outcome.Kind{outcome.KindAdopted, outcome.KindRejected, outcome.KindIntegrationFailed, outcome.KindAbandoned} {
		if _, _, err := fixture.deps.handleReportOutcome(context.Background(), nil, OutcomeInput{
			ResolutionID: 1, Kind: kind, Note: "the agent reported a fact",
		}); err != nil {
			t.Fatalf("report %s: %v", kind, err)
		}
	}
	if len(repo.preferences) != 0 {
		t.Errorf("outcome reporting created %d preference(s); memory must stay explicit", len(repo.preferences))
	}
}
