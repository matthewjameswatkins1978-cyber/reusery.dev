//go:build integration

package postgres

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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
)

// projectManifest is the one manifest both scans below read: same bytes from
// the local checkout and from the public repository, so both must derive the
// same fingerprint, the same project id and the same stored row.
const projectManifest = "module example.com/widget\n\ngo 1.27.1\n\nrequire github.com/example/dep v1.4.0\n"

// writeManifestProject creates a local checkout of projectManifest.
func writeManifestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(projectManifest), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return root
}

// manifestGitHub is an unauthenticated public repository fixture that records
// every request so a test can prove the scan stayed inside the allowed set.
type manifestGitHub struct {
	server   *httptest.Server
	commit   string
	paths    []string
	lastAuth atomic.Value
}

func newManifestGitHub(t *testing.T) *manifestGitHub {
	t.Helper()
	fixture := &manifestGitHub{commit: "0123456789abcdef0123456789abcdef01234567"}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.paths = append(fixture.paths, r.URL.Path)
		fixture.lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/contents/go.mod"):
			payload, _ := json.Marshal(map[string]any{
				"type": "file", "encoding": "base64", "size": len(projectManifest),
				"content": base64.StdEncoding.EncodeToString([]byte(projectManifest)),
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

func projectTestService(t *testing.T, store *Store, remote *manifestGitHub) *project.Service {
	t.Helper()
	fixed := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	return project.NewService(store, project.Options{
		Clock: func() time.Time { return fixed },
		NewGitHubClient: func() project.GitHubClient {
			return github.NewClientWithBaseURL(remote.server.URL, "")
		},
	})
}

// TestIntegrationPublicAndLocalScanProduceTheSameFingerprint is the storage
// half of the determinism rule: identical manifest facts from a local checkout
// and from a public repository identify one project, one fingerprint row and
// no filesystem path anywhere.
func TestIntegrationPublicAndLocalScanProduceTheSameFingerprint(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()
	pool := store.pool
	root := writeManifestProject(t)
	remote := newManifestGitHub(t)
	service := projectTestService(t, store, remote)

	localResult, err := service.ScanLocal(ctx, root)
	if err != nil {
		t.Fatalf("local scan: %v", err)
	}
	publicResult, err := service.ScanPublicGitHub(ctx, "acme/widget", "", "")
	if err != nil {
		t.Fatalf("public scan: %v", err)
	}

	if localResult.FingerprintSHA256 != publicResult.FingerprintSHA256 {
		t.Errorf("fingerprint differs: local %s, public %s",
			localResult.FingerprintSHA256, publicResult.FingerprintSHA256)
	}
	if localResult.Project.ID != publicResult.Project.ID {
		t.Errorf("project id differs: local %q, public %q",
			localResult.Project.ID, publicResult.Project.ID)
	}
	if !publicResult.Reused {
		t.Error("an identical fingerprint must reuse the stored row")
	}
	if publicResult.SourceRevision != remote.commit {
		t.Errorf("source revision = %q, want the resolved commit SHA", publicResult.SourceRevision)
	}
	if localResult.Project.SourceLocator != "" {
		t.Errorf("local source locator = %q, want empty", localResult.Project.SourceLocator)
	}
	if publicResult.Project.SourceLocator != "acme/widget" {
		t.Errorf("public source locator = %q", publicResult.Project.SourceLocator)
	}

	var fingerprints int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM project_fingerprints").Scan(&fingerprints); err != nil {
		t.Fatalf("count fingerprints: %v", err)
	}
	if fingerprints != 1 {
		t.Errorf("fingerprint rows = %d, want 1", fingerprints)
	}
	var projects int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM projects").Scan(&projects); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projects != 1 {
		t.Errorf("project rows = %d, want 1", projects)
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

	// No table may ever contain the local root.
	for _, table := range []string{"projects", "project_fingerprints", "project_preferences", "project_contexts"} {
		columns, err := textColumns(ctx, pool, table)
		if err != nil {
			t.Fatalf("columns of %s: %v", table, err)
		}
		for _, column := range columns {
			var hits int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM `+table+` WHERE `+column+`::text LIKE $1`, "%"+root+"%",
			).Scan(&hits); err != nil {
				t.Fatalf("scan %s.%s for the local root: %v", table, column, err)
			}
			if hits != 0 {
				t.Errorf("%s.%s leaks the local root", table, column)
			}
		}
	}
}

// textColumns lists the text columns of one table for a leak assertion.
func textColumns(ctx context.Context, pool *pgxpool.Pool, table string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		  AND data_type IN ('text', 'character varying', 'jsonb', 'json')
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

// TestIntegrationProjectPreferenceSurvivesRevocation covers the storage rules
// for memory: an active preference is scoped to its project, forgetting keeps
// the row, and a revoked preference never reappears in the active list.
func TestIntegrationProjectPreferenceSurvivesRevocation(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()
	root := writeManifestProject(t)
	remote := newManifestGitHub(t)
	service := projectTestService(t, store, remote)

	scan, err := service.ScanLocal(ctx, root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	projectID := scan.Project.ID

	if err := store.UpsertPrimitive(ctx, fixturePrimitive()); err != nil {
		t.Fatalf("upsert primitive: %v", err)
	}
	if err := store.UpsertContract(ctx, fixtureContract()); err != nil {
		t.Fatalf("upsert contract: %v", err)
	}
	if err := store.UpsertSpecimen(ctx, fixtureSpecimen()); err != nil {
		t.Fatalf("upsert specimen: %v", err)
	}

	remembered, created, err := service.Remember(ctx, project.RememberRequest{
		ProjectID: projectID, PrimitiveID: fixtureSpecimen().PrimitiveID,
		CandidateID: fixtureSpecimen().ID, Reason: "not_quite",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !created || remembered.ID == 0 || !remembered.Active() {
		t.Fatalf("remembered = %+v, created = %v", remembered, created)
	}
	if remembered.SourceReason != "not_quite" {
		t.Errorf("source reason = %q, want not_quite", remembered.SourceReason)
	}

	active, err := store.ListActivePreferences(ctx, projectID, project.MaxHistoryLimit)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active preferences = %d, want 1", len(active))
	}

	// Forgetting is a revoke, never a delete.
	if _, err := service.Forget(ctx, projectID, remembered.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	active, err = store.ListActivePreferences(ctx, projectID, project.MaxHistoryLimit)
	if err != nil {
		t.Fatalf("active after forget: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active preferences after forget = %d, want 0", len(active))
	}
	all, err := store.ListPreferences(ctx, projectID, project.MaxHistoryLimit)
	if err != nil {
		t.Fatalf("list after forget: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("stored preferences = %d, want the revoked row to survive", len(all))
	}
	if all[0].Active() {
		t.Error("the revoked row must stay revoked")
	}

	// A different project cannot see another project's memory.
	other := projectID + "-other"
	if _, err := store.GetPreference(ctx, other, remembered.ID); err == nil {
		t.Error("a preference must be scoped to exactly one project")
	}
}
