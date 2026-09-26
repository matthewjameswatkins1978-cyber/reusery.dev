package project

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
)

// GitHub client failures. Each maps onto a distinct, safe tool error so an
// outage is never reported as a project-context problem.
var (
	// ErrGitHubRateLimited means GitHub refused the scan on quota.
	ErrGitHubRateLimited = errors.New("project: public GitHub rate limit reached")
	// ErrGitHubUnavailable means GitHub returned a server or auth failure
	// while serving a public repository.
	ErrGitHubUnavailable = errors.New("project: public GitHub is unavailable")
)

// GitHubClient is the narrow read surface a public project scan needs. It is
// satisfied by the existing fixed-host GitHub REST client, so tests inject an
// httptest server exactly as the discovery providers do.
//
// The client is constructed WITHOUT a credential: a private or inaccessible
// repository must fail as unsupported rather than become readable, because
// private access is Packet 13 scope.
type GitHubClient interface {
	Get(ctx context.Context, maxResponseBytes int64, path string, into any) (httpx.Response, error)
}

// githubRepo is the subset of repository metadata a public scan reads.
type githubRepo struct {
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// githubCommit resolves a ref to an immutable commit SHA.
type githubCommit struct {
	SHA string `json:"sha"`
}

// githubContent is one file returned by the contents API.
type githubContent struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Size     int    `json:"size"`
}

// ScanPublicGitHub derives a project fingerprint from a PUBLIC repository
// without cloning it, without downloading source and without a credential.
//
// It resolves the requested or default branch to an immutable commit SHA and
// then reads only go.work and the go.mod files that workspace references,
// always at that SHA. No recursive tree crawl, no source blobs, no checkout,
// no git executable.
func ScanPublicGitHub(ctx context.Context, client GitHubClient, repository, ref, subdir string) (ScanResult, error) {
	ctx, cancel := context.WithTimeout(ctx, GitHubScanTimeout)
	defer cancel()

	if client == nil {
		return ScanResult{}, ErrGitHubUnavailable
	}
	owner, repo, err := splitRepository(repository)
	if err != nil {
		return ScanResult{}, err
	}
	prefix, err := sanitiseSubdir(subdir)
	if err != nil {
		return ScanResult{}, err
	}

	reader := &remoteReader{client: client, owner: owner, repo: repo, remaining: MaxGitHubRequests}

	info, err := reader.repository(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	target := strings.TrimSpace(ref)
	if target == "" {
		target = info.DefaultBranch
	}
	if strings.TrimSpace(target) == "" {
		return ScanResult{}, fmt.Errorf("%w: repository declares no default branch", ErrInvalidProjectContext)
	}
	sha, err := reader.commit(ctx, target)
	if err != nil {
		return ScanResult{}, err
	}
	reader.sha = sha

	work, manifests, warnings, err := reader.collect(ctx, prefix)
	if err != nil {
		return ScanResult{}, err
	}
	inputs, err := reader.readModules(ctx, manifests, prefix)
	if err != nil {
		return ScanResult{}, err
	}
	modules, err := modulesFrom(inputs, work)
	if err != nil {
		return ScanResult{}, err
	}

	fingerprint := Fingerprint{
		SchemaVersion: SchemaVersion,
		Language:      LanguageGo,
		Modules:       modules,
	}
	locator := repository
	if prefix != "" {
		locator = repository + "/" + strings.TrimSuffix(prefix, "/")
	}
	return finish(fingerprint, SourceGitHubPublic, locator, sha, warnings)
}

// splitRepository validates the owner/repo selector.
func splitRepository(repository string) (owner, repo string, err error) {
	trimmed := strings.TrimSpace(repository)
	if trimmed == "" || strings.ContainsAny(trimmed, `\ `) || strings.HasPrefix(trimmed, "/") {
		return "", "", fmt.Errorf("%w: got %q", ErrInvalidRepository, repository)
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", "", fmt.Errorf("%w: got %q", ErrInvalidRepository, repository)
	}
	return parts[0], parts[1], nil
}

// sanitiseSubdir validates an optional repository subdirectory. It is a
// repository-relative path; absolute paths, traversal and Windows separators
// are all rejected before any request is made.
func sanitiseSubdir(subdir string) (string, error) {
	raw := strings.TrimSpace(subdir)
	if raw == "" {
		return "", nil
	}
	// An absolute-looking subdir is rejected rather than quietly relativised.
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(raw, "..") {
		return "", fmt.Errorf("%w: subdir %q is not a safe repository-relative path", ErrInvalidProjectContext, subdir)
	}
	trimmed := strings.Trim(raw, "/")
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: subdir %q is not a safe repository-relative path", ErrInvalidProjectContext, subdir)
		}
	}
	return trimmed + "/", nil
}

