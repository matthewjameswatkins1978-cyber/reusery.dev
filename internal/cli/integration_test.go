//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

// e2eImage is pinned deliberately; do not move to a beta major.
const e2eImage = "postgres:18.6-alpine"

// startDatabase brings up a real PostgreSQL, migrates it from zero and returns
// the pool plus an app wired to it. This is the real Packet 3 stack: no mocks.
func startDatabase(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, e2eImage,
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

// newApp builds a CLI application bound to an existing pool. Each call returns
// fresh buffers, which also demonstrates that state lives in PostgreSQL rather
// than in the process.
func newApp(t *testing.T, pool *pgxpool.Pool, databaseURL string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &App{
		Stdout: stdout,
		Stderr: stderr,
		LoadConfig: func() (config.Config, error) {
			return config.Config{
				HTTPAddr:    ":0",
				LogLevel:    slog.LevelInfo,
				DatabaseURL: databaseURL,
			}, nil
		},
		OpenStore: func(context.Context, config.Config) (Store, func(), error) {
			// The pool is shared and closed by the test, not by a command.
			return postgres.NewStore(pool), func() {}, nil
		},
		LoadBundle:    catalog.Load,
		LoadProfile:   discovery.LoadProfile,
		NewDiscoverer: app.NewDiscoverer,
		Serve: func(context.Context, config.Config, *slog.Logger) error {
			return nil
		},
		Clock: func() time.Time {
			return time.Date(2026, time.September, 25, 20, 0, 0, 0, time.UTC)
		},
	}
	return app, stdout, stderr
}

// TestEndToEndResolutionSlice runs the whole Packet 4 path against real
// PostgreSQL: migrate -> seed -> resolve -> persist -> inspect.
func TestEndToEndResolutionSlice(t *testing.T) {
	ctx := context.Background()
	pool, databaseURL := startDatabase(t)

	app, stdout, stderr := newApp(t, pool, databaseURL)

	// 1. Seed the repository-authored development bundle.
	seedArgs := []string{"seed", "--root", repoRoot, "--manifest", filepath.FromSlash(manifestRel)}
	if code := app.Run(ctx, seedArgs); code != ExitOK {
		t.Fatalf("seed exit = %d; stderr=%q", code, stderr.String())
	}

	// Re-seeding must be idempotent: still 11 observations per fixture.
	seedStore := postgres.NewStore(pool)
	partialEvidence, err := seedStore.ListEvidenceBySubject(ctx, "fixture/process/bounded-subprocess/partial-adapt")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(partialEvidence) != 11 {
		t.Fatalf("partial-adapt evidence = %d, want 11", len(partialEvidence))
	}
	stdout.Reset()
	stderr.Reset()
	if code := app.Run(ctx, seedArgs); code != ExitOK {
		t.Fatalf("second seed exit = %d; stderr=%q", code, stderr.String())
	}
	partialEvidence, err = seedStore.ListEvidenceBySubject(ctx, "fixture/process/bounded-subprocess/partial-adapt")
	if err != nil {
		t.Fatalf("list evidence after re-seed: %v", err)
	}
	if len(partialEvidence) != 11 {
		t.Fatalf("partial-adapt evidence after re-seed = %d, want 11 (append-only, no duplicates)", len(partialEvidence))
	}

	// 2. Resolve the example request: partial first, complete second.
	stdout.Reset()
	stderr.Reset()
	resolveArgs := []string{"resolve", "--request", filepath.Join(repoRoot, "examples", "resolve-bounded-subprocess.json"), "--format", "json"}
	if code := app.Run(ctx, resolveArgs); code != ExitOK {
		t.Fatalf("resolve exit = %d; stderr=%q", code, stderr.String())
	}

	var resolved struct {
		ResolutionID int64            `json:"resolution_id"`
		Resolution   model.Resolution `json:"resolution"`
		Considered   []struct {
			SpecimenID       string          `json:"specimen_id"`
			ReuseMode        model.ReuseMode `json:"reuse_mode"`
			Selected         bool            `json:"selected"`
			RejectionReasons []string        `json:"rejection_reasons"`
		} `json:"considered"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resolved); err != nil {
		t.Fatalf("resolve output is not JSON: %v\n%s", err, stdout.String())
	}

	if resolved.ResolutionID != 1 {
		t.Errorf("resolution_id = %d, want 1", resolved.ResolutionID)
	}
	if resolved.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q, want depend", resolved.Resolution.Outcome)
	}
	const completeID = "fixture/process/bounded-subprocess/complete-dependency"
	const partialID = "fixture/process/bounded-subprocess/partial-adapt"
	if resolved.Resolution.SpecimenID != completeID {
		t.Errorf("selected specimen = %q, want %q", resolved.Resolution.SpecimenID, completeID)
	}
	if len(resolved.Considered) != 2 {
		t.Fatalf("considered = %d, want 2", len(resolved.Considered))
	}
	if resolved.Considered[0].SpecimenID != partialID || resolved.Considered[0].Selected {
		t.Errorf("considered[0] = %#v, want rejected %q", resolved.Considered[0], partialID)
	}
	if !resolved.Considered[1].Selected {
		t.Errorf("considered[1] = %#v, want selected", resolved.Considered[1])
	}
	if len(resolved.Resolution.Rejected) != 1 || resolved.Resolution.Rejected[0].SpecimenID != partialID {
		t.Errorf("rejected = %#v, want %q", resolved.Resolution.Rejected, partialID)
	}
	if !containsString(resolved.Resolution.Rejected[0].Reasons, `required requirement "bounds-stderr" failed`) {
		t.Errorf("rejection reasons = %v, want the bounds-stderr failure", resolved.Resolution.Rejected[0].Reasons)
	}
	if !containsString(resolved.Resolution.Unknowns, partialID+": process-tree-semantics-explicit unknown") {
		t.Errorf("unknowns = %v, want the process-tree unknown", resolved.Resolution.Unknowns)
	}
	if len(resolved.Resolution.EvidenceIDs) == 0 {
		t.Error("resolution has no evidence IDs")
	}

	// 3. Inspect through a fresh process-shaped CLI: the decision is durable.
	inspectApp, inspectOut, inspectErr := newApp(t, pool, databaseURL)
	inspectArgs := []string{"resolution", "--id", "1", "--format", "json"}
	if code := inspectApp.Run(ctx, inspectArgs); code != ExitOK {
		t.Fatalf("resolution exit = %d; stderr=%q", code, inspectErr.String())
	}
	var inspection struct {
		ResolutionID int64            `json:"resolution_id"`
		Resolution   model.Resolution `json:"resolution"`
	}
	if err := json.Unmarshal(inspectOut.Bytes(), &inspection); err != nil {
		t.Fatalf("inspection output is not JSON: %v\n%s", err, inspectOut.String())
	}
	if inspection.Resolution.Outcome != model.OutcomeDepend || inspection.Resolution.SpecimenID != completeID {
		t.Errorf("inspection = %#v, want depend on %q", inspection.Resolution, completeID)
	}

	// 4. The same conclusion must be reachable through the Packet 3 store.
	loaded, err := postgres.NewStore(pool).GetResolution(ctx, 1)
	if err != nil {
		t.Fatalf("get resolution: %v", err)
	}
	if loaded.Outcome != model.OutcomeDepend || loaded.SpecimenID != completeID {
		t.Errorf("stored resolution = %#v, want depend on %q", loaded, completeID)
	}
	if len(loaded.Rejected) != 1 || loaded.Rejected[0].SpecimenID != partialID {
		t.Errorf("stored rejections = %#v, want %q", loaded.Rejected, partialID)
	}

	// 5. BUILD LOCALLY against the same real database.
	stdout.Reset()
	stderr.Reset()
	buildLocalArgs := []string{"resolve", "--request", filepath.Join(repoRoot, "examples", "resolve-bounded-subprocess-build-local.json"), "--format", "json"}
	if code := app.Run(ctx, buildLocalArgs); code != ExitOK {
		t.Fatalf("build-locally resolve exit = %d; stderr=%q", code, stderr.String())
	}
	var buildLocal struct {
		ResolutionID int64            `json:"resolution_id"`
		Resolution   model.Resolution `json:"resolution"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &buildLocal); err != nil {
		t.Fatalf("build-locally output is not JSON: %v\n%s", err, stdout.String())
	}
	if buildLocal.ResolutionID != 2 {
		t.Errorf("resolution_id = %d, want 2 (append-only history)", buildLocal.ResolutionID)
	}
	if buildLocal.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Errorf("outcome = %q, want build_locally", buildLocal.Resolution.Outcome)
	}
	if buildLocal.Resolution.SpecimenID != "" {
		t.Errorf("specimen = %q, want empty for build_locally", buildLocal.Resolution.SpecimenID)
	}
	if len(buildLocal.Resolution.Rejected) != 1 {
		t.Errorf("rejected = %#v, want the partial candidate", buildLocal.Resolution.Rejected)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
