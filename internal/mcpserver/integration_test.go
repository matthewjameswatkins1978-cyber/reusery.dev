//go:build integration

package mcpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/pkggodev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/depsdev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/githubmeta"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

const (
	mcpImage         = "postgres:18.6-alpine"
	mcpRepoRoot      = "../.."
	mcpManifest      = "catalogue/dev/bounded-subprocess/manifest.yaml"
	completeID       = "fixture/process/bounded-subprocess/complete-dependency"
	secondCompleteID = "fixture/process/bounded-subprocess/complete-second"
	partialID        = "fixture/process/bounded-subprocess/partial-adapt"
)

// startMCPDatabase brings up a real PostgreSQL, migrates through 00003 and
// returns the pool.
func startMCPDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := startMCPDatabaseWithURL(t)
	return pool
}

// startMCPDatabaseWithURL is startMCPDatabase plus the connection string a
// child process needs in its environment.
func startMCPDatabaseWithURL(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, mcpImage,
		tcpostgres.WithDatabase("reusery"),
		tcpostgres.WithUsername("reusery"),
		tcpostgres.WithPassword("reusery"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, databaseURL
}

// seedMCPFixture loads the canonical bundle and pins the reference candidate's
// revision so provenance is inspectable, exactly as the CLI fixtures do.
func seedMCPFixture(t *testing.T, store *postgres.Store) {
	t.Helper()
	ctx := context.Background()
	bundle, err := catalog.Load(mcpRepoRoot, filepath.FromSlash(mcpManifest))
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if err := catalog.Seed(ctx, bundle, store); err != nil {
		t.Fatalf("seed: %v", err)
	}
	specimen, err := store.GetSpecimen(ctx, completeID)
	if err != nil {
		t.Fatalf("load specimen: %v", err)
	}
	specimen.Source.Revision = "v1.0.0"
	if err := store.UpsertSpecimen(ctx, specimen); err != nil {
		t.Fatalf("pin revision: %v", err)
	}

	// A second behaviourally complete candidate gives re-resolution something
	// to fall back to when the first is rejected.
	duplicateCompleteSpecimen(t, store, secondCompleteID)
}

// duplicateCompleteSpecimen clones the complete fixture under a new identity
// so a feedback loop has two equally eligible candidates to choose between.
func duplicateCompleteSpecimen(t *testing.T, store *postgres.Store, id string) {
	t.Helper()
	ctx := context.Background()
	source, err := store.GetSpecimen(ctx, completeID)
	if err != nil {
		t.Fatalf("load source specimen: %v", err)
	}
	source.ID = id
	source.Name = id
	if err := store.UpsertSpecimen(ctx, source); err != nil {
		t.Fatalf("insert duplicate specimen: %v", err)
	}
	evidence, err := store.ListEvidenceBySubject(ctx, completeID)
	if err != nil {
		t.Fatalf("load source evidence: %v", err)
	}
	for _, item := range evidence {
		item.SubjectID = id
		item.ID = "dup/" + id + "/" + item.ID
		if err := store.InsertEvidence(ctx, item); err != nil {
			t.Fatalf("insert duplicate evidence: %v", err)
		}
	}
}

// fakeDepsDev serves a stable deps.dev v3 payload for the fixture package.
func fakeDepsDev(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":requirements") {
			_, _ = io.WriteString(w, `{"go":{"directDependencies":[{"name":"golang.org/x/sys","requirement":"v0.1.0"}],"indirectDependencies":[]}}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"versionKey": {"system":"GO","name":"github.com/example/complete","version":"v1.0.0"},
			"publishedAt": "2026-01-02T03:04:05Z",
			"isDeprecated": false,
			"licenses": ["MIT"],
			"advisoryKeys": [],
			"links": [{"label":"SOURCE_REPO","url":"https://github.com/example/complete"}]
		}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// fakeGitHubMetadata serves repository metadata for the fixture repositories.
func fakeGitHubMetadata(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"full_name": "example/complete",
			"html_url": "https://github.com/example/complete",
			"archived": false,
			"pushed_at": "2026-08-01T00:00:00Z",
			"default_branch": "main",
			"license": {"spdx_id": "MIT"},
			"stargazers_count": 1234,
			"forks_count": 56
		}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// mcpHarness is the real store behind an MCP server with httptest upstreams.
type mcpHarness struct {
	pool    *pgxpool.Pool
	store   *postgres.Store
	deps    Dependencies
	session *mcp.ClientSession
	cleanup func()
}

func newMCPHarness(t *testing.T, pool *pgxpool.Pool, external bool) *mcpHarness {
	t.Helper()
	store := postgres.NewStore(pool)

	deps := Dependencies{
		Catalog:                   store,
		Resolver:                  app.NewQualityResolver(store, fixtureClock),
		Inspector:                 store,
		Outcomes:                  app.NewOutcomeRecorder(store, fixtureClock),
		ExternalOperationsEnabled: external,
	}
	if external {
		deps.Enricher = enrichment.NewService(store, fixtureClock, []enrichment.Provider{
			depsdev.NewWithBaseURL(fakeDepsDev(t).URL),
			githubmeta.NewWithBaseURL(fakeGitHubMetadata(t).URL, ""),
		})
		deps.Discoverer = discovery.NewService(store, fixtureClock, []discovery.Provider{
			pkggodev.NewWithBaseURL(pkggodevFixture(t).URL),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := NewServer(deps)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect client: %v", err)
	}
	return &mcpHarness{
		pool: pool, store: store, deps: deps, session: clientSession,
		cleanup: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
			cancel()
		},
	}
}

// TestPostgreSQLMCPFlow runs the whole agent sequence against a real
// database with the real Packet 2 evaluator, Packet 3 store and Packet 7
// quality service. The MCP transport only adapts.
func TestPostgreSQLMCPFlow(t *testing.T) {
	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)

	ctx := context.Background()
	_ = ctx
	harness := newMCPHarness(t, pool, false)
	defer harness.cleanup()

	// 1. capability discovery
	_, catalogResult := callTool(t, harness.session, ToolCatalog, CatalogInput{})
	if catalogResult["primitives"] == nil {
		t.Fatalf("catalog returned nothing: %v", catalogResult)
	}
	_, detail := callTool(t, harness.session, ToolCatalog, CatalogInput{PrimitiveID: fixturePrimitive})
	if detail["contract"] == nil {
		t.Fatalf("catalog detail returned no contract: %v", detail)
	}

	// 2. resolve the complete dependency fixture. The seeded catalogue carries
	// behavioural evidence but no public metadata, so the test supplies an
	// explicit structured policy exactly as an agent would.
	pol := permissivePolicyForFixture()
	_, decision := callTool(t, harness.session, ToolResolve, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: completeID, ReuseMode: model.ReuseDependency},
		},
		Policy: &pol,
	})
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; decision=%v", decision["status"], decision)
	}
	if decision["outcome"] != "depend" {
		t.Fatalf("outcome = %v, want depend", decision)
	}
	if decision["policy_id"] != pol.ID {
		t.Errorf("policy_id = %v, want %s", decision["policy_id"], pol.ID)
	}
	resolutionID := int64(decision["resolution_id"].(float64))

	// 3. remember the decision
	_, stored := callTool(t, harness.session, ToolInspectResolution,
		InspectResolutionInput{ResolutionID: resolutionID})
	if stored["resolution"] == nil {
		t.Fatalf("resolution missing: %v", stored)
	}
	if stored["outcome_feedback"] == nil {
		t.Fatalf("outcome_feedback missing: %v", stored)
	}
	if feedback := stored["outcome_feedback"].([]any); len(feedback) != 0 {
		t.Errorf("expected no feedback yet, got %v", feedback)
	}

	// 4. record what actually happened
	for _, kind := range []string{"adopted", "integration_succeeded"} {
		_, recorded := callTool(t, harness.session, ToolReportOutcome, OutcomeInput{
			ResolutionID: resolutionID, Kind: outcome.Kind(kind), Note: "compiles and passes local tests",
		})
		if recorded["kind"] != kind {
			t.Errorf("recorded kind = %v, want %s", recorded["kind"], kind)
		}
	}

	// 5. inspect again: the history is chronological and append-only
	_, reloaded := callTool(t, harness.session, ToolInspectResolution,
		InspectResolutionInput{ResolutionID: resolutionID})
	events := reloaded["outcome_feedback"].([]any)
	if len(events) != 2 {
		t.Fatalf("outcome events = %d, want 2", len(events))
	}
	if events[0].(map[string]any)["kind"] != "adopted" ||
		events[1].(map[string]any)["kind"] != "integration_succeeded" {
		t.Errorf("events are out of order: %v", events)
	}
	resolution := reloaded["resolution"].(map[string]any)
	if resolution["outcome"] != "depend" {
		t.Errorf("outcome changed after reporting: %v", resolution["outcome"])
	}

	// 6. the decision really is in PostgreSQL, not in process memory
	var storedPolicy string
	var storedCount int
	if err := pool.QueryRow(ctx,
		"SELECT policy_id FROM resolutions WHERE id = $1", resolutionID).Scan(&storedPolicy); err != nil {
		t.Fatalf("query policy_id: %v", err)
	}
	if storedPolicy != permissivePolicyForFixture().ID {
		t.Errorf("stored policy_id = %q", storedPolicy)
	}
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM resolutions").Scan(&storedCount); err != nil {
		t.Fatalf("count resolutions: %v", err)
	}
	if storedCount != 1 {
		t.Errorf("resolutions = %d, want 1", storedCount)
	}
	var feedbackCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM resolution_feedback").Scan(&feedbackCount); err != nil {
		t.Fatalf("count feedback: %v", err)
	}
	if feedbackCount != 2 {
		t.Errorf("resolution_feedback rows = %d, want 2", feedbackCount)
	}
}

// TestMCPMetadataOnlyCandidateNeverBecomesDepend is the standing rule proven
// over MCP against real storage: public metadata without behavioural evidence
// stays needs_verification.
func TestMCPMetadataOnlyCandidateNeverBecomesDepend(t *testing.T) {
	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)
	harness := newMCPHarness(t, pool, false)
	defer harness.cleanup()

	// A stored specimen with only discovery evidence.
	if err := store.UpsertSpecimen(context.Background(), model.Specimen{
		ID:          "public/pkg.go.dev/example/metadata-only@v1.0.0",
		PrimitiveID: fixturePrimitive,
		Name:        "example/metadata-only",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source:      model.SourceRef{URL: "https://pkg.go.dev/example/metadata-only", Revision: "v1.0.0", License: "MIT"},
	}); err != nil {
		t.Fatalf("upsert specimen: %v", err)
	}

	_, decision := callTool(t, harness.session, ToolResolve, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: "public/pkg.go.dev/example/metadata-only@v1.0.0", ReuseMode: model.ReuseDependency},
		},
	})
	if decision["status"] != "needs_verification" {
		t.Fatalf("status = %v, want needs_verification", decision["status"])
	}
	if decision["resolution_id"] != nil || decision["outcome"] != nil {
		t.Errorf("needs_verification produced a decision: %v", decision)
	}
	unknowns := decision["unknowns"].([]any)
	if len(unknowns) != len(requirementIDs) {
		t.Errorf("unknowns = %d, want %d", len(unknowns), len(requirementIDs))
	}

	var count int
	if err := harness.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM resolutions").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("resolutions = %d, want 0", count)
	}
}

// TestMCPNextBestFitChangesTheDecision proves re-resolution rather than queue
// pagination: feedback changes the effective constraints and a different
// candidate becomes the answer.
func TestMCPNextBestFitChangesTheDecision(t *testing.T) {
	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)
	harness := newMCPHarness(t, pool, false)
	defer harness.cleanup()

	pol := permissivePolicyForFixture()
	candidates := []CandidateRef{
		{SpecimenID: completeID, ReuseMode: model.ReuseDependency},
		{SpecimenID: secondCompleteID, ReuseMode: model.ReuseDependency},
	}

	_, base := callTool(t, harness.session, ToolResolve, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  candidates,
		Policy:      &pol,
	})
	if base["selected"] == nil {
		t.Fatalf("base decision selected nothing: %v", base)
	}
	baseSelection := base["selected"].(map[string]any)["specimen_id"]
	if baseSelection != completeID {
		t.Fatalf("base selection = %v, want %s", baseSelection, completeID)
	}

	_, refined := callTool(t, harness.session, ToolRefine, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates:  candidates,
		Policy:      &pol,
		Feedback: []policyFeedback{
			{CandidateID: completeID, Reason: feedbackNotQuite},
		},
	})
	applied := refined["applied_feedback"].([]any)
	if len(applied) != 1 {
		t.Fatalf("applied_feedback = %v", applied)
	}
	if applied[0].(map[string]any)["reason"] != string(feedbackNotQuite) {
		t.Errorf("reason = %v", applied[0])
	}
	rejected := refined["rejected"].([]any)
	found := false
	for _, raw := range rejected {
		entry := raw.(map[string]any)
		if entry["specimen_id"] == completeID {
			found = true
			reasons := strings.Join(toStringSlice(entry["reasons"]), " ")
			if !strings.Contains(reasons, "user_feedback:not_quite") {
				t.Errorf("negative knowledge missing: %s", reasons)
			}
		}
	}
	if !found {
		t.Errorf("excluded candidate missing from rejected: %v", rejected)
	}
	selected := refined["selected"]
	if selected != nil {
		selectedID := selected.(map[string]any)["specimen_id"]
		if selectedID == baseSelection {
			t.Error("refinement returned the same candidate: this is queue pagination, not re-resolution")
		}
	}
	if refined["effective_policy"] == nil {
		t.Error("refine must report the effective policy")
	}
}

// permissivePolicyForFixture is a complete structured policy that allows the
// seeded fixture whose behavioural evidence exists but whose metadata facts
// are unknown. It exercises the structured-policy path rather than the
// built-in baseline.
func permissivePolicyForFixture() Policy {
	return Policy{
		SchemaVersion: 1,
		ID:            "test/integration/v1",
		Reuse: PolicyReuse{
			Allowed:   []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference},
			Preferred: []model.ReuseMode{},
		},
		Licence: PolicyLicence{
			Allow: []string{}, Deny: []string{},
			Unknown: policy.ActionAllow, Multiple: policy.ActionReview, Unlisted: policy.ActionAllow,
		},
		Security:     PolicySecurity{KnownAdvisory: policy.ActionReview, Unknown: policy.ActionAllow},
		Dependencies: PolicyDeps{Unknown: policy.ActionAllow},
		Maintenance: PolicyMaint{
			Archived: policy.ActionAllow, Deprecated: policy.ActionAllow,
			Stale: policy.ActionReview, Unknown: policy.ActionAllow,
		},
		Source: PolicySource{
			RequireRevisionFor: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt},
			MissingRevision:    policy.ActionReview,
		},
		Selection: PolicySelection{MaxOptions: 3},
	}
}

func toStringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, fmt.Sprintf("%v", item))
	}
	return out
}

// pkggodevFixture serves a stable pkg.go.dev search payload so discovery runs
// against httptest rather than the public internet.
func pkggodevFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/search"):
			_, _ = io.WriteString(w, `{"items":[
			  {"packagePath":"github.com/example/subproc","modulePath":"github.com/example/subproc","version":"v1.0.0","synopsis":"Package subproc runs subprocesses with bounded output."}
			],"total":1}`)
		case strings.HasPrefix(r.URL.Path, "/v1/package/"):
			_, _ = io.WriteString(w, `{"modulePath":"github.com/example/subproc","version":"v1.0.0","path":"github.com/example/subproc","name":"pkg","synopsis":"Fixture package metadata.","isStandardLibrary":false,"isRedistributable":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestMCPDiscoveryAndEnrichmentFlow runs the two gated tools through MCP
// against httptest upstreams, then resolves the discovered specimen: it stays
// needs_verification because discovery and metadata are never behavioural
// evidence.
func TestMCPDiscoveryAndEnrichmentFlow(t *testing.T) {
	pool := startMCPDatabase(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)
	harness := newMCPHarness(t, pool, true)
	defer harness.cleanup()

	_, discovered := callTool(t, harness.session, ToolDiscover, discovery.Profile{
		SchemaVersion: discovery.SchemaVersion,
		PrimitiveID:   fixturePrimitive,
		ContractID:    fixtureContract,
		Providers: []discovery.ProviderPlan{
			{ID: "pkg.go.dev", Queries: []discovery.Query{{Text: "subprocess runner cancellation", Limit: 3}}},
		},
	})
	candidates := discovered["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("discovery returned no candidates: %v", discovered)
	}
	if strings.Contains(mustJSON(t, discovered), `"result":"pass"`) ||
		strings.Contains(mustJSON(t, discovered), `"result":"fail"`) {
		t.Errorf("discovery produced behavioural evidence: %s", mustJSON(t, discovered))
	}
	discoveredID := candidates[0].(map[string]any)["specimen_id"].(string)

	_, enriched := callTool(t, harness.session, ToolEnrich, EnrichInput{
		SpecimenIDs: []string{discoveredID},
	})
	if enriched["evidence"].(float64) == 0 {
		t.Errorf("enrichment recorded nothing: %v", enriched)
	}
	if strings.Contains(mustJSON(t, enriched), `"result":"pass"`) {
		t.Errorf("enrichment produced behavioural evidence: %s", mustJSON(t, enriched))
	}

	_, decision := callTool(t, harness.session, ToolResolve, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: discoveredID, ReuseMode: model.ReuseDependency},
		},
	})
	if decision["status"] != "needs_verification" {
		t.Fatalf("discovered candidate status = %v, want needs_verification", decision["status"])
	}
	if decision["outcome"] != nil {
		t.Errorf("outcome = %v, want null", decision["outcome"])
	}
}
