// Package depsdev enriches Go package specimens with attributable metadata
// from the stable deps.dev v3 JSON API.
//
// Everything it returns is attributable INFO or UNKNOWN observation. A licence
// expression, an advisory identifier or a dependency count says nothing about
// whether a package satisfies a behavioural contract requirement: the Packet 2
// evaluator stays the only authority over requirement satisfaction.
package depsdev

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

const (
	// DefaultBaseURL is the code-owned production endpoint. It is never taken
	// from user configuration. v3 is used throughout: v3alpha is not.
	DefaultBaseURL = "https://api.deps.dev"

	versionEndpoint       = "/v3/systems/GO/packages/"
	requirementsSuffix    = ":requirements"
	packageSpecimenPrefix = "public/pkg.go.dev/"

	// moduleClaimPrefix and moduleClaimSeparator match the exact claim shape
	// Packet 5's pkg.go.dev provider writes. Module identity is recovered from
	// that deterministic, code-owned format — never guessed from a package
	// path, and never by parsing free English.
	moduleClaimPrefix    = "package \""
	moduleClaimSeparator = "\" belongs to module \""
)

// Provider enriches pkg.go.dev specimens through deps.dev.
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

// ID returns the stable provider identifier.
func (p *Provider) ID() string { return enrichment.ProviderDepsDev }

// Supports reports whether this provider can enrich one specimen. Module
// identity itself is resolved later from stored discovery evidence, so a
// specimen whose module cannot be established produces an explicit issue and
// no manufactured evidence.
func (p *Provider) Supports(specimen model.Specimen) bool {
	return strings.HasPrefix(specimen.ID, packageSpecimenPrefix)
}

