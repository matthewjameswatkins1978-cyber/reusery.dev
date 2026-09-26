package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
)

// githubFixture serves the three endpoints a public scan uses and records
// every request so a test can prove nothing else was touched.
type githubFixture struct {
	server        *httptest.Server
	requests      atomic.Int64
	treeRequests  atomic.Int64
	lastAuth      atomic.Value
	private       bool
	defaultBranch string
	commitSHA     string
	rateLimited   bool
	malformed     bool
	missing       bool
	manifests     map[string]string
	paths         []string
	sleep         time.Duration
}

func newGitHubFixture(t *testing.T) *githubFixture {
	t.Helper()
	fixture := &githubFixture{
		private:       false,
		defaultBranch: "main",
		commitSHA:     "0123456789abcdef0123456789abcdef01234567",
		manifests:     map[string]string{},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)
		fixture.paths = append(fixture.paths, r.URL.Path)
		fixture.lastAuth.Store(r.Header.Get("Authorization"))
		if strings.Contains(r.URL.Path, "/git/") || strings.HasSuffix(r.URL.Path, "/git/trees") {
			fixture.treeRequests.Add(1)
		}
		if fixture.sleep > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(fixture.sleep):
			}
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case fixture.rateLimited:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		case fixture.malformed:
			_, _ = w.Write([]byte(`{"default_branch": `))
		case strings.Contains(r.URL.Path, "/contents/"):
			serveManifest(w, r, fixture)
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + fixture.commitSHA + `"}`))
		case strings.Count(r.URL.Path, "/") == 3:
			if fixture.missing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			if fixture.private {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"default_branch":"` + fixture.defaultBranch + `","private":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func serveManifest(w http.ResponseWriter, r *http.Request, fixture *githubFixture) {
	// /repos/{owner}/{repo}/contents/{path}
	marker := "/contents/"
	index := strings.Index(r.URL.Path, marker)
	if index < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	path := r.URL.Path[index+len(marker):]
	content, ok := fixture.manifests[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type":     "file",
		"encoding": "base64",
		"size":     len(content),
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(payload)
}

// client returns an UNAUTHENTICATED fixed-host client, exactly as production
// builds one. No credential is ever attached.
func (f *githubFixture) client() *github.Client {
	return github.NewClientWithBaseURL(f.server.URL, "")
}

func TestPublicScanReadsRootGoModAtAnImmutableCommit(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["go.mod"] = simpleGoMod

	result, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Project.SourceKind != SourceGitHubPublic {
		t.Errorf("source kind = %q", result.Project.SourceKind)
	}
	if result.Project.SourceLocator != "acme/widget" {
		t.Errorf("source locator = %q", result.Project.SourceLocator)
	}
	if result.SourceRevision != fixture.commitSHA {
		t.Errorf("source revision = %q, want the resolved commit SHA", result.SourceRevision)
	}
	if len(result.Fingerprint.Modules) != 1 || result.Fingerprint.Modules[0].ModulePath != "example.com/widget" {
		t.Fatalf("modules = %+v", result.Fingerprint.Modules)
	}
	if got := fixture.lastAuth.Load(); got != "" && got != nil {
		t.Errorf("public scanning attached an Authorization header: %v", got)
	}
	if fixture.treeRequests.Load() != 0 {
		t.Errorf("scanned %d tree endpoint(s); a manifest read must not crawl the repository", fixture.treeRequests.Load())
	}
	if fixture.requests.Load() > int64(MaxGitHubRequests) {
		t.Errorf("issued %d requests, budget is %d", fixture.requests.Load(), MaxGitHubRequests)
	}
}

func TestPublicScanUsesAnExplicitRef(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.defaultBranch = "some-other-branch"
	fixture.commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixture.manifests["go.mod"] = simpleGoMod

	result, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "v1.2.3", "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.SourceRevision != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("source revision = %q, want the explicitly requested ref's commit", result.SourceRevision)
	}
}

func TestPublicScanReadsSubdirectoryAndWorkspace(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["backend/go.work"] = "go 1.27.1\n\nuse (\n\t./svc\n)\n"
	fixture.manifests["backend/svc/go.mod"] = "module example.com/backend/svc\n\ngo 1.27.1\n"

	result, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "backend")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Project.SourceLocator != "acme/widget/backend" {
		t.Errorf("source locator = %q", result.Project.SourceLocator)
	}
	paths := ModulePaths(result.Fingerprint)
	if len(paths) != 1 || paths[0] != "example.com/backend/svc" {
		t.Fatalf("module paths = %v", paths)
	}
	if result.Fingerprint.Modules[0].GoVersion != "1.27.1" {
		t.Errorf("workspace go version not inherited: %+v", result.Fingerprint.Modules[0])
	}
}

func TestPublicScanRejectsPrivateRepositories(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.private = true
	fixture.manifests["go.mod"] = simpleGoMod

	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/secret", "", "")
	if !errors.Is(err, ErrPrivateRepositoryUnsupported) {
		t.Fatalf("err = %v, want ErrPrivateRepositoryUnsupported", err)
	}
	// No content request happens at all: the repository lookup 404s
	// anonymously and the scan stops there.
	for _, path := range fixture.paths {
		if strings.Contains(path, "/contents/") || strings.Contains(path, "/commits/") {
			t.Errorf("read %s from an inaccessible repository", path)
		}
	}
}

