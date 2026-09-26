//go:build integration

package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

// exactModule is the module the fixture project requires, so one candidate is
// an exact existing dependency and another is a version change.
const exactModule = "github.com/example/dep"

// writeLocalProject creates the smallest real Go project the local scanner
// can read.
func writeLocalProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := "module example.com/widget\n\ngo 1.27.1\n\nrequire " + exactModule + " v1.4.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return root
}

// seedProjectCandidates installs three behaviourally complete dependency
// candidates that differ only in how they relate to the project manifest.
//
// fixture/aaa requires a different version, fixture/zzz is the exact version
// and fixture/mmm is new to the project. Without project context the stable
// specimen id decides (aaa); with project context aaa needs review and zzz
// wins the exact-present tie-break.
func seedProjectCandidates(t *testing.T, store *postgres.Store) {
	t.Helper()
	ctx := context.Background()
	contract, err := store.GetContract(ctx, fixtureContract)
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}

	candidates := []struct {
		id       string
		revision string
		module   string
	}{
		{id: "fixture/aaa", revision: "v1.6.0", module: exactModule},
		{id: "fixture/zzz", revision: "v1.4.0", module: exactModule},
		{id: "fixture/mmm", revision: "v2.0.0", module: "github.com/example/other"},
	}
	for _, entry := range candidates {
		specimen := model.Specimen{
			ID:          entry.id,
			PrimitiveID: fixturePrimitive,
			Name:        entry.id,
			ReuseMode:   []model.ReuseMode{model.ReuseDependency},
			Source: model.SourceRef{
				URL: "https://example.invalid/" + entry.id, Path: entry.module,
				Revision: entry.revision, License: "MIT",
			},
		}
		if err := store.UpsertSpecimen(ctx, specimen); err != nil {
			t.Fatalf("upsert %s: %v", entry.id, err)
		}
		evidence := []model.Evidence{{
			ID: "ev/" + entry.id + "/relevance", SubjectID: entry.id,
			Kind: "discovery_match", Result: model.EvidenceInfo, ObservedAt: fixtureClock(),
		}}
		for kind, value := range map[string]any{
			"source_license": "MIT", "known_advisory_count": 0,
			"repository_archived": false, "package_deprecated": false,
			"source_revision": entry.revision,
		} {
			evidence = append(evidence, fact(entry.id, kind, value))
		}
		for _, requirement := range contract.Requirements {
			evidence = append(evidence, model.Evidence{
				ID: "ev/" + entry.id + "/pass/" + requirement.ID, SubjectID: entry.id,
				Kind: "test", Claim: "behavioural test satisfied " + requirement.ID,
				Result: model.EvidencePass, AppliesTo: requirement.ID, ObservedAt: fixtureClock(),
			})
		}
		for _, observation := range evidence {
			if err := store.InsertEvidence(ctx, observation); err != nil {
				t.Fatalf("insert evidence for %s: %v", entry.id, err)
			}
		}
	}
}

// projectCandidates is the bounded candidate set every project test resolves.
func projectCandidates() []CandidateRef {
	return []CandidateRef{
		{SpecimenID: "fixture/aaa", ReuseMode: model.ReuseDependency},
		{SpecimenID: "fixture/zzz", ReuseMode: model.ReuseDependency},
		{SpecimenID: "fixture/mmm", ReuseMode: model.ReuseDependency},
	}
}

// projectFixtureWithStore is the MCP harness for project integration tests.
type projectFixtureWithStore struct {
	deps    Dependencies
	cleanup func()
}

func newProjectFixtureWithStore(t *testing.T, store *postgres.Store, projectRoot string) *projectFixtureWithStore {
	t.Helper()
	clock := func() time.Time { return fixtureClock() }
	return &projectFixtureWithStore{
		deps: Dependencies{
			Catalog:     store,
			Inspector:   store,
			Projects:    project.NewService(store, project.Options{Clock: clock}),
			Resolver:    resolver.NewQualityService(store, resolver.Clock(clock)),
			Outcomes:    app.NewOutcomeRecorder(store, clock),
			ProjectRoot: projectRoot,
		},
		cleanup: func() {},
	}
}