// remoteReader reads an explicitly enumerated set of repository-relative
// manifest files at one immutable commit, under the same bounds the local
// scanner uses plus a request and wall-clock budget.
type remoteReader struct {
	client    GitHubClient
	owner     string
	repo      string
	sha       string
	remaining int
	files     int
	bytes     int
}

// repository reads the repository metadata and refuses private repositories
// before any content request is made.
func (r *remoteReader) repository(ctx context.Context) (githubRepo, error) {
	var info githubRepo
	status, err := r.get(ctx, fmt.Sprintf("/repos/%s/%s", r.owner, r.repo), &info)
	if err != nil {
		return githubRepo{}, err
	}
	switch {
	case status == 404 || status == 451 || info.Private:
		return githubRepo{}, ErrPrivateRepositoryUnsupported
	case status != 200:
		return githubRepo{}, classifyGitHubStatus(status)
	}
	return info, nil
}

// commit resolves a ref to an immutable SHA so every later read is pinned.
func (r *remoteReader) commit(ctx context.Context, ref string) (string, error) {
	var commit githubCommit
	status, err := r.get(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s", r.owner, r.repo, url.PathEscape(ref)), &commit)
	if err != nil {
		return "", err
	}
	switch {
	case status == 404 || status == 451:
		return "", ErrPrivateRepositoryUnsupported
	case status != 200:
		return "", classifyGitHubStatus(status)
	}
	if strings.TrimSpace(commit.SHA) == "" {
		return "", fmt.Errorf("%w: ref %q resolved to no commit", ErrInvalidProjectContext, ref)
	}
	return commit.SHA, nil
}

// collect finds go.work or go.mod at the repository prefix and follows the
// workspace's use entries, all at the resolved commit.
func (r *remoteReader) collect(ctx context.Context, prefix string) (*modfile.WorkFile, []string, []string, error) {
	workPath := prefix + "go.work"
	work, err := r.readWork(ctx, workPath)
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, nil, nil, err
	}
	if work == nil {
		modulePath := prefix + "go.mod"
		if _, err := r.read(ctx, modulePath); errors.Is(err, errNotFound) {
			return nil, nil, nil, ErrUnsupportedProjectContext
		} else if err != nil {
			return nil, nil, nil, err
		}
		return nil, []string{modulePath}, nil, nil
	}

	manifests := make([]string, 0, len(work.Use))
	warnings := []string{}
	for _, use := range work.Use {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: scan cancelled", ErrInvalidProjectContext)
		}
		dir, warning, ok := safeWorkspaceUse(prefix, use.Path)
		if !ok {
			warnings = append(warnings, warning)
			continue
		}
		modulePath := dir + "go.mod"
		if _, err := r.read(ctx, modulePath); errors.Is(err, errNotFound) {
			warnings = append(warnings, "workspace use entry has no go.mod and was skipped")
			continue
		} else if err != nil {
			return nil, nil, nil, err
		}
		manifests = append(manifests, modulePath)
		if len(manifests) > MaxWorkspaceModules {
			return nil, nil, nil, fmt.Errorf("%w: workspace declares more than %d modules",
				ErrInvalidProjectContext, MaxWorkspaceModules)
		}
	}
	return work, manifests, warnings, nil
}

// safeWorkspaceUse keeps a go.work use entry inside the repository prefix.
// Repository-relative paths only; the warning never contains a path.
func safeWorkspaceUse(prefix, usePath string) (string, string, bool) {
	trimmed := strings.TrimSpace(usePath)
	if trimmed == "" {
		return "", "empty workspace use entry was skipped", false
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.Contains(trimmed, "..") {
		return "", "workspace use entry outside the repository was skipped", false
	}
	trimmed = strings.TrimPrefix(trimmed, "./")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", "workspace use entry outside the repository was skipped", false
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", "workspace use entry outside the repository was skipped", false
		}
	}
	return prefix + trimmed + "/", "", true
}

