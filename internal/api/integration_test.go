//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/pkggodev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/depsdev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/githubmeta"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

const (
	apiImage       = "postgres:18.6-alpine"
	apiRepoRoot    = "../.."
	apiManifestRel = "catalogue/dev/bounded-subprocess/manifest.yaml"

	apiCodeSpecimen = "public/github/code/example/tools@abc123def456:exec/exec.go"
)

func writeFixtureJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, body); err != nil {
		panic(err)
	}
}

// newPkgGoDevFixture serves a bounded pkg.go.dev search and package payload.
func newPkgGoDevFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/search"):
			writeFixtureJSON(w, `{"items":[
			  {"packagePath":"github.com/example/subproc","modulePath":"github.com/example/subproc","version":"v1.0.0","synopsis":"Package subproc runs subprocesses with bounded output."}
			],"total":1}`)
		case strings.HasPrefix(r.URL.Path, "/v1/package/"):
			writeFixtureJSON(w, `{"modulePath":"github.com/example/subproc","version":"v1.0.0","path":"github.com/example/subproc","name":"pkg","synopsis":"Fixture package metadata.","isStandardLibrary":false,"isRedistributable":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newGitHubRepositoryFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, `{"total_count":1,"incomplete_results":false,"items":[
		  {"full_name":"example/tools","html_url":"https://github.com/example/tools",
		   "description":"Bounded subprocess execution for Go","language":"Go","archived":false,
		   "pushed_at":"2026-08-14T09:30:00Z","default_branch":"main",
		   "license":{"key":"mit","name":"MIT License","spdx_id":"MIT"},"stargazers_count":9999}
		]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func newGitHubCodeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, `{"total_count":1,"incomplete_results":false,"items":[
		  {"name":"exec.go","path":"exec/exec.go","sha":"abc123def456",
		   "html_url":"https://github.com/example/tools/blob/abc123def456/exec/exec.go",
		   "repository":{"full_name":"example/tools","html_url":"https://github.com/example/tools"}}
		]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// newGenericDepsDevFixture answers any GO package so the discovered specimen
// can be enriched without hard-coding its identity.
func newGenericDepsDevFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/v3/systems/GO/packages/")
		module := trimmed
		version := "v0.0.0"
		if strings.Contains(trimmed, "/versions/") {
			parts := strings.SplitN(trimmed, "/versions/", 2)
			module = parts[0]
			version = strings.TrimSuffix(parts[1], ":requirements")
		}
		if strings.HasSuffix(r.URL.Path, ":requirements") {
			writeFixtureJSON(w, `{"go":{"directDependencies":[{"name":"golang.org/x/sys","requirement":"v0.1.0"}],"indirectDependencies":[]}}`)
			return
		}
		writeFixtureJSON(w, fmt.Sprintf(`{
			"versionKey": {"system":"GO","name":%q,"version":%q},
			"publishedAt": "2026-01-02T03:04:05Z",
			"isDeprecated": false,
			"licenses": ["MIT"],
			"advisoryKeys": [],
			"links": [{"label":"SOURCE_REPO","url":"https://github.com/example/subproc"}]
		}`, module, version))
	}))
	t.Cleanup(server.Close)
	return server
}

func newGenericGitHubMetaFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/") {
			http.NotFound(w, r)
			return
		}
		writeFixtureJSON(w, `{
			"full_name": "example/subproc",
			"html_url": "https://github.com/example/subproc",
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

// apiHarness is a live HTTP server in front of a real PostgreSQL store with
// fake model output and httptest providers: no external network anywhere.
type apiHarness struct {
	store      *postgres.Store
	server     *httptest.Server
	client     *http.Client
	normalizer *fakeNormalizer
	pool       *pgxpool.Pool
	externalOn bool
}

func startAPIPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, apiImage,
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
	return pool
}

// seedAPICatalogue seeds the canonical first primitive and adds the reference
// specimen the resolve stages use.
func seedAPICatalogue(t *testing.T, store *postgres.Store) {
	t.Helper()
	ctx := context.Background()

	bundle, err := catalog.Load(apiRepoRoot, filepath.FromSlash(apiManifestRel))
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if err := catalog.Seed(ctx, bundle, store); err != nil {
		t.Fatalf("seed: %v", err)
	}

	codeSpecimen := model.Specimen{
		ID:          apiCodeSpecimen,
		PrimitiveID: bundle.Primitive.ID,
		Name:        "example/tools:exec/exec.go",
		Source: model.SourceRef{
			URL:      "https://github.com/example/tools/blob/abc123def456/exec/exec.go",
			Revision: "abc123def456",
			Path:     "exec/exec.go",
		},
		ReuseMode: []model.ReuseMode{model.ReuseReference},
	}
	if err := store.UpsertSpecimen(ctx, codeSpecimen); err != nil {
		t.Fatalf("upsert code specimen: %v", err)
	}
	if err := store.InsertEvidence(ctx, discovery.NewObservation(discovery.ObservationSpec{
		ProviderID:  discovery.ProviderGitHubCode,
		SubjectID:   codeSpecimen.ID,
		Kind:        "discovery_match",
		Claim:       `GitHub code search matched file "exec/exec.go" in repository "example/tools"`,
		Result:      model.EvidenceInfo,
		Source:      codeSpecimen.Source,
		ObservedAt:  time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		Methodology: discovery.MethodologyGitHubCode,
		Artifact:    "query=exec.CommandContext",
	})); err != nil {
		t.Fatalf("insert discovery evidence: %v", err)
	}
}

// newAPIHarness wires the handler over real services with injected upstreams
// and serves it over HTTP.
func newAPIHarness(t *testing.T, pool *pgxpool.Pool, external bool) *apiHarness {
	t.Helper()
	store := postgres.NewStore(pool)

	pkggodevServer := newPkgGoDevFixture(t)
	repoServer := newGitHubRepositoryFixture(t)
	codeServer := newGitHubCodeFixture(t)
	discoverer := discovery.NewService(store, func() time.Time {
		return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	}, []discovery.Provider{
		pkggodev.NewWithBaseURL(pkggodevServer.URL),
		github.NewRepositoryProvider(github.NewClientWithBaseURL(repoServer.URL, "")),
		github.NewCodeProvider(github.NewClientWithBaseURL(codeServer.URL, "")),
	})

	enricher := enrichment.NewService(store, func() time.Time {
		return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	}, []enrichment.Provider{
		depsdev.NewWithBaseURL(newGenericDepsDevFixture(t).URL),
		githubmeta.NewWithBaseURL(newGenericGitHubMetaFixture(t).URL, ""),
	})

	normalizer := &fakeNormalizer{result: sampleIntentResult(intent.StatusReady)}

	deps := Dependencies{
		NormalizerFactory:         func() (app.Normalizer, error) { return normalizer, nil },
		Discoverer:                discoverer,
		Enricher:                  enricher,
		Resolver:                  resolver.NewQualityService(store, func() time.Time { return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC) }),
		Inspector:                 store,
		ReadyCheckers:             readyCheckers(postgres.ReadyChecker(pool)),
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ExternalOperationsEnabled: external,
	}

	server := httptest.NewServer(NewHandler(deps))
	t.Cleanup(server.Close)

	return &apiHarness{
		store:      store,
		server:     server,
		client:     server.Client(),
		normalizer: normalizer,
		pool:       pool,
		externalOn: external,
	}
}

// request performs one HTTP call and returns the status plus decoded body.
func (h *apiHarness) request(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if res.Header.Get("X-Request-ID") == "" {
		t.Errorf("%s %s response has no X-Request-ID", method, path)
	}
	if res.Header.Get("Reusery-API-Version") != APIVersion {
		t.Errorf("%s %s Reusery-API-Version = %q", method, path, res.Header.Get("Reusery-API-Version"))
	}

	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return res.StatusCode, decoded
}

func (h *apiHarness) mustStatus(t *testing.T, method, path, body string, want int) map[string]any {
	t.Helper()
	status, decoded := h.request(t, method, path, body)
	if status != want {
		t.Fatalf("%s %s: status = %d, want %d; body=%v", method, path, status, want, decoded)
	}
	return decoded
}

// TestAPIInspectionResolveRefineFlowWithRealPostgreSQL drives the stored
// inspection and Packet 7 decision routes over HTTP against a real database.
func TestAPIInspectionResolveRefineFlowWithRealPostgreSQL(t *testing.T) {
	pool := startAPIPostgres(t)
	store := postgres.NewStore(pool)
	seedAPICatalogue(t, store)
	harness := newAPIHarness(t, pool, false)

	// Health never depends on the database.
	health := harness.mustStatus(t, http.MethodGet, "/health", "", http.StatusOK)
	if health["status"] != "ok" {
		t.Errorf("health = %v", health["status"])
	}
	ready := harness.mustStatus(t, http.MethodGet, "/ready", "", http.StatusOK)
	if ready["status"] != "ok" {
		t.Errorf("ready = %v", ready["status"])
	}

	// Inspection of slash-containing opaque ids.
	primitiveID := "process/bounded-subprocess"
	contract := harness.mustStatus(t, http.MethodGet,
		"/v1/primitives?id="+url.QueryEscape(primitiveID), "", http.StatusOK)
	if contract["id"] != primitiveID {
		t.Errorf("primitive id = %v", contract["id"])
	}

	contractBody := harness.mustStatus(t, http.MethodGet,
		"/v1/contracts?id="+url.QueryEscape("process/bounded-subprocess/v1"), "", http.StatusOK)
	if len(contractBody["requirements"].([]any)) == 0 {
		t.Error("contract has no requirements")
	}

	specimen := harness.mustStatus(t, http.MethodGet,
		"/v1/specimens?id="+url.QueryEscape(apiCodeSpecimen), "", http.StatusOK)
	if specimen["id"] != apiCodeSpecimen {
		t.Errorf("specimen id = %v", specimen["id"])
	}

	// Paginated evidence: walk every page and require a stable ordering.
	collected := []string{}
	cursor := ""
	for page := 0; page < 50; page++ {
		path := "/v1/evidence?subject_id=" + url.QueryEscape(apiCodeSpecimen) + "&limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		body := harness.mustStatus(t, http.MethodGet, path, "", http.StatusOK)
		items := body["evidence"].([]any)
		for _, raw := range items {
			collected = append(collected, raw.(map[string]any)["id"].(string))
		}
		next, _ := body["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(collected) == 0 {
		t.Fatal("no evidence paged back")
	}
	seen := map[string]bool{}
	for _, id := range collected {
		if seen[id] {
			t.Fatalf("duplicate evidence %q across pages", id)
		}
		seen[id] = true
	}

	// Resolve the reference candidate through the real Packet 7 service.
	policyBody := marshalPolicy(t, apiPolicy())
	resolveBody := fmt.Sprintf(`{
		"primitive_id": %q,
		"contract_id": %q,
		"candidates": [{"specimen_id": %q, "reuse_mode": "reference"}],
		"policy": %s
	}`, primitiveID, "process/bounded-subprocess/v1", apiCodeSpecimen, policyBody)

	decision := harness.mustStatus(t, http.MethodPost, "/v1/resolve", resolveBody, http.StatusOK)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%v", decision["status"], decision)
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "reference" {
		t.Errorf("outcome = %v, want reference", resolution["outcome"])
	}
	if resolution["policy_id"] != "api/test-v1" {
		t.Errorf("policy_id = %v", resolution["policy_id"])
	}
	reasons := joinAny(resolution["reasons"].([]any))
	if !strings.Contains(reasons, "reference-only") ||
		!strings.Contains(reasons, "behavioural contract satisfaction is not established") {
		t.Errorf("reference reasons = %s", reasons)
	}
	if len(resolution["unknowns"].([]any)) == 0 {
		t.Error("reference resolution lost its unknowns")
	}

	// Persisted identity round-trips through the inspection route.
	firstID := int64(decision["resolution_id"].(float64))
	stored := harness.mustStatus(t, http.MethodGet,
		fmt.Sprintf("/v1/resolutions/%d", firstID), "", http.StatusOK)
	storedResolution := stored["resolution"].(map[string]any)
	if storedResolution["specimen_id"] != apiCodeSpecimen {
		t.Errorf("stored specimen = %v", storedResolution["specimen_id"])
	}
	if storedResolution["policy_id"] != "api/test-v1" {
		t.Errorf("stored policy_id = %v", storedResolution["policy_id"])
	}

	// Structured feedback produces a meaningfully different, persisted result.
	refineBody := fmt.Sprintf(`{
		"primitive_id": %q,
		"contract_id": %q,
		"candidates": [{"specimen_id": %q, "reuse_mode": "reference"}],
		"policy": %s,
		"feedback": [{"candidate_id": %q, "reason": "avoid_reference"}]
	}`, primitiveID, "process/bounded-subprocess/v1", apiCodeSpecimen, policyBody, apiCodeSpecimen)

	refined := harness.mustStatus(t, http.MethodPost, "/v1/refine", refineBody, http.StatusOK)
	if refined["status"] != "resolved" {
		t.Fatalf("refined status = %v, want resolved", refined["status"])
	}
	refinedResolution := refined["resolution"].(map[string]any)
	if refinedResolution["outcome"] != "build_locally" {
		t.Errorf("refined outcome = %v, want build_locally", refinedResolution["outcome"])
	}
	rejected := refinedResolution["rejected"].([]any)
	found := false
	for _, raw := range rejected {
		entry := raw.(map[string]any)
		if entry["specimen_id"] == apiCodeSpecimen {
			found = true
			if text := joinAny(entry["reasons"].([]any)); !strings.Contains(text, "user_feedback:avoid_reference") {
				t.Errorf("negative knowledge = %s", text)
			}
		}
	}
	if !found {
		t.Errorf("rejected = %v", rejected)
	}
	if secondID := int64(refined["resolution_id"].(float64)); secondID == firstID {
		t.Errorf("refinement reused resolution id %d", firstID)
	}

	// Two stored resolutions total: needs_verification never persisted.
	count, err := store.CountResolutions(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("resolutions = %d, want 2", count)
	}
}

// TestAPIFullTransportFlowWithRealPostgreSQL proves a non-Go client can run
// every Reusery stage over HTTP: normalize -> discover -> enrich -> inspect ->
// resolve -> refine -> inspect persisted history.
func TestAPIFullTransportFlowWithRealPostgreSQL(t *testing.T) {
	pool := startAPIPostgres(t)
	store := postgres.NewStore(pool)
	seedAPICatalogue(t, store)
	harness := newAPIHarness(t, pool, true)

	// 1. normalize
	normalized := harness.mustStatus(t, http.MethodPost, "/v1/normalize",
		`{"input":"I need a Go component that runs child processes with timeouts"}`, http.StatusOK)
	if normalized["status"] != "ready" {
		t.Fatalf("normalize status = %v", normalized["status"])
	}
	if harness.normalizer.calls != 1 {
		t.Errorf("normalizer calls = %d", harness.normalizer.calls)
	}

	// 2. discover
	discoverBody := `{
		"schema_version": 1,
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"providers": [
			{"id": "pkg.go.dev", "queries": [{"text": "subprocess runner cancellation", "limit": 5}]},
			{"id": "github-repositories", "queries": [{"text": "bounded subprocess go", "limit": 5}]}
		]
	}`
	discovered := harness.mustStatus(t, http.MethodPost, "/v1/discover", discoverBody, http.StatusOK)
	candidates := discovered["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatal("discovery returned no candidates")
	}
	if strings.Contains(mustRaw(t, discovered), `"result":"pass"`) ||
		strings.Contains(mustRaw(t, discovered), `"result":"fail"`) {
		t.Errorf("discovery produced behavioural evidence: %s", mustRaw(t, discovered))
	}

	discoveredID := candidates[0].(map[string]any)["specimen"].(map[string]any)["id"].(string)

	// 3. enrich
	enrichBody := fmt.Sprintf(`{"specimen_ids":[%q]}`, discoveredID)
	enriched := harness.mustStatus(t, http.MethodPost, "/v1/enrich", enrichBody, http.StatusOK)
	if strings.Contains(mustRaw(t, enriched), `"result":"pass"`) ||
		strings.Contains(mustRaw(t, enriched), `"result":"fail"`) {
		t.Errorf("enrichment produced behavioural evidence: %s", mustRaw(t, enriched))
	}

	// 4. inspect
	inspected := harness.mustStatus(t, http.MethodGet,
		"/v1/specimens?id="+url.QueryEscape(discoveredID), "", http.StatusOK)
	if inspected["id"] != discoveredID {
		t.Errorf("inspected id = %v", inspected["id"])
	}
	page := harness.mustStatus(t, http.MethodGet,
		"/v1/evidence?subject_id="+url.QueryEscape(discoveredID)+"&limit=2", "", http.StatusOK)
	if len(page["evidence"].([]any)) == 0 {
		t.Error("no evidence for the discovered specimen")
	}

	// 5. resolve the discovered candidate: plausible but unverified.
	policyBody := marshalPolicy(t, apiPolicy())
	resolveBody := fmt.Sprintf(`{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [{"specimen_id": %q, "reuse_mode": "dependency"}],
		"policy": %s
	}`, discoveredID, policyBody)

	decision := harness.mustStatus(t, http.MethodPost, "/v1/resolve", resolveBody, http.StatusOK)
	if decision["status"] != "needs_verification" {
		t.Fatalf("discovered candidate status = %v, want needs_verification", decision["status"])
	}
	if decision["resolution_id"] != nil || decision["resolution"] != nil {
		t.Errorf("needs_verification persisted something: %v", decision)
	}

	// 6. resolve the reference candidate, then refine it.
	referenceBody := fmt.Sprintf(`{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [{"specimen_id": %q, "reuse_mode": "reference"}],
		"policy": %s
	}`, apiCodeSpecimen, policyBody)
	reference := harness.mustStatus(t, http.MethodPost, "/v1/resolve", referenceBody, http.StatusOK)
	if reference["status"] != "resolved" {
		t.Fatalf("reference status = %v, want resolved; body=%v", reference["status"], reference)
	}
	resolutionID := int64(reference["resolution_id"].(float64))

	refineBody := fmt.Sprintf(`{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [{"specimen_id": %q, "reuse_mode": "reference"}],
		"policy": %s,
		"feedback": [{"candidate_id": %q, "reason": "not_quite"}]
	}`, apiCodeSpecimen, policyBody, apiCodeSpecimen)
	refined := harness.mustStatus(t, http.MethodPost, "/v1/refine", refineBody, http.StatusOK)
	if refined["status"] != "resolved" {
		t.Fatalf("refined status = %v, want resolved; body=%v", refined["status"], refined)
	}
	refinedResolution := refined["resolution"].(map[string]any)
	if refinedResolution["outcome"] != "build_locally" {
		t.Errorf("refined outcome = %v, want build_locally", refinedResolution["outcome"])
	}

	// 7. inspect both persisted resolutions
	first := harness.mustStatus(t, http.MethodGet,
		fmt.Sprintf("/v1/resolutions/%d", resolutionID), "", http.StatusOK)
	if first["resolution"].(map[string]any)["outcome"] != "reference" {
		t.Errorf("stored resolution 1 = %v", first["resolution"])
	}
	secondID := int64(refined["resolution_id"].(float64))
	second := harness.mustStatus(t, http.MethodGet,
		fmt.Sprintf("/v1/resolutions/%d", secondID), "", http.StatusOK)
	if second["resolution"].(map[string]any)["outcome"] != "build_locally" {
		t.Errorf("stored resolution 2 = %v", second["resolution"])
	}

	count, err := store.CountResolutions(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("resolutions = %d, want exactly 2", count)
	}
}

// TestAPIExternalOperationsSafeDefault starts a server with the switch off and
// proves no paid or provider-backed route is reachable.
func TestAPIExternalOperationsSafeDefault(t *testing.T) {
	pool := startAPIPostgres(t)
	store := postgres.NewStore(pool)
	seedAPICatalogue(t, store)
	harness := newAPIHarness(t, pool, false)

	if code, _ := harness.request(t, http.MethodGet, "/health", ""); code != http.StatusOK {
		t.Errorf("health = %d", code)
	}
	if code, _ := harness.request(t, http.MethodGet, "/ready", ""); code != http.StatusOK {
		t.Errorf("ready = %d", code)
	}
	if code, _ := harness.request(t, http.MethodGet,
		"/v1/contracts?id="+url.QueryEscape("process/bounded-subprocess/v1"), ""); code != http.StatusOK {
		t.Errorf("inspection = %d", code)
	}

	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{"normalize", "/v1/normalize", `{"input":"run child processes"}`},
		{"discover", "/v1/discover", `{"schema_version":1,"primitive_id":"p","contract_id":"c","providers":[{"id":"pkg.go.dev","queries":[{"text":"x","limit":1}]}]}`},
		{"enrich", "/v1/enrich", `{"specimen_ids":["public/pkg.go.dev/example@v1.0.0"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := harness.request(t, http.MethodPost, test.path, test.body)
			if status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%v", status, body)
			}
			if body["code"] != CodeExternalOperations {
				t.Errorf("code = %v, want %s", body["code"], CodeExternalOperations)
			}
		})
	}

	if harness.normalizer.calls != 0 {
		t.Errorf("model was called %d time(s) while disabled", harness.normalizer.calls)
	}
}

func marshalPolicy(t *testing.T, p Policy) string {
	t.Helper()
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	return string(encoded)
}

func mustRaw(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(bytes.TrimSpace(encoded))
}
