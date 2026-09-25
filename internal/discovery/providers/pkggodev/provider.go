// Package pkggodev discovers plausible Go packages through the supported
// pkg.go.dev JSON API.
//
// Everything it returns is attributable INFO or UNKNOWN observation about a
// package. A search hit is a lead: it never establishes that a package
// satisfies a behavioural contract requirement.
package pkggodev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

const (
	// DefaultBaseURL is the code-owned production endpoint. It is never taken
	// from user configuration.
	DefaultBaseURL = "https://pkg.go.dev"

	// searchEndpoint and packageEndpoint are the only pkg.go.dev APIs this
	// provider uses. HTML is never scraped.
	searchEndpoint    = "/v1/search"
	packageEndpoint   = "/v1/package/"
	cataloguePageBase = "https://pkg.go.dev/"
)

// Provider discovers packages on pkg.go.dev.
type Provider struct {
	baseURL string
}

// New builds the production provider.
func New() *Provider { return NewWithBaseURL(DefaultBaseURL) }

// NewWithBaseURL builds a provider against an explicit base URL. Tests inject
// an httptest server here; production never calls it.
func NewWithBaseURL(baseURL string) *Provider {
	return &Provider{baseURL: strings.TrimRight(baseURL, "/")}
}

// ID returns the stable provider identifier used in discovery profiles.
func (p *Provider) ID() string { return discovery.ProviderPkgGoDev }

type searchItem struct {
	PackagePath string `json:"packagePath"`
	ModulePath  string `json:"modulePath"`
	Version     string `json:"version"`
	Synopsis    string `json:"synopsis"`
}

type searchResponse struct {
	Items []searchItem `json:"items"`
	Total int          `json:"total"`
}

// license is decoded permissively: pkg.go.dev currently returns no licence
// field, and Reusery must survive the API adding one with any shape rather
// than failing the whole discovery run.
type packageResponse struct {
	ModulePath        string          `json:"modulePath"`
	Version           string          `json:"version"`
	Path              string          `json:"path"`
	Name              string          `json:"name"`
	Synopsis          string          `json:"synopsis"`
	IsStandardLibrary bool            `json:"isStandardLibrary"`
	IsRedistributable bool            `json:"isRedistributable"`
	License           json.RawMessage `json:"license"`
}

// hit is one unique package discovered by one or more queries.
type hit struct {
	item       searchItem
	observedAt time.Time
	matches    []model.Evidence
}

