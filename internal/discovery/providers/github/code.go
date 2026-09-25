package github

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// CodeProvider discovers public GitHub source files as reference-level
// candidates.
//
// A hit is a pointer to attributable source, not a demonstration: finding
// exec.CommandContext does not establish supports-cancellation, finding
// StdoutPipe does not establish drains-stdout-stderr-concurrently, and finding
// SysProcAttr.Setpgid does not establish process-tree termination.
type CodeProvider struct {
	client *Client
}

// NewCodeProvider builds the code provider over a shared client.
func NewCodeProvider(client *Client) *CodeProvider {
	return &CodeProvider{client: client}
}

// ID returns the stable provider identifier used in discovery profiles.
func (p *CodeProvider) ID() string { return discovery.ProviderGitHubCode }

type codeSearchResponse struct {
	Items []codeItem `json:"items"`
	searchResult
}

type codeItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	SHA        string `json:"sha"`
	HTMLURL    string `json:"html_url"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

// Discover runs the profile's queries within the supplied budget.
//
// Source files are never downloaded: the file URL, blob SHA and repository
// path are sufficient discovery provenance for Packet 5.
func (p *CodeProvider) Discover(ctx context.Context, request discovery.ProviderRequest) (discovery.ProviderResult, error) {
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
		var response codeSearchResponse
		result.Requests++
		path := codeSearchPath + "?q=" + url.QueryEscape(query.Text) + "&per_page=" + strconv.Itoa(perPage)
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
			if item.Path == "" || item.SHA == "" || item.HTMLURL == "" || item.Repository.FullName == "" {
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

// candidate normalises one code hit into a reference-level specimen pinned to
// the blob GitHub actually returned.
func (p *CodeProvider) candidate(
	primitive model.Primitive,
	item codeItem,
	queryText string,
	observedAt time.Time,
) discovery.Candidate {
	fullName := item.Repository.FullName
	specimenID := "public/github/code/" + fullName + "@" + item.SHA + ":" + url.PathEscape(item.Path)

	obs := func(kind, claim string, result model.EvidenceResult, artifact string) model.Evidence {
		return discovery.NewObservation(discovery.ObservationSpec{
			ProviderID:  p.ID(),
			SubjectID:   specimenID,
			Kind:        kind,
			Claim:       claim,
			Result:      result,
			Source:      model.SourceRef{URL: item.HTMLURL, Revision: item.SHA, Path: item.Path},
			ObservedAt:  observedAt,
			Methodology: discovery.MethodologyGitHubCode,
			Artifact:    artifact,
		})
	}

	return discovery.Candidate{
		ProviderID: p.ID(),
		Specimen: model.Specimen{
			ID:          specimenID,
			PrimitiveID: primitive.ID,
			Name:        fullName + ":" + item.Path,
			Source: model.SourceRef{
				URL:      item.HTMLURL,
				Revision: item.SHA,
				Path:     item.Path,
			},
			ReuseMode: []model.ReuseMode{model.ReuseReference},
		},
		Evidence: []model.Evidence{
			obs("discovery_match",
				fmt.Sprintf("GitHub code search matched file %q in repository %q", item.Path, fullName),
				model.EvidenceInfo, "query="+queryText),
			obs("source_revision",
				fmt.Sprintf("GitHub code search returned blob %s for %s:%s", item.SHA, fullName, item.Path),
				model.EvidenceInfo, "query="+queryText),
			obs("source_license",
				fmt.Sprintf("GitHub code search returned no licence information for %s:%s", fullName, item.Path),
				model.EvidenceUnknown, "query="+queryText),
		},
	}
}