// TestMCPProjectContextChangesTheDecisionForFactualReasons is Packet 10's
// critical proof: same primitive, same contract, same candidate evidence, same
// base policy — only the project context differs.
func TestMCPProjectContextChangesTheDecisionForFactualReasons(t *testing.T) {
	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)
	seedProjectCandidates(t, store)

	root := writeLocalProject(t)
	fixture := newProjectFixtureWithStore(t, store, root)
	defer fixture.cleanup()

	// 1. scan the local project
	_, scanned, err := fixture.deps.handleProjectScan(context.Background(), nil,
		ProjectScanInput{Source: "local"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if scanned.SourceKind != project.SourceLocal || scanned.SourceLocator != "" {
		t.Fatalf("scan result = %+v", scanned)
	}
	if scanned.Counts.DirectDependencies != 1 {
		t.Errorf("direct dependencies = %d, want 1", scanned.Counts.DirectDependencies)
	}
	projectID := scanned.ProjectID

	// 2. inspect the context
	_, view, err := fixture.deps.handleProjectContext(context.Background(), nil,
		ProjectContextInput{ProjectID: projectID})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(view.ActivePreferences) != 0 {
		t.Errorf("preferences = %v, want none yet", view.ActivePreferences)
	}

	// 3. without project context the stable specimen id decides
	_, plain, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		Candidates: projectCandidates(),
	})
	if err != nil {
		t.Fatalf("resolve without project: %v", err)
	}
	if plain.Selected == nil || plain.Selected.SpecimenID != "fixture/aaa" {
		t.Fatalf("plain selection = %+v, want fixture/aaa", plain.Selected)
	}
	if plain.ProjectID != "" {
		t.Errorf("plain decision carries project context: %q", plain.ProjectID)
	}

	// 4. with project context the version-change candidate needs review and
	// the exact-present candidate wins the tie-break.
	_, contextual, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		ProjectID: projectID, Candidates: projectCandidates(),
	})
	if err != nil {
		t.Fatalf("resolve with project: %v", err)
	}
	if contextual.Selected == nil || contextual.Selected.SpecimenID != "fixture/zzz" {
		t.Fatalf("project-aware selection = %+v, want fixture/zzz", contextual.Selected)
	}
	if contextual.Selected.SpecimenID == plain.Selected.SpecimenID {
		t.Fatal("project context did not change the decision")
	}
	if contextual.ProjectContextHash == "" {
		t.Fatal("the decision carries no project context hash")
	}
	fits := map[string]string{}
	for _, effect := range contextual.ProjectEffects {
		fits[effect.SpecimenID] = string(effect.DependencyFit)
	}
	if fits["fixture/aaa"] != string(resolver.FitExistingVersionChange) {
		t.Errorf("aaa fit = %q, want existing_version_change", fits["fixture/aaa"])
	}
	if fits["fixture/zzz"] != string(resolver.FitExistingExact) {
		t.Errorf("zzz fit = %q, want existing_exact", fits["fixture/zzz"])
	}
	if !strings.Contains(strings.Join(contextual.Reasons, "\n"), "already present in project "+projectID) {
		t.Errorf("the decision does not say which project context mattered: %v", contextual.Reasons)
	}
	if !strings.Contains(mustJSON(t, contextual), "version change requires review") {
		t.Error("no version-change review trade-off in the decision")
	}

	// 5. remember an explicit preference about the current answer
	_, remembered, err := fixture.deps.handleProjectRemember(context.Background(), nil, ProjectRememberInput{
		ProjectID: projectID, PrimitiveID: fixturePrimitive,
		CandidateID: "fixture/zzz", Reason: "not_quite",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !remembered.Created {
		t.Error("the first remember must create")
	}

	// 6. resolve again: the excluded candidate is gone, a different route wins
	_, afterRemember, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		ProjectID: projectID, Candidates: projectCandidates(),
	})
	if err != nil {
		t.Fatalf("resolve after remember: %v", err)
	}
	if afterRemember.Selected == nil || afterRemember.Selected.SpecimenID == contextual.Selected.SpecimenID {
		t.Fatalf("selection did not change after remembering: %+v", afterRemember.Selected)
	}
	if afterRemember.Selected.SpecimenID != "fixture/mmm" {
		t.Errorf("selection = %v, want fixture/mmm", afterRemember.Selected.SpecimenID)
	}

	// 7. forget the preference: the original eligibility returns
	_, _, err = fixture.deps.handleProjectForget(context.Background(), nil,
		ProjectForgetInput{ProjectID: projectID, PreferenceID: remembered.ID})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	_, afterForget, err := fixture.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		ProjectID: projectID, Candidates: projectCandidates(),
	})
	if err != nil {
		t.Fatalf("resolve after forget: %v", err)
	}
	if afterForget.Selected == nil || afterForget.Selected.SpecimenID != "fixture/zzz" {
		t.Fatalf("selection after forget = %+v, want fixture/zzz restored", afterForget.Selected)
	}

	// The historical decision still names the context it actually saw.
	if contextual.ResolutionID == nil {
		t.Fatal("the contextual decision was not persisted")
	}
	stored, err := fixture.deps.Resolver.Resolution(context.Background(), *contextual.ResolutionID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.ProjectContextHash != contextual.ProjectContextHash {
		t.Errorf("historical context hash = %q, want %q", stored.ProjectContextHash, contextual.ProjectContextHash)
	}

	// Outcome history stays separate from preference memory.
	if _, _, err := fixture.deps.handleReportOutcome(context.Background(), nil, OutcomeInput{
		ResolutionID: 1, Kind: outcome.KindAdopted, Note: "wired into the service",
	}); err != nil {
		t.Fatalf("report outcome: %v", err)
	}
	preferences, err := fixture.deps.Projects.ListPreferences(context.Background(), projectID, project.MaxHistoryLimit)
	if err != nil {
		t.Fatalf("list preferences: %v", err)
	}
	revoked := 0
	for _, preference := range preferences {
		if !preference.Active() {
			revoked++
		}
	}
	if revoked != 1 || len(preferences) != 1 {
		t.Errorf("preferences = %d with %d revoked; the revoked row must survive", len(preferences), revoked)
	}
}