// Discover runs the profile's queries within the supplied budget.
//
// It returns a non-nil error only when every query failed. Partial failure is
// reported through issues with Incomplete set, so a failing query never
// discards the packages the other queries found.
func (p *Provider) Discover(ctx context.Context, request discovery.ProviderRequest) (discovery.ProviderResult, error) {
	var result discovery.ProviderResult

	api := httpx.New(p.baseURL, discovery.UserAgent, request.Budget.MaxResponseBytes)
	if request.Budget.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Budget.Timeout)
		defer cancel()
	}

	queries := uniqueQueries(request.Queries, request.Budget.MaxQueries)

	seen := make(map[string]*hit, len(queries)*2)
	order := make([]*hit, 0, len(queries)*2)
	failedQueries := 0

	for _, query := range queries {
		if result.Requests >= request.Budget.MaxHTTPRequests {
			result.Incomplete = true
			result.Issues = append(result.Issues, discovery.ProviderIssue{
				Kind:     discovery.IssueBudgetExhausted,
				Provider: p.ID(),
				Query:    query.Text,
				Message:  fmt.Sprintf("request budget of %d reached; query %q was not sent", request.Budget.MaxHTTPRequests, query.Text),
			})
			break
		}

		limit := query.Limit
		if limit > request.Budget.MaxResults {
			limit = request.Budget.MaxResults
		}

		var response searchResponse
		result.Requests++
		endpoint := searchEndpoint + "?q=" + url.QueryEscape(query.Text) + "&limit=" + strconv.Itoa(limit)
		if _, err := api.GetJSON(ctx, endpoint, nil, &response); err != nil {
			result.Issues = append(result.Issues, issueFor(err, query.Text))
			failedQueries++
			continue
		}

		items := response.Items
		if len(items) > limit {
			items = items[:limit]
		}
		for _, item := range items {
			if item.PackagePath == "" || item.Version == "" {
				continue
			}
			key := item.PackagePath + "@" + item.Version
			current, ok := seen[key]
			if !ok {
				current = &hit{item: item, observedAt: request.ObservedAt}
				seen[key] = current
				order = append(order, current)
			}
			current.matches = append(current.matches, discovery.NewObservation(discovery.ObservationSpec{
				ProviderID:  p.ID(),
				SubjectID:   specimenID(item.PackagePath, item.Version),
				Kind:        "discovery_match",
				Claim:       fmt.Sprintf("pkg.go.dev matched package %q", item.PackagePath),
				Result:      model.EvidenceInfo,
				Source:      evidenceSource(item),
				ObservedAt:  request.ObservedAt,
				Methodology: discovery.MethodologyPkgGoDev,
				Artifact:    "query=" + query.Text,
			}))
		}
	}

	if failedQueries == len(queries) && failedQueries > 0 {
		return result, fmt.Errorf("pkg.go.dev: all %d queries failed", failedQueries)
	}
	if len(queries) == 0 {
		return result, nil
	}

	budgetExhausted := false
	for _, current := range order {
		if result.Requests >= request.Budget.MaxHTTPRequests {
			if !budgetExhausted {
				budgetExhausted = true
				result.Incomplete = true
				result.Issues = append(result.Issues, discovery.ProviderIssue{
					Kind:     discovery.IssueBudgetExhausted,
					Provider: p.ID(),
					Message: fmt.Sprintf("request budget of %d reached before package metadata could be read; candidates are search-derived only",
						request.Budget.MaxHTTPRequests),
				})
			}
			result.Candidates = append(result.Candidates, p.candidate(request.Primitive, current, nil, false))
			continue
		}

		var metadata packageResponse
		result.Requests++
		_, err := api.GetJSON(ctx, packageEndpoint+escapeSegments(current.item.PackagePath), nil, &metadata)
		if err != nil {
			result.Issues = append(result.Issues, issueFor(err, ""))
			result.Candidates = append(result.Candidates, p.candidate(request.Primitive, current, nil, false))
			continue
		}
		result.Candidates = append(result.Candidates, p.candidate(request.Primitive, current, &metadata, true))
	}

	return result, nil
}

// candidate normalises one discovered package into a model.Specimen plus its
// attributable observations.
//
// The specimen's version always comes from search: switching to whatever the
// package endpoint reports latest would silently change the identity the
// search result actually matched.
func (p *Provider) candidate(primitive model.Primitive, current *hit, metadata *packageResponse, haveMetadata bool) discovery.Candidate {
	item := current.item
	specimen := model.Specimen{
		ID:          specimenID(item.PackagePath, item.Version),
		PrimitiveID: primitive.ID,
		Name:        item.PackagePath,
		Source:      model.SourceRef{URL: cataloguePageBase + item.PackagePath, Revision: item.Version, Path: item.PackagePath},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}

	evidence := make([]model.Evidence, 0, len(current.matches)+6)
	evidence = append(evidence, current.matches...)
	if synopsis := item.Synopsis; synopsis != "" {
		evidence = append(evidence, p.observation(current, "package_synopsis",
			fmt.Sprintf("pkg.go.dev describes package %q as %q", item.PackagePath, synopsis),
			model.EvidenceInfo, "query="+firstQuery(current.matches)))
	}
	if item.ModulePath != "" {
		evidence = append(evidence, p.observation(current, "package_module",
			fmt.Sprintf("package %q belongs to module %q", item.PackagePath, item.ModulePath),
			model.EvidenceInfo, "query="+firstQuery(current.matches)))
	}
	evidence = append(evidence, p.observation(current, "package_version",
		fmt.Sprintf("search returned version %q for package %q", item.Version, item.PackagePath),
		model.EvidenceInfo, "query="+firstQuery(current.matches)))

	if haveMetadata {
		evidence = append(evidence,
			p.observation(current, "package_standard_library",
				fmt.Sprintf("pkg.go.dev reports isStandardLibrary=%t for package %q", metadata.IsStandardLibrary, item.PackagePath),
				model.EvidenceInfo, "endpoint="+packageEndpoint+escapeSegments(item.PackagePath)),
			p.observation(current, "package_redistributable",
				fmt.Sprintf("pkg.go.dev reports isRedistributable=%t for package %q", metadata.IsRedistributable, item.PackagePath),
				model.EvidenceInfo, "endpoint="+packageEndpoint+escapeSegments(item.PackagePath)),
		)
		if license, ok := unambiguousLicense(metadata.License); ok {
			specimen.Source.License = license
			evidence = append(evidence, p.observation(current, "source_license",
				fmt.Sprintf("pkg.go.dev reports licence %q for package %q", license, item.PackagePath),
				model.EvidenceInfo, "endpoint="+packageEndpoint+escapeSegments(item.PackagePath)))
		} else {
			evidence = append(evidence, p.observation(current, "source_license",
				fmt.Sprintf("pkg.go.dev returned no unambiguous licence for package %q", item.PackagePath),
				model.EvidenceUnknown, "endpoint="+packageEndpoint+escapeSegments(item.PackagePath)))
		}
	} else {
		evidence = append(evidence, p.observation(current, "source_license",
			fmt.Sprintf("pkg.go.dev returned no unambiguous licence for package %q", item.PackagePath),
			model.EvidenceUnknown, "endpoint="+packageEndpoint+escapeSegments(item.PackagePath)))
	}

	return discovery.Candidate{ProviderID: p.ID(), Specimen: specimen, Evidence: evidence}
}