// readWork returns nil when the repository simply has no workspace file.
func (r *remoteReader) readWork(ctx context.Context, path string) (*modfile.WorkFile, error) {
	data, err := r.read(ctx, path)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	work, err := modfile.ParseWork(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: go.work is not valid: %v", ErrInvalidProjectContext, err)
	}
	return work, nil
}

// readModules reads the enumerated go.mod files into parse inputs.
func (r *remoteReader) readModules(ctx context.Context, paths []string, prefix string) ([]manifestInput, error) {
	inputs := make([]manifestInput, 0, len(paths))
	for _, path := range paths {
		data, err := r.read(ctx, path)
		if err != nil {
			return nil, err
		}
		label := strings.TrimPrefix(path, prefix)
		if label == "" {
			label = path
		}
		inputs = append(inputs, manifestInput{Label: label, Data: data})
	}
	return inputs, nil
}

// errNotFound marks a repository-relative path that does not exist.
var errNotFound = errors.New("project: file not found in repository")

// read fetches one file at the resolved commit, enforcing the response-size,
// decoded-size, file-count and request budgets.
func (r *remoteReader) read(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan cancelled", ErrInvalidProjectContext)
	}
	var content githubContent
	status, err := r.get(ctx, fmt.Sprintf("/repos/%s/%s/contents/%s", r.owner, r.repo, escapePath(path)), &content)
	if err != nil {
		return nil, err
	}
	switch {
	case status == 404:
		return nil, errNotFound
	case status == 451:
		return nil, ErrPrivateRepositoryUnsupported
	case status != 200:
		return nil, classifyGitHubStatus(status)
	}
	if content.Type != "" && content.Type != "file" {
		return nil, fmt.Errorf("%w: %s is not a file", ErrInvalidProjectContext, path)
	}
	if content.Size > MaxGitHubDecodedManifest {
		return nil, fmt.Errorf("%w: manifest %s exceeds %d bytes",
			ErrInvalidProjectContext, path, MaxGitHubDecodedManifest)
	}
	if content.Encoding != "base64" {
		return nil, fmt.Errorf("%w: manifest %s has unexpected encoding", ErrInvalidProjectContext, path)
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("%w: manifest %s is not valid base64", ErrInvalidProjectContext, path)
	}
	if len(data) > MaxGitHubDecodedManifest {
		return nil, fmt.Errorf("%w: manifest %s exceeds %d bytes",
			ErrInvalidProjectContext, path, MaxGitHubDecodedManifest)
	}

	r.files++
	if r.files > MaxManifestFiles {
		return nil, fmt.Errorf("%w: more than %d manifest files", ErrInvalidProjectContext, MaxManifestFiles)
	}
	if r.bytes+len(data) > MaxTotalManifestBytes {
		return nil, fmt.Errorf("%w: manifests exceed %d bytes total", ErrInvalidProjectContext, MaxTotalManifestBytes)
	}
	r.bytes += len(data)
	return data, nil
}

// get issues one bounded request and tracks the request budget.
func (r *remoteReader) get(ctx context.Context, path string, into any) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("%w: more than %d requests", ErrInvalidProjectContext, MaxGitHubRequests)
	}
	r.remaining--
	response, err := r.client.Get(ctx, MaxGitHubResponseBytes, path, into)
	// A non-2xx is reported by the shared HTTP helper as both a response and
	// an error; the status is what the caller needs to classify it.
	var statusErr *httpx.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode, nil
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %v", ErrGitHubUnavailable, err)
	}
	return response.StatusCode, nil
}

func classifyGitHubStatus(status int) error {
	switch status {
	case 403, 429:
		return ErrGitHubRateLimited
	default:
		return ErrGitHubUnavailable
	}
}

// escapePath escapes each path segment while preserving separators, so a
// repository-relative path such as backend/go.mod reaches the API intact.
func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
