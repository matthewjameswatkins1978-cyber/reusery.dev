//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	internalapp "github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

// projectChooseSpecimen is the candidate the canonical choose request offers
// and therefore the candidate a remembered preference is about.
const projectChooseSpecimen = "fixture/process/bounded-subprocess/complete-dependency"

// newProjectApp wires the full application against real PostgreSQL with every
// service the project sub-commands need.
func newProjectApp(t *testing.T, pool *pgxpool.Pool, databaseURL string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	app, stdout, stderr := newApp(t, pool, databaseURL)
	app.LoadPolicy = policy.Load
	app.LoadFeedback = policy.LoadFeedback
	app.NewProjectService = func(store Store, clock func() time.Time) (*project.Service, error) {
		return internalapp.NewProjectService(store, clock)
	}
	return app, stdout, stderr
}

// runProject runs one command and returns the parsed JSON document on stdout.
// A command's JSON must be a single document on stdout with diagnostics on
// stderr, so anything else fails the test.
func runProject(t *testing.T, app *App, stdout, stderr *bytes.Buffer, args ...string) map[string]any {
	t.Helper()
	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d for %v; stderr=%q", code, args, stderr.String())
	}
	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("stdout for %v is not one JSON document: %v\n%s", args, err, stdout.String())
	}
	return document
}

