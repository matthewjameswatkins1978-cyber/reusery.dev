// Package githubmeta enriches public GitHub repository and code specimens
// with attributable repository metadata from the GitHub REST API.
//
// It reuses Packet 5's shared GitHub client, so there is exactly one GitHub
// HTTP stack: same optional token, same API version header, same rate-limit
// handling, same fixed production host and same safe redirect rules.
//
// Everything it returns is attributable INFO or UNKNOWN observation. Whether a
// repository is archived or recently pushed says nothing about whether its
// code satisfies a behavioural contract requirement.
package githubmeta

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

const (
	// DefaultBaseURL is the code-owned production host.
	DefaultBaseURL = github.DefaultBaseURL

	repositorySpecimenPrefix = "public/github/repository/"
	codeSpecimenPrefix       = "public/github/code/"
)

// Provider reads repository metadata for GitHub repository and code specimens.
type Provider struct {
	client  *github.Client
	baseURL string
}

// New builds the production provider over an optional token. Without a token
// GitHub's public unauthenticated limits apply.
func New(token string) *Provider {
	return NewWithBaseURL(DefaultBaseURL, token)
}

// NewWithBaseURL builds a provider against an explicit base URL. Tests inject
// an httptest server here; production never calls it.
func NewWithBaseURL(baseURL, token string) *Provider {
	return &Provider{
		client:  github.NewClientWithBaseURL(baseURL, token),
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// ID returns the stable provider identifier.
func (p *Provider) ID() string { return enrichment.ProviderGitHubMetadata }

// Supports reports whether this provider can enrich one specimen. Both a
// repository candidate and a code candidate map to their parent repository;
// neither ever has its own identity rewritten.
func (p *Provider) Supports(specimen model.Specimen) bool {
	return strings.HasPrefix(specimen.ID, repositorySpecimenPrefix) ||
		strings.HasPrefix(specimen.ID, codeSpecimenPrefix)
}

// repositoryResponse is the subset of GET /repos/{owner}/{repo} Packet 7 uses.
//
// Popularity signals are deliberately not decoded: stars, fork counts and
// watchers are not quality evidence and never reach ordering, policy or
// selection.
type repositoryResponse struct {
	Archived      *bool       `json:"archived"`
	PushedAt      string      `json:"pushed_at"`
	DefaultBranch string      `json:"default_branch"`
	License       *licenseRef `json:"license"`
}

type licenseRef struct {
	SPDXID string `json:"spdx_id"`
}

// Enrich reads one repository endpoint for one specimen.
func (p *Provider) Enrich(ctx context.Context, request enrichment.Request) (enrichment.ProviderResult, error) {
	var result enrichment.ProviderResult

	fullName, ok := repositoryOf(request.Specimen)
	if !ok {
		result.Issues = append(result.Issues, enrichment.ProviderIssue{
			Kind:       enrichment.IssueIdentity,
			Provider:   p.ID(),
			SpecimenID: request.Specimen.ID,
			Message:    "the specimen identity does not encode a GitHub owner/repository pair; repository metadata was skipped rather than guessed",
		})
		return result, nil
	}
	if request.Budget.MaxHTTPRequests < 1 {
		result.Issues = append(result.Issues, enrichment.ProviderIssue{
			Kind:       enrichment.IssueBudgetExhausted,
			Provider:   p.ID(),
			SpecimenID: request.Specimen.ID,
			Message: fmt.Sprintf("request budget of %d cannot cover the repository read; nothing was sent",
				request.Budget.MaxHTTPRequests),
		})
		return result, nil
	}

	owner, repository, _ := strings.Cut(fullName, "/")
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)

	var payload repositoryResponse
	result.Requests++
	if _, err := p.client.Get(ctx, request.Budget.MaxResponseBytes, path, &payload); err != nil {
		return result, err
	}

	source := model.SourceRef{URL: p.baseURL + path}
	obs := func(kind, claim string, outcome model.EvidenceResult, value any) model.Evidence {
		artifact := ""
		if value != nil {
			if encoded, err := policy.EncodeFactArtifact(value); err == nil {
				artifact = encoded
			}
		}
		return enrichment.NewObservation(enrichment.ObservationSpec{
			ProviderID:  p.ID(),
			SubjectID:   request.Specimen.ID,
			Kind:        kind,
			Claim:       claim,
			Result:      outcome,
			Source:      source,
			ObservedAt:  request.ObservedAt,
			Methodology: enrichment.MethodologyGitHubMetadata,
			Artifact:    artifact,
		})
	}

	if payload.Archived != nil {
		archived := *payload.Archived
		result.Evidence = append(result.Evidence, obs(enrichment.KindRepositoryArchived,
			fmt.Sprintf("repository %q archived=%t", fullName, archived),
			model.EvidenceInfo, archived))
	}
	if payload.PushedAt != "" {
		result.Evidence = append(result.Evidence, obs(enrichment.KindRepositoryPushedAt,
			fmt.Sprintf("repository %q last pushed at %s", fullName, payload.PushedAt),
			model.EvidenceInfo, payload.PushedAt))
	}
	if payload.DefaultBranch != "" {
		result.Evidence = append(result.Evidence, obs(enrichment.KindRepositoryDefaultBranch,
			fmt.Sprintf("repository %q default branch is %q", fullName, payload.DefaultBranch),
			model.EvidenceInfo, payload.DefaultBranch))
	}

	if spdx, ok := unambiguousSPDX(payload.License); ok {
		result.Evidence = append(result.Evidence, obs(enrichment.KindSourceLicense,
			fmt.Sprintf("GitHub reports licence %q for repository %q", spdx, fullName),
			model.EvidenceInfo, spdx))
	} else {
		result.Evidence = append(result.Evidence, obs(enrichment.KindSourceLicense,
			fmt.Sprintf("GitHub returned no unambiguous licence for repository %q", fullName),
			model.EvidenceUnknown, nil))
	}

	return result, nil
}

// repositoryOf recovers the parent repository from the specimen identity.
// Both identity shapes are code-owned Packet 5 formats, so this is a format
// match rather than a guess.
func repositoryOf(specimen model.Specimen) (string, bool) {
	if rest, found := strings.CutPrefix(specimen.ID, repositorySpecimenPrefix); found {
		return validFullName(rest)
	}
	if rest, found := strings.CutPrefix(specimen.ID, codeSpecimenPrefix); found {
		index := strings.Index(rest, "@")
		if index <= 0 {
			return "", false
		}
		return validFullName(rest[:index])
	}
	return "", false
}

// validFullName accepts exactly owner/repository with two non-empty,
// non-traversing segments so an identity can never alter the request target.
func validFullName(name string) (string, bool) {
	owner, repository, found := strings.Cut(name, "/")
	if !found || repository == "" || strings.Contains(repository, "/") {
		return "", false
	}
	for _, segment := range []string{owner, repository} {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return name, true
}

// unambiguousSPDX rejects missing, null, NOASSERTION, NONE and compound values,
// so Reusery never guesses an SPDX expression and never implies a legal
// conclusion.
func unambiguousSPDX(license *licenseRef) (string, bool) {
	if license == nil {
		return "", false
	}
	spdx := strings.TrimSpace(license.SPDXID)
	if spdx == "" || spdx == "NOASSERTION" || spdx == "NONE" {
		return "", false
	}
	if strings.ContainsAny(spdx, " \t,;") {
		return "", false
	}
	return spdx, true
}