// publicGitHubFixture serves the endpoints a public project scan uses and
// records every request so a test can prove nothing else was touched.
type publicGitHubFixture struct {
	server   *httptest.Server
	manifest string
	commit   string
	paths    []string
	lastAuth atomic.Value
}

func newPublicGitHubFixture(t *testing.T, manifest string) *publicGitHubFixture {
	t.Helper()
	fixture := &publicGitHubFixture{
		manifest: manifest,
		commit:   "0123456789abcdef0123456789abcdef01234567",
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.paths = append(fixture.paths, r.URL.Path)
		fixture.lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/contents/go.mod"):
			payload, _ := json.Marshal(map[string]any{
				"type": "file", "encoding": "base64", "size": len(fixture.manifest),
				"content": base64.StdEncoding.EncodeToString([]byte(fixture.manifest)),
			})
			_, _ = w.Write(payload)
		case strings.HasSuffix(r.URL.Path, "/contents/go.work"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + fixture.commit + `"}`))
		case strings.HasSuffix(r.URL.Path, "/acme/widget"):
			_, _ = w.Write([]byte(`{"default_branch":"main","private":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

// TestMCPPublicGitHubProjectScanNeedsNoCredential proves a public repository
// can be fingerprinted without a token, and that identical manifest facts
// identify the same project as a local scan.
func TestMCPPublicGitHubProjectScanNeedsNoCredential(t *testing.T) {
	manifest := "module example.com/widget\n\ngo 1.27.1\n\nrequire " + exactModule + " v1.4.0\n"
	remote := newPublicGitHubFixture(t, manifest)

	root := writeLocalProject(t)
	localResult, err := project.ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("local scan: %v", err)
	}

	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)
	seedProjectCandidates(t, store)
	mcp := newProjectFixtureWithStore(t, store, "")
	defer mcp.cleanup()
	mcp.deps.Projects = project.NewService(store, project.Options{
		Clock:           func() time.Time { return fixtureClock() },
		NewGitHubClient: func() project.GitHubClient { return github.NewClientWithBaseURL(remote.server.URL, "") },
	})
	mcp.deps.ExternalOperationsEnabled = true

	_, scanned, err := mcp.deps.handleProjectScan(context.Background(), nil,
		ProjectScanInput{Source: "github_public", Repository: "acme/widget"})
	if err != nil {
		t.Fatalf("github scan: %v", err)
	}
	if scanned.SourceKind != project.SourceGitHubPublic {
		t.Errorf("source kind = %q", scanned.SourceKind)
	}
	if scanned.SourceRevision != remote.commit {
		t.Errorf("source revision = %q, want the resolved commit SHA", scanned.SourceRevision)
	}
	if scanned.FingerprintSHA256 != localResult.FingerprintSHA256 {
		t.Errorf("identical manifests hashed differently: %s vs %s",
			scanned.FingerprintSHA256, localResult.FingerprintSHA256)
	}
	if scanned.ProjectID != localResult.Project.ID {
		t.Errorf("project ids differ: %q vs %q", scanned.ProjectID, localResult.Project.ID)
	}
	if auth, _ := remote.lastAuth.Load().(string); auth != "" {
		t.Errorf("public scan attached a credential: %q", auth)
	}
	for _, path := range remote.paths {
		if strings.HasSuffix(path, "/contents/go.mod") || strings.HasSuffix(path, "/contents/go.work") ||
			strings.Contains(path, "/commits/") || strings.HasSuffix(path, "/acme/widget") {
			continue
		}
		t.Errorf("unexpected endpoint touched: %s", path)
	}

	// The public project drives the same context-aware decision as a local one.
	_, decision, err := mcp.deps.handleResolve(context.Background(), nil, ResolveInput{
		PrimitiveID: fixturePrimitive, ContractID: fixtureContract,
		ProjectID: scanned.ProjectID, Candidates: projectCandidates(),
	})
	if err != nil {
		t.Fatalf("resolve with public project: %v", err)
	}
	if decision.Selected == nil || decision.Selected.SpecimenID != "fixture/zzz" {
		t.Errorf("selection = %+v, want fixture/zzz", decision.Selected)
	}
}