func (p *Provider) observation(current *hit, kind, claim string, result model.EvidenceResult, artifact string) model.Evidence {
	return discovery.NewObservation(discovery.ObservationSpec{
		ProviderID:  p.ID(),
		SubjectID:   specimenID(current.item.PackagePath, current.item.Version),
		Kind:        kind,
		Claim:       claim,
		Result:      result,
		Source:      evidenceSource(current.item),
		ObservedAt:  current.observedAt,
		Methodology: discovery.MethodologyPkgGoDev,
		Artifact:    artifact,
	})
}

func evidenceSource(item searchItem) model.SourceRef {
	return model.SourceRef{
		URL:      cataloguePageBase + item.PackagePath,
		Revision: item.Version,
		Path:     item.PackagePath,
	}
}

// specimenID is the stable public identity of one package at one version.
func specimenID(packagePath, version string) string {
	return "public/pkg.go.dev/" + url.PathEscape(packagePath) + "@" + version
}

func firstQuery(matches []model.Evidence) string {
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimPrefix(matches[0].Artifact, "query=")
}

// uniqueQueries removes repeated query text and applies the per-provider query
// budget, preserving authored order.
func uniqueQueries(queries []discovery.Query, max int) []discovery.Query {
	seen := make(map[string]struct{}, len(queries))
	unique := make([]discovery.Query, 0, len(queries))
	for _, query := range queries {
		text := strings.TrimSpace(query.Text)
		if text == "" {
			continue
		}
		if _, duplicate := seen[text]; duplicate {
			continue
		}
		seen[text] = struct{}{}
		unique = append(unique, discovery.Query{Text: text, Limit: query.Limit})
		if len(unique) >= max {
			break
		}
	}
	return unique
}

// escapeSegments escapes each path segment while preserving the separators, so
// a package path cannot alter the request target.
func escapeSegments(packagePath string) string {
	segments := strings.Split(packagePath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// unambiguousLicense returns a licence only when the provider supplied one
// clear value. Missing, null, empty, compound or non-string values produce no
// licence at all: Reusery never invents an SPDX expression.
func unambiguousLicense(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == `""` {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t,;") {
		return "", false
	}
	return value, true
}

// issueFor classifies one transport or response problem as an inspectable
// provider issue. It never includes credentials or request headers.
func issueFor(err error, query string) discovery.ProviderIssue {
	return discovery.IssueFromError(discovery.ProviderPkgGoDev, err, query)
}
