package github

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// RepositoryProvider discovers public GitHub repositories as reference-level
// candidates.
type RepositoryProvider struct {
	client *Client
}

// NewRepositoryProvider builds the repository provider over a shared client.
func NewRepositoryProvider(client *Client) *RepositoryProvider {
	return &RepositoryProvider{client: client}
}

// ID returns the stable provider identifier used in discovery profiles.
func (p *RepositoryProvider) ID() string { return discovery.ProviderGitHubRepositories }

type repositorySearchResponse struct {
	Items []repositoryItem `json:"items"`
	searchResult
}

type repositoryItem struct {
	FullName      string      `json:"full_name"`
	HTMLURL       string      `json:"html_url"`
	Description   string      `json:"description"`
	Language      string      `json:"language"`
	Archived      bool        `json:"archived"`
	PushedAt      string      `json:"pushed_at"`
	DefaultBranch string      `json:"default_branch"`
	License       *licenseRef `json:"license"`
}

type licenseRef struct {
	SPDXID string `json:"spdx_id"`
}

// Discover runs the profile's queries within the supplied budget.
//
// Stars, forks and other popularity signals are deliberately not decoded: they
// are not quality evidence, and Reusery does not rank.
func (p *RepositoryProvider) Discover(ctx context.Context, request discovery.ProviderRequest) (discovery.ProviderResult, error) {
	var result discovery.ProviderResult
	if request.Budget.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Budget.Timeout)
		defer cancel()
	}

	queries := uniqueQueries(request.Queries, request.Budget.MaxQueries)
	collected := discovery.NewCandidateSet()
	failedQueries := 0

	for _, query := range queries {
		if result.Requests >= request.Budget.MaxHTTPRequests {
			result.Incomplete = true
			result.Issues = append(result.Issues, budgetIssue(p.ID(), query.Text, request.Budget.MaxHTTPRequests))
			break
		}

		perPage := min(query.Limit, request.Budget.MaxResults)
		var response repositorySearchResponse
		result.Requests++
		path := repositorySearchPath + "?q=" + url.QueryEscape(query.Text) + "&per_page=" + strconv.Itoa(perPage)
		if _, err := p.client.get(ctx, request.Budget.MaxResponseBytes, path, &response); err != nil {
			result.Issues = append(result.Issues, issue(p.ID(), err, query.Text))
			failedQueries++
			continue
		}

		if response.IncompleteResults {
			result.Incomplete = true
			result.Issues = append(result.Issues, incompleteIssue(p.ID(), query.Text))
		}

		items := response.Items
		if len(items) > perPage {
			items = items[:perPage]
		}
		for _, item := range items {
			if item.FullName == "" || item.HTMLURL == "" {
				continue
			}
			if err := collected.Add(p.candidate(request.Primitive, item, query.Text, request.ObservedAt)); err != nil {
				return result, err
			}
		}
	}

	if failedQueries == len(queries) && failedQueries > 0 {
		return result, fmt.Errorf("%s: all %d queries failed", p.ID(), failedQueries)
	}
	result.Candidates = collected.List()
	return result, nil
}

// candidate normalises one repository hit into a reference-level specimen.
//
// Repository search supplies no immutable revision, so SourceRef.Revision
// stays empty and that absence is recorded as explicit UNKNOWN evidence rather
// than pretended away.
func (p *RepositoryProvider) candidate(
	primitive model.Primitive,
	item repositoryItem,
	queryText string,
	observedAt time.Time,
) discovery.Candidate {
	specimenID := "public/github/repository/" + item.FullName
	page := model.SourceRef{URL: item.HTMLURL, License: licenseOf(item.License)}

	obs := func(kind, claim string, result model.EvidenceResult, artifact string) model.Evidence {
		return discovery.NewObservation(discovery.ObservationSpec{
			ProviderID:  p.ID(),
			SubjectID:   specimenID,
			Kind:        kind,
			Claim:       claim,
			Result:      result,
			Source:      model.SourceRef{URL: item.HTMLURL},
			ObservedAt:  observedAt,
			Methodology: discovery.MethodologyGitHubRepos,
			Artifact:    artifact,
		})
	}

	evidence := []model.Evidence{
		obs("discovery_match",
			fmt.Sprintf("GitHub repository search matched repository %q", item.FullName),
			model.EvidenceInfo, "query="+queryText),
	}
	if item.Description != "" {
		evidence = append(evidence, obs("repository_description",
			fmt.Sprintf("repository %q description: %q", item.FullName, item.Description),
			model.EvidenceInfo, "query="+queryText))
	}
	if item.Language != "" {
		evidence = append(evidence, obs("repository_language",
			fmt.Sprintf("repository %q primary language is %q", item.FullName, item.Language),
			model.EvidenceInfo, "query="+queryText))
	}
	evidence = append(evidence, obs("repository_archived",
		fmt.Sprintf("repository %q archived=%t", item.FullName, item.Archived),
		model.EvidenceInfo, "query="+queryText))
	if item.PushedAt != "" {
		evidence = append(evidence, obs("repository_last_push",
			fmt.Sprintf("repository %q last pushed at %s", item.FullName, item.PushedAt),
			model.EvidenceInfo, "query="+queryText))
	}
	if item.DefaultBranch != "" {
		evidence = append(evidence, obs("repository_default_branch",
			fmt.Sprintf("repository %q default branch is %q", item.FullName, item.DefaultBranch),
			model.EvidenceInfo, "query="+queryText))
	}

	if spdx, ok := unambiguousSPDX(item.License); ok {
		evidence = append(evidence, obs("source_license",
			fmt.Sprintf("GitHub reports licence %q for repository %q", spdx, item.FullName),
			model.EvidenceInfo, "query="+queryText))
	} else {
		evidence = append(evidence, obs("source_license",
			fmt.Sprintf("GitHub returned no unambiguous licence for repository %q", item.FullName),
			model.EvidenceUnknown, "query="+queryText))
	}
	evidence = append(evidence, obs("source_revision",
		fmt.Sprintf("repository search returned no immutable revision for %q", item.FullName),
		model.EvidenceUnknown, "query="+queryText))

	return discovery.Candidate{
		ProviderID: p.ID(),
		Specimen: model.Specimen{
			ID:          specimenID,
			PrimitiveID: primitive.ID,
			Name:        item.FullName,
			Source:      page,
			ReuseMode:   []model.ReuseMode{model.ReuseReference},
		},
		Evidence: evidence,
	}
}

// licenseOf returns a licence only for one clear SPDX identifier.
func licenseOf(license *licenseRef) string {
	spdx, ok := unambiguousSPDX(license)
	if !ok {
		return ""
	}
	return spdx
}

// unambiguousSPDX rejects missing, null, NOASSERTION, NONE and compound values
// so Reusery never guesses an SPDX expression.
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