type versionKey struct {
	System  string `json:"system"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type advisoryKey struct {
	ID string `json:"id"`
}

type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// versionResponse is the deps.dev GetVersion payload. Optional fields are
// pointers so a field the provider did not return is never persisted as though
// it had returned a default value.
type versionResponse struct {
	VersionKey       versionKey    `json:"versionKey"`
	PublishedAt      string        `json:"publishedAt"`
	IsDeprecated     *bool         `json:"isDeprecated"`
	DeprecatedReason string        `json:"deprecatedReason"`
	Licenses         []string      `json:"licenses"`
	AdvisoryKeys     []advisoryKey `json:"advisoryKeys"`
	Links            []link        `json:"links"`
}

type dependencyRef struct {
	Name        string `json:"name"`
	Requirement string `json:"requirement"`
}

type requirementsResponse struct {
	Go struct {
		DirectDependencies   []dependencyRef `json:"directDependencies"`
		IndirectDependencies []dependencyRef `json:"indirectDependencies"`
	} `json:"go"`
}

// Enrich reads GetVersion and then GetRequirements for one specimen.
//
// GetVersion failing is a provider failure for this specimen. GetRequirements
// failing is reported as an issue so the version, licence, advisory and
// deprecation evidence already gathered is not thrown away.
func (p *Provider) Enrich(ctx context.Context, request enrichment.Request) (enrichment.ProviderResult, error) {
	var result enrichment.ProviderResult

	module, ok := p.moduleIdentity(request)
	if !ok {
		result.Issues = append(result.Issues, enrichment.ProviderIssue{
			Kind:       enrichment.IssueIdentity,
			Provider:   p.ID(),
			SpecimenID: request.Specimen.ID,
			Message:    "stored discovery evidence did not establish a Go module identity; deps.dev enrichment was skipped rather than guessing a module path",
		})
		return result, nil
	}
	version := strings.TrimSpace(request.Specimen.Source.Revision)
	if version == "" {
		result.Issues = append(result.Issues, enrichment.ProviderIssue{
			Kind:       enrichment.IssueIdentity,
			Provider:   p.ID(),
			SpecimenID: request.Specimen.ID,
			Message:    "the specimen carries no exact version; deps.dev enrichment was skipped rather than resolving an implicit version",
		})
		return result, nil
	}
	if request.Budget.MaxHTTPRequests < 2 {
		result.Issues = append(result.Issues, enrichment.ProviderIssue{
			Kind:       enrichment.IssueBudgetExhausted,
			Provider:   p.ID(),
			SpecimenID: request.Specimen.ID,
			Message: fmt.Sprintf("request budget of %d cannot cover the version and requirements reads; nothing was sent",
				request.Budget.MaxHTTPRequests),
		})
		return result, nil
	}

	api := httpx.New(p.baseURL, enrichment.UserAgent, request.Budget.MaxResponseBytes)
	escModule := url.PathEscape(module)
	escVersion := url.PathEscape(version)
	versionPath := versionEndpoint + escModule + "/versions/" + escVersion

	var versionData versionResponse
	result.Requests++
	if _, err := api.GetJSON(ctx, versionPath, nil, &versionData); err != nil {
		return result, err
	}

	source := model.SourceRef{
		URL:      p.baseURL + versionPath,
		Revision: version,
		Path:     module,
	}
	obs := func(kind, claim string, result2 model.EvidenceResult, value any) model.Evidence {
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
			Result:      result2,
			Source:      source,
			ObservedAt:  request.ObservedAt,
			Methodology: enrichment.MethodologyDepsDev,
			Artifact:    artifact,
		})
	}

	reportedVersion := versionData.VersionKey.Version
	if reportedVersion == "" {
		reportedVersion = version
	}
	result.Evidence = append(result.Evidence, obs(enrichment.KindPackageVersion,
		fmt.Sprintf("deps.dev reported version %q for Go module %q", reportedVersion, module),
		model.EvidenceInfo, reportedVersion))

	if versionData.PublishedAt != "" {
		result.Evidence = append(result.Evidence, obs(enrichment.KindPackagePublishedAt,
			fmt.Sprintf("deps.dev reported publishedAt %q for Go module %q version %q", versionData.PublishedAt, module, version),
			model.EvidenceInfo, versionData.PublishedAt))
	}
	if versionData.IsDeprecated != nil {
		deprecated := *versionData.IsDeprecated
		result.Evidence = append(result.Evidence, obs(enrichment.KindPackageDeprecated,
			fmt.Sprintf("deps.dev reported isDeprecated=%t for Go module %q version %q", deprecated, module, version),
			model.EvidenceInfo, deprecated))
	}
	if versionData.DeprecatedReason != "" {
		result.Evidence = append(result.Evidence, obs(enrichment.KindPackageDeprecatedReason,
			fmt.Sprintf("deps.dev reported deprecatedReason %q for Go module %q version %q", versionData.DeprecatedReason, module, version),
			model.EvidenceInfo, versionData.DeprecatedReason))
	}

	result.Evidence = append(result.Evidence, p.licenceObservations(obs, module, version, versionData.Licenses)...)
	result.Evidence = append(result.Evidence, p.advisoryObservations(obs, module, version, versionData.AdvisoryKeys)...)
	if sourceLink, ok := repositoryLink(versionData.Links); ok {
		result.Evidence = append(result.Evidence, obs(enrichment.KindSourceRepositoryLink,
			fmt.Sprintf("deps.dev returned source repository link %q for Go module %q", sourceLink, module),
			model.EvidenceInfo, sourceLink))
	}

	// The requirements read is secondary: if it fails, everything above
	// survives as evidence and the failure is reported as an issue.
	requirementsPath := versionPath + requirementsSuffix
	var requirementsData requirementsResponse
	result.Requests++
	if _, err := api.GetJSON(ctx, requirementsPath, nil, &requirementsData); err != nil {
		result.Issues = append(result.Issues, enrichment.IssueFromError(p.ID(), request.Specimen.ID, err))
		return result, nil
	}

	direct := requirementsData.Go.DirectDependencies
	indirect := requirementsData.Go.IndirectDependencies
	result.Evidence = append(result.Evidence, dependencyObservations(obs, module, version,
		enrichment.KindDirectDependency, enrichment.KindDirectDependencyCount, direct)...)
	result.Evidence = append(result.Evidence, dependencyObservations(obs, module, version,
		enrichment.KindIndirectDependency, enrichment.KindIndirectDependencyCount, indirect)...)

	return result, nil
}

// licenceObservations implements the licence rules: zero expressions leave the
// source licence unresolved, one expression is recorded exactly, and several
// are each recorded exactly together with an explicit UNKNOWN relationship
// observation. Expressions are never combined, rewritten or interpreted.
func (p *Provider) licenceObservations(
	obs func(kind, claim string, result model.EvidenceResult, value any) model.Evidence,
	module, version string,
	expressions []string,
) []model.Evidence {
	if len(expressions) == 0 {
		return []model.Evidence{obs(enrichment.KindSourceLicense,
			fmt.Sprintf("deps.dev returned no licence expression for Go module %q version %q", module, version),
			model.EvidenceUnknown, nil)}
	}

	observations := make([]model.Evidence, 0, len(expressions)+1)
	seen := make(map[string]struct{}, len(expressions))
	unique := make([]string, 0, len(expressions))
	for _, expression := range expressions {
		expression = strings.TrimSpace(expression)
		if expression == "" {
			continue
		}
		if _, duplicate := seen[expression]; duplicate {
			continue
		}
		seen[expression] = struct{}{}
		unique = append(unique, expression)
	}
	for _, expression := range unique {
		observations = append(observations, obs(enrichment.KindSourceLicense,
			fmt.Sprintf("deps.dev returned licence expression %q for Go module %q version %q", expression, module, version),
			model.EvidenceInfo, expression))
	}
	if len(unique) == 0 {
		return []model.Evidence{obs(enrichment.KindSourceLicense,
			fmt.Sprintf("deps.dev returned no licence expression for Go module %q version %q", module, version),
			model.EvidenceUnknown, nil)}
	}
	if len(unique) > 1 {
		observations = append(observations, obs(enrichment.KindLicenceRelationship,
			fmt.Sprintf("deps.dev returned %d licence expressions for Go module %q version %q and does not establish how they relate",
				len(unique), module, version),
			model.EvidenceUnknown, nil))
	}
	return observations
}

// advisoryObservations records one observation per advisory identifier plus a
// count observation that always exists, including when the count is zero.
//
// A zero count is worded as an absence of reported identifiers at the
// observation time. It is never "secure", never "safe", never
// "vulnerability-free" and never a pass: a zero advisory count is not
// EvidencePass.
func (p *Provider) advisoryObservations(
	obs func(kind, claim string, result model.EvidenceResult, value any) model.Evidence,
	module, version string,
	advisories []advisoryKey,
) []model.Evidence {
	observations := make([]model.Evidence, 0, len(advisories)+1)
	seen := make(map[string]struct{}, len(advisories))
	count := 0
	for _, advisory := range advisories {
		id := strings.TrimSpace(advisory.ID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		count++
		observations = append(observations, obs(enrichment.KindKnownAdvisory,
			fmt.Sprintf("deps.dev reported advisory identifier %q for Go module %q version %q", id, module, version),
			model.EvidenceInfo, id))
	}

	claim := fmt.Sprintf("deps.dev reported %d known direct advisory identifiers for this version at the observation time", count)
	if count == 0 {
		claim = "deps.dev reported zero known direct advisory identifiers for this version at the observation time"
	}
	observations = append(observations, obs(enrichment.KindKnownAdvisoryCount,
		claim, model.EvidenceInfo, count))
	return observations
}

func dependencyObservations(
	obs func(kind, claim string, result model.EvidenceResult, value any) model.Evidence,
	module, version, itemKind, countKind string,
	dependencies []dependencyRef,
) []model.Evidence {
	observations := make([]model.Evidence, 0, len(dependencies)+1)
	seen := make(map[string]struct{}, len(dependencies))
	kept := 0
	for _, dependency := range dependencies {
		name := strings.TrimSpace(dependency.Name)
		if name == "" {
			continue
		}
		value := name
		if requirement := strings.TrimSpace(dependency.Requirement); requirement != "" {
			value = name + "@" + requirement
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		kept++
		observations = append(observations, obs(itemKind,
			fmt.Sprintf("deps.dev reported declared dependency %q for Go module %q version %q", value, module, version),
			model.EvidenceInfo, value))
	}
	observations = append(observations, obs(countKind,
		fmt.Sprintf("deps.dev reported %d declared dependencies of this kind for Go module %q version %q", kept, module, version),
		model.EvidenceInfo, kept))
	return observations
}

// repositoryLink returns the first link with an absolute http(s) URL, exactly
// as the provider returned it.
func repositoryLink(links []link) (string, bool) {
	for _, entry := range links {
		raw := strings.TrimSpace(entry.URL)
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		return raw, true
	}
	return "", false
}

// moduleIdentity recovers the Go module from Packet 5's package_module
// observation. The claim shape is code-owned and deterministic, so this is a
// format match rather than English parsing; anything that does not match is
// skipped instead of guessed.
func (p *Provider) moduleIdentity(request enrichment.Request) (string, bool) {
	for _, item := range request.ExistingEvidence {
		if item.Kind != "package_module" || item.Result != model.EvidenceInfo {
			continue
		}
		if !strings.HasPrefix(item.Claim, moduleClaimPrefix) {
			continue
		}
		rest := strings.TrimPrefix(item.Claim, moduleClaimPrefix)
		index := strings.Index(rest, moduleClaimSeparator)
		if index < 0 {
			continue
		}
		rawPackage := rest[:index]
		tail := rest[index+len(moduleClaimSeparator):]
		if !strings.HasSuffix(tail, "\"") {
			continue
		}
		rawModule := strings.TrimSuffix(tail, "\"")

		packagePath, err := strconv.Unquote(`"` + rawPackage + `"`)
		if err != nil {
			continue
		}
		module, err := strconv.Unquote(`"` + rawModule + `"`)
		if err != nil || module == "" {
			continue
		}
		if declared := request.Specimen.Source.Path; declared != "" && declared != packagePath {
			continue
		}
		return module, true
	}
	return "", false
}
