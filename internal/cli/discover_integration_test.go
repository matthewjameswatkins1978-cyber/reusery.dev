//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/pkggodev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

func writeFixtureJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// newPkgGoDevFixture serves the pkg.go.dev v1beta search and package shapes.
func newPkgGoDevFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/search"):
			writeFixtureJSON(w, `{"items":[
			  {"packagePath":"github.com/example/boundedproc","modulePath":"github.com/example","version":"v1.4.2","synopsis":"Package boundedproc runs subprocesses with bounded output."},
			  {"packagePath":"github.com/example/pipepool","modulePath":"github.com/example/pipepool","version":"v0.9.0","synopsis":"Package pipepool drains stdout and stderr concurrently."}
			],"total":2}`)
		case strings.HasPrefix(r.URL.Path, "/v1/package/"):
			path := strings.TrimPrefix(r.URL.Path, "/v1/package/")
			writeFixtureJSON(w, fmt.Sprintf(`{
			  "modulePath":"github.com/example","version":"v1.4.2","path":%q,"name":"pkg",
			  "synopsis":"Fixture package metadata.","isStandardLibrary":false,"isRedistributable":true
			}`, path))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newGitHubRepositoryFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, `{"total_count":2,"incomplete_results":false,"items":[
		  {"full_name":"example/go-subproc","html_url":"https://github.com/example/go-subproc",
		   "description":"Bounded subprocess execution for Go","language":"Go","archived":false,
		   "pushed_at":"2026-08-14T09:30:00Z","default_branch":"main",
		   "license":{"key":"apache-2.0","name":"Apache License 2.0","spdx_id":"Apache-2.0"},"stargazers_count":9999},
		  {"full_name":"example/proctree","html_url":"https://github.com/example/proctree",
		   "description":"Explicit process-tree termination","language":"Go","archived":true,
		   "pushed_at":"2024-02-02T00:00:00Z","default_branch":"trunk","license":null}
		]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func newGitHubCodeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, `{"total_count":2,"incomplete_results":false,"items":[
		  {"name":"exec.go","path":"internal/exec.go","sha":"1111111111111111111111111111111111111111",
		   "html_url":"https://github.com/example/go-subproc/blob/aaaa1111/internal/exec.go",
		   "repository":{"full_name":"example/go-subproc","html_url":"https://github.com/example/go-subproc"}},
		  {"name":"tree.go","path":"internal/tree.go","sha":"2222222222222222222222222222222222222222",
		   "html_url":"https://github.com/example/go-subproc/blob/aaaa1111/internal/tree.go",
		   "repository":{"full_name":"example/go-subproc","html_url":"https://github.com/example/go-subproc"}}
		]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestPublicDiscoveryIntegration exercises the whole Packet 5 path against
// real PostgreSQL with httptest provider fixtures. No public network, no mock
// database.
func TestPublicDiscoveryIntegration(t *testing.T) {
	ctx := context.Background()
	pool, databaseURL := startDatabase(t)
	app, stdout, stderr := newApp(t, pool, databaseURL)

	// 1. Seed the canonical primitive and contract.
	if code := app.Run(ctx, []string{"seed", "--root", repoRoot, "--manifest", filepath.FromSlash(manifestRel)}); code != ExitOK {
		t.Fatalf("seed exit = %d; stderr=%q", code, stderr.String())
	}
	pgStore := postgres.NewStore(pool)

	// 2. Providers call local fixtures, not the public internet.
	providers := []discovery.Provider{
		pkggodev.NewWithBaseURL(newPkgGoDevFixture(t).URL),
		github.NewRepositoryProvider(github.NewClientWithBaseURL(newGitHubRepositoryFixture(t).URL, "")),
		github.NewCodeProvider(github.NewClientWithBaseURL(newGitHubCodeFixture(t).URL, "")),
	}
	app.NewDiscoverer = func(store Store, _ config.Config, clock discovery.Clock) Discoverer {
		return discovery.NewService(store, clock, providers)
	}

	// 3. Run the real discovery profile through the real command.
	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "json"}
	if code := app.Run(ctx, args); code != ExitOK {
		t.Fatalf("discover exit = %d; stderr=%q", code, stderr.String())
	}

	var result discovery.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("discover output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(result.Providers) != 3 {
		t.Fatalf("provider reports = %d, want 3", len(result.Providers))
	}
	for _, report := range result.Providers {
		if !report.Succeeded {
			t.Errorf("provider %s failed against the fixture: %#v", report.ID, report)
		}
		if report.Requests == 0 {
			t.Errorf("provider %s issued no requests", report.ID)
		}
	}

	byProvider := map[string]int{}
	for _, candidate := range result.Candidates {
		byProvider[candidate.ProviderID]++
	}
	for _, id := range discovery.KnownProviderIDs() {
		if byProvider[id] == 0 {
			t.Errorf("provider %s discovered nothing; candidates by provider = %v", id, byProvider)
		}
	}

	// 4. Reload from PostgreSQL: provenance and explicit unknowns survived.
	licenceObservations, licenceUnknowns := 0, 0
	for _, candidate := range result.Candidates {
		stored, err := pgStore.GetSpecimen(ctx, candidate.Specimen.ID)
		if err != nil {
			t.Fatalf("reload specimen %s: %v", candidate.Specimen.ID, err)
		}
		if stored.Source.URL == "" {
			t.Errorf("specimen %s lost its source provenance", stored.ID)
		}
		if stored.PrimitiveID != "process/bounded-subprocess" {
			t.Errorf("specimen %s targets %q", stored.ID, stored.PrimitiveID)
		}

		evidence, err := pgStore.ListEvidenceBySubject(ctx, candidate.Specimen.ID)
		if err != nil {
			t.Fatalf("reload evidence for %s: %v", candidate.Specimen.ID, err)
		}
		if len(evidence) != len(candidate.Evidence) {
			t.Errorf("evidence for %s = %d, want %d", stored.ID, len(evidence), len(candidate.Evidence))
		}
		hasLicenceObservation := false
		for _, observation := range evidence {
			if observation.AppliesTo != "" {
				t.Errorf("evidence %s applies to %q; discovery never maps to a requirement", observation.ID, observation.AppliesTo)
			}
			if observation.Result != model.EvidenceInfo && observation.Result != model.EvidenceUnknown {
				t.Errorf("evidence %s has behavioural result %q", observation.ID, observation.Result)
			}
			if observation.Source.URL == "" && observation.Source.Path == "" {
				t.Errorf("evidence %s lost its provenance", observation.ID)
			}
			if observation.Kind == "source_license" {
				hasLicenceObservation = true
				licenceObservations++
				if observation.Result == model.EvidenceUnknown {
					licenceUnknowns++
				}
			}
		}
		if !hasLicenceObservation {
			t.Errorf("specimen %s has no licence observation at all", stored.ID)
		}
	}
	if licenceObservations == 0 {
		t.Error("no licence observations were persisted")
	}
	if licenceUnknowns == 0 {
		t.Error("no explicit licence unknown was persisted; absence must be recorded, not assumed")
	}

	// 5. Discovery never resolves.
	count, err := pgStore.CountResolutions(ctx)
	if err != nil {
		t.Fatalf("count resolutions: %v", err)
	}
	if count != 0 {
		t.Errorf("discovery stored %d resolutions, want 0", count)
	}

	// 6. The trust boundary: provider metadata alone cannot satisfy the
	// behavioural contract.
	contract, err := pgStore.GetContract(ctx, "process/bounded-subprocess/v1")
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	if len(contract.Requirements) != 11 {
		t.Fatalf("contract requirements = %d, want the canonical 11", len(contract.Requirements))
	}
	for _, candidate := range result.Candidates {
		specimen, err := pgStore.GetSpecimen(ctx, candidate.Specimen.ID)
		if err != nil {
			t.Fatalf("reload specimen: %v", err)
		}
		evidence, err := pgStore.ListEvidenceBySubject(ctx, specimen.ID)
		if err != nil {
			t.Fatalf("reload evidence: %v", err)
		}
		evaluation, err := resolver.Evaluate(contract, specimen, evidence)
		if err != nil {
			t.Fatalf("evaluate %s: %v", specimen.ID, err)
		}
		for _, requirement := range evaluation.Requirements {
			if requirement.Status != resolver.RequirementUnknown {
				t.Errorf("discovery metadata turned %s.%s into %q; discovery is not verification",
					specimen.ID, requirement.RequirementID, requirement.Status)
			}
		}
		if evaluation.HasFailures() {
			t.Errorf("%s has a failed requirement from discovery data", specimen.ID)
		}
	}
}