// TestProjectCLIEndToEndWithRealPostgreSQL walks the whole Packet 10 CLI
// surface against real PostgreSQL: scan, show, choose, remember, history,
// forget, show again. Every JSON document is parsed from stdout, so a command
// that mixed diagnostics into its output would fail here.
func TestProjectCLIEndToEndWithRealPostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool, databaseURL := startDatabase(t)
	app, stdout, stderr := newProjectApp(t, pool, databaseURL)

	if code := app.Run(ctx, []string{
		"seed", "--root", repoRoot, "--manifest", filepath.FromSlash(manifestRel),
	}); code != ExitOK {
		t.Fatalf("seed exit = %d", code)
	}

	// 1. scan a real Go project from a temp root.
	projectRoot := t.TempDir()
	manifest := "module example.com/widget\n\ngo 1.27.1\n\nrequire github.com/example/dep v1.4.0\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	scan := runProject(t, app, stdout, stderr, "project", "scan",
		"--root", projectRoot, "--format", "json")
	projectID, _ := scan["project_id"].(string)
	if !strings.HasPrefix(projectID, "project/go/") {
		t.Fatalf("project_id = %q", projectID)
	}
	if scan["source_kind"] != "local" {
		t.Errorf("source_kind = %v, want local", scan["source_kind"])
	}
	if locator, _ := scan["source_locator"].(string); locator != "" {
		t.Errorf("a local scan must never store a locator, got %q", locator)
	}
	if bytes.Contains(stdout.Bytes(), []byte(projectRoot)) {
		t.Errorf("the scan output contains the local root: %s", stdout.String())
	}

	// 2. show the project: nothing remembered yet.
	view := runProject(t, app, stdout, stderr, "project", "show",
		"--project-id", projectID, "--format", "json")
	if active, ok := view["active_preferences"].([]any); !ok || len(active) != 0 {
		t.Errorf("active_preferences = %v, want none", view["active_preferences"])
	}

	// Give the complete fixture a pinned revision so provenance is satisfied,
	// exactly as the offline choose test does.
	store := postgres.NewStore(pool)
	specimen, err := store.GetSpecimen(ctx, projectChooseSpecimen)
	if err != nil {
		t.Fatalf("load specimen: %v", err)
	}
	specimen.Source.Revision = "v1.0.0"
	if err := store.UpsertSpecimen(ctx, specimen); err != nil {
		t.Fatalf("pin revision: %v", err)
	}

	// A permissive policy keeps the fixture decision deterministic: the
	// canonical baseline would legitimately return needs_verification for
	// these development fixtures, which would persist nothing to inspect.
	chooseRoot := t.TempDir()
	requestDocument, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(chooseRequestRel)))
	if err != nil {
		t.Fatalf("read choose request: %v", err)
	}
	writeChooseRequest(t, chooseRoot, "request.json", string(requestDocument))
	if err := os.WriteFile(filepath.Join(chooseRoot, "policy.yaml"),
		[]byte(permissivePolicyYAML()), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	// 3. choose without project context: Packet 7 output, no project fields.
	plain := runProject(t, app, stdout, stderr, "choose",
		"--root", chooseRoot,
		"--request", "request.json", "--policy", "policy.yaml", "--format", "json")
	if _, present := plain["project_id"]; present {
		t.Errorf("choose without --project-id reported project context: %v", plain["project_id"])
	}
	plainID, _ := plain["resolution_id"].(float64)
	if plainID == 0 {
		t.Fatalf("the baseline choose persisted nothing: status=%v", plain["status"])
	}

	// 4. choose with the same request under the project's context.
	contextual := runProject(t, app, stdout, stderr, "choose",
		"--root", chooseRoot,
		"--request", "request.json", "--policy", "policy.yaml",
		"--project-id", projectID, "--format", "json")
	if contextual["project_id"] != projectID {
		t.Errorf("project_id = %v, want %s", contextual["project_id"], projectID)
	}
	hash, _ := contextual["project_context_hash"].(string)
	if hash == "" {
		t.Fatal("a project-aware choose must record the context hash it saw")
	}
	effects, _ := contextual["project_effects"].([]any)
	if len(effects) == 0 {
		t.Errorf("project_effects = %v, want one entry per candidate", contextual["project_effects"])
	}
	resolutions := runProject(t, app, stdout, stderr, "project", "history",
		"--project-id", projectID, "--format", "json")
	entries, _ := resolutions["resolutions"].([]any)
	if len(entries) != 1 {
		t.Fatalf("history entries = %d, want the one project-aware decision", len(entries))
	}
	first, _ := entries[0].(map[string]any)
	if first["project_context_hash"] != hash {
		t.Errorf("stored context hash = %v, want %s", first["project_context_hash"], hash)
	}

	// 5. remember an explicit preference about the selected candidate.
	remembered := runProject(t, app, stdout, stderr, "project", "remember",
		"--project-id", projectID,
		"--primitive-id", "process/bounded-subprocess",
		"--candidate-id", projectChooseSpecimen,
		"--reason", "not_quite",
		"--format", "json")
	created, _ := remembered["created"].(bool)
	if !created {
		t.Fatalf("the first remember must create: %v", remembered)
	}
	if remembered["kind"] != "exclude_candidate" {
		t.Errorf("kind = %v, want exclude_candidate", remembered["kind"])
	}
	preferenceID, ok := remembered["id"].(float64)
	if !ok || preferenceID == 0 {
		t.Fatalf("preference id = %v", remembered["id"])
	}

	// remembering the same thing twice reuses the active row
	again := runProject(t, app, stdout, stderr, "project", "remember",
		"--project-id", projectID,
		"--primitive-id", "process/bounded-subprocess",
		"--candidate-id", projectChooseSpecimen,
		"--reason", "not_quite",
		"--format", "json")
	if againCreated, _ := again["created"].(bool); againCreated {
		t.Error("remembering twice must not create a second row")
	}

	// 6. the memory is active and visible in show.
	view = runProject(t, app, stdout, stderr, "project", "show",
		"--project-id", projectID, "--format", "json")
	active, _ := view["active_preferences"].([]any)
	if len(active) != 1 {
		t.Fatalf("active preferences = %d, want 1", len(active))
	}

	// 7. forget revokes rather than deletes.
	forgotten := runProject(t, app, stdout, stderr, "project", "forget",
		"--project-id", projectID,
		"--preference-id", strconv.FormatInt(int64(preferenceID), 10),
		"--format", "json")
	if activeFlag, _ := forgotten["active"].(bool); activeFlag {
		t.Error("a forgotten preference must not be active")
	}

	// 8. show again: nothing active, one remembered and revoked.
	view = runProject(t, app, stdout, stderr, "project", "show",
		"--project-id", projectID, "--format", "json")
	if active, _ := view["active_preferences"].([]any); len(active) != 0 {
		t.Errorf("active preferences after forget = %v, want none", view["active_preferences"])
	}
	if forgottenCount, _ := view["forgotten_preferences"].(float64); int(forgottenCount) != 1 {
		t.Errorf("forgotten_preferences = %v, want 1", view["forgotten_preferences"])
	}

	// 9. an unknown project fails as an execution error, not a usage error.
	stdout.Reset()
	stderr.Reset()
	if code := app.Run(ctx, []string{"project", "show", "--project-id", "project/go/missing",
		"--format", "json"}); code != ExitError {
		t.Errorf("missing project exit = %d, want %d (stderr=%q)", code, ExitError, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a failing command must keep stdout empty, got %q", stdout.String())
	}
}