func TestPublicScanMissingRepositoryIsUnsupported(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.missing = true
	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/does-not-exist", "", "")
	if !errors.Is(err, ErrPrivateRepositoryUnsupported) {
		t.Errorf("err = %v, want ErrPrivateRepositoryUnsupported", err)
	}
	for _, path := range fixture.paths {
		if strings.Contains(path, "/contents/") {
			t.Errorf("read %s from a repository that does not exist", path)
		}
	}
}

func TestPublicScanMalformedResponseIsUnavailable(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.malformed = true
	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if !errors.Is(err, ErrGitHubUnavailable) {
		t.Errorf("err = %v, want ErrGitHubUnavailable", err)
	}
}

func TestPublicScanRateLimitIsReportedAsSuch(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.rateLimited = true
	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if !errors.Is(err, ErrGitHubRateLimited) {
		t.Errorf("err = %v, want ErrGitHubRateLimited", err)
	}
}

func TestPublicScanBoundByCancelledContext(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["go.mod"] = simpleGoMod
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ScanPublicGitHub(ctx, fixture.client(), "acme/widget", "", "")
	if err == nil {
		t.Fatal("expected a cancelled scan to fail")
	}
}

func TestPublicScanRejectsUnsafeSubdir(t *testing.T) {
	fixture := newGitHubFixture(t)
	for _, subdir := range []string{"../etc", "backend/../../etc", `backend\svc`, "/etc"} {
		_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", subdir)
		if !errors.Is(err, ErrInvalidProjectContext) {
			t.Errorf("subdir %q: err = %v, want ErrInvalidProjectContext", subdir, err)
		}
	}
}

func TestPublicScanRejectsMalformedRepositorySelector(t *testing.T) {
	fixture := newGitHubFixture(t)
	for _, repository := range []string{"", "acme", "acme/widget/extra", "/acme/widget", `acme\widget`, "../etc"} {
		_, err := ScanPublicGitHub(context.Background(), fixture.client(), repository, "", "")
		if !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("repository %q: err = %v, want ErrInvalidRepository", repository, err)
		}
	}
	if fixture.requests.Load() != 0 {
		t.Errorf("issued %d request(s) for a malformed selector", fixture.requests.Load())
	}
}

func TestPublicScanEnforcesTheResponseSizeBound(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["go.mod"] = simpleGoMod + "// " + strings.Repeat("x", MaxGitHubDecodedManifest)

	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func TestPublicScanBoundsWorkspaceModules(t *testing.T) {
	fixture := newGitHubFixture(t)
	work := strings.Builder{}
	work.WriteString("go 1.27.1\n\nuse (\n")
	for index := 0; index <= MaxWorkspaceModules; index++ {
		name := "m/" + itoa(index)
		fixture.manifests[name+"/go.mod"] = "module example.com/" + name + "\n\ngo 1.27.1\n"
		work.WriteString("\t./" + name + "\n")
	}
	work.WriteString(")\n")
	fixture.manifests["go.work"] = work.String()

	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
	if fixture.requests.Load() > int64(MaxGitHubRequests) {
		t.Errorf("issued %d requests, budget is %d", fixture.requests.Load(), MaxGitHubRequests)
	}
}

func TestPublicScanSkipsWorkspaceEntriesOutsideTheRepository(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["go.work"] = "go 1.27.1\n\nuse (\n\t./svc\n\t../outside\n)\n"
	fixture.manifests["svc/go.mod"] = "module example.com/svc\n\ngo 1.27.1\n"
	fixture.manifests["outside/go.mod"] = "module example.com/outside\n\ngo 1.27.1\n"

	result, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	paths := ModulePaths(result.Fingerprint)
	if len(paths) != 1 || paths[0] != "example.com/svc" {
		t.Fatalf("module paths = %v", paths)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected a warning for the skipped entry")
	}
}

// TestPublicAndLocalScanProduceTheSameFingerprint is the consistency rule:
// identical manifest facts must hash identically regardless of where they
// were read from. Only source metadata may differ.
func TestPublicAndLocalScanProduceTheSameFingerprint(t *testing.T) {
	fixture := newGitHubFixture(t)
	fixture.manifests["go.mod"] = simpleGoMod
	remote, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/widget", "", "")
	if err != nil {
		t.Fatalf("public scan: %v", err)
	}

	root := t.TempDir()
	writeManifest(t, root, "go.mod", simpleGoMod)
	local, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("local scan: %v", err)
	}

	if remote.FingerprintSHA256 != local.FingerprintSHA256 {
		t.Errorf("fingerprint differs by source: %s vs %s", remote.FingerprintSHA256, local.FingerprintSHA256)
	}
	// Identity comes from manifests alone, so both are the same project.
	if remote.Project.ID != local.Project.ID {
		t.Errorf("project ids differ: %q vs %q", remote.Project.ID, local.Project.ID)
	}
	// Source metadata is allowed to differ.
	if remote.Project.SourceKind == local.Project.SourceKind {
		t.Errorf("source kinds should differ: %q", remote.Project.SourceKind)
	}
	if remote.Project.SourceLocator == local.Project.SourceLocator {
		t.Errorf("source locators should differ: %q", remote.Project.SourceLocator)
	}
}

func TestPublicScanWithoutAnyManifestIsUnsupported(t *testing.T) {
	fixture := newGitHubFixture(t)
	_, err := ScanPublicGitHub(context.Background(), fixture.client(), "acme/empty", "", "")
	if !errors.Is(err, ErrUnsupportedProjectContext) {
		t.Errorf("err = %v, want ErrUnsupportedProjectContext", err)
	}
}