// TestPublicDiscoveryRerunIsStable proves a second run with the same run
// timestamp neither duplicates observations nor loses anything.
func TestPublicDiscoveryRerunIsStable(t *testing.T) {
	ctx := context.Background()
	pool, databaseURL := startDatabase(t)
	app, stdout, stderr := newApp(t, pool, databaseURL)

	if code := app.Run(ctx, []string{"seed", "--root", repoRoot, "--manifest", filepath.FromSlash(manifestRel)}); code != ExitOK {
		t.Fatalf("seed exit = %d; stderr=%q", code, stderr.String())
	}

	providers := []discovery.Provider{
		pkggodev.NewWithBaseURL(newPkgGoDevFixture(t).URL),
		github.NewRepositoryProvider(github.NewClientWithBaseURL(newGitHubRepositoryFixture(t).URL, "")),
		github.NewCodeProvider(github.NewClientWithBaseURL(newGitHubCodeFixture(t).URL, "")),
	}
	app.NewDiscoverer = func(store Store, _ config.Config, clock discovery.Clock) Discoverer {
		return discovery.NewService(store, clock, providers)
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "json"}
	if code := app.Run(ctx, args); code != ExitOK {
		t.Fatalf("first discover exit = %d; stderr=%q", code, stderr.String())
	}
	var first discovery.Result
	if err := json.Unmarshal(stdout.Bytes(), &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pgStore := postgres.NewStore(pool)
	specimenCount, evidenceCount := countRegistry(t, pgStore, first.Candidates)

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(ctx, args); code != ExitOK {
		t.Fatalf("second discover exit = %d; stderr=%q", code, stderr.String())
	}
	var second discovery.Result
	if err := json.Unmarshal(stdout.Bytes(), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(second.Candidates) != len(first.Candidates) {
		t.Errorf("candidates = %d on rerun, want %d", len(second.Candidates), len(first.Candidates))
	}
	specimens, evidence := countRegistry(t, pgStore, second.Candidates)
	if specimens != specimenCount || evidence != evidenceCount {
		t.Errorf("registry grew from %d/%d to %d/%d on an identical rerun",
			specimenCount, evidenceCount, specimens, evidence)
	}
}

func countRegistry(t *testing.T, store *postgres.Store, candidates []discovery.Candidate) (int, int) {
	t.Helper()
	specimens, evidence := 0, 0
	for _, candidate := range candidates {
		if _, err := store.GetSpecimen(context.Background(), candidate.Specimen.ID); err == nil {
			specimens++
		}
		observations, err := store.ListEvidenceBySubject(context.Background(), candidate.Specimen.ID)
		if err != nil {
			t.Fatalf("list evidence: %v", err)
		}
		evidence += len(observations)
	}
	return specimens, evidence
}
