// Package discovery finds plausible public candidates for a primitive by
// querying bounded public provider APIs.
//
// Discovery produces leads, not proof. Provider output is attributable
// INFO or UNKNOWN evidence attached to normal model.Specimen values; it is
// never behavioural PASS or FAIL, and it never carries AppliesTo. Provider
// search order is retrieval mechanics, not Reusery ranking: nothing in this
// package feeds provider ordering into the resolver kernel, and nothing here
// decides that a candidate satisfies a contract.
package discovery

import (
	"context"
	"net/http"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Provider IDs Reusery can construct in this packet. A profile naming
// anything else is rejected before any provider is built.
const (
	ProviderPkgGoDev           = "pkg.go.dev"
	ProviderGitHubRepositories = "github-repositories"
	ProviderGitHubCode         = "github-code"
)

// KnownProviderIDs returns the provider IDs supported by this packet. A fresh
// slice is returned every call so callers cannot mutate the canonical list.
func KnownProviderIDs() []string {
	return []string{ProviderPkgGoDev, ProviderGitHubRepositories, ProviderGitHubCode}
}

// UserAgent identifies Reusery to every public provider.
const UserAgent = "reusery-discovery (+https://reusery.dev)"

// Budget bounds one discovery run. These are deliberately conservative fixed
// defaults rather than a configuration surface: no provider may exceed them.
type Budget struct {
	// MaxProviders is the most providers a profile may declare.
	MaxProviders int
	// MaxQueries is the most queries one provider plan may declare.
	MaxQueries int
	// MaxResults caps results accepted for a single query.
	MaxResults int
	// MaxHTTPRequests caps HTTP requests one provider may issue.
	MaxHTTPRequests int
	// MaxCandidates caps unique candidates returned for the whole run.
	MaxCandidates int
	// Timeout is the wall-clock budget for one provider.
	Timeout time.Duration
	// MaxResponseBytes caps one HTTP response body.
	MaxResponseBytes int64
}

// DefaultBudget returns the bounded defaults used by this packet.
func DefaultBudget() Budget {
	return Budget{
		MaxProviders:     3,
		MaxQueries:       3,
		MaxResults:       6,
		MaxHTTPRequests:  20,
		MaxCandidates:    24,
		Timeout:          15 * time.Second,
		MaxResponseBytes: 2 << 20, // 2 MiB
	}
}

// Query is one provider search request described in a discovery profile.
type Query struct {
	Text  string `yaml:"text" json:"text"`
	Limit int    `yaml:"limit" json:"limit"`
}

// ProviderPlan groups the queries one provider runs for a profile.
type ProviderPlan struct {
	ID      string  `yaml:"id" json:"id"`
	Queries []Query `yaml:"queries" json:"queries"`
}

// Profile is a structured discovery plan for one primitive and contract.
type Profile struct {
	SchemaVersion int            `yaml:"schema_version" json:"schema_version"`
	PrimitiveID   string         `yaml:"primitive_id" json:"primitive_id"`
	ContractID    string         `yaml:"contract_id" json:"contract_id"`
	Providers     []ProviderPlan `yaml:"providers" json:"providers"`
}

// Candidate is one plausible public specimen together with the provider
// observations that make it attributable.
type Candidate struct {
	ProviderID string           `json:"provider_id"`
	Specimen   model.Specimen   `json:"specimen"`
	Evidence   []model.Evidence `json:"evidence"`
}

// IssueKind classifies an operational provider failure. Operational failure
// is never candidate evidence: it says nothing about any candidate.
type IssueKind string

const (
	IssueRateLimited       IssueKind = "rate_limited"
	IssueAuthentication    IssueKind = "authentication"
	IssueForbidden         IssueKind = "forbidden"
	IssueTimeout           IssueKind = "timeout"
	IssueUnavailable       IssueKind = "unavailable"
	IssueInvalidQuery      IssueKind = "invalid_query"
	IssueInvalidResponse   IssueKind = "invalid_response"
	IssueBudgetExhausted   IssueKind = "budget_exhausted"
	IssueIncompleteResults IssueKind = "incomplete_results"
)

// ProviderIssue is an inspectable operational problem. It carries only safe
// information: never tokens, authorization headers, credentials or request
// dumps.
type ProviderIssue struct {
	Kind       IssueKind `json:"kind"`
	Provider   string    `json:"provider"`
	Query      string    `json:"query,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	RetryAfter string    `json:"retry_after,omitempty"`
	Message    string    `json:"message"`
}

// ProviderResult is one provider's bounded output for a discovery run.
type ProviderResult struct {
	Candidates []Candidate
	Requests   int
	Incomplete bool
	Issues     []ProviderIssue
}

// ProviderReport is the service-level summary of one provider's run.
type ProviderReport struct {
	ID             string          `json:"id"`
	Succeeded      bool            `json:"succeeded"`
	Requests       int             `json:"requests"`
	CandidateCount int             `json:"candidate_count"`
	Incomplete     bool            `json:"incomplete"`
	Issues         []ProviderIssue `json:"issues"`
}

// Result is the canonical discovery output: what was found, and what each
// provider did. It contains no score, confidence, winner or quality order.
type Result struct {
	PrimitiveID string           `json:"primitive_id"`
	ContractID  string           `json:"contract_id"`
	ObservedAt  time.Time        `json:"observed_at"`
	Candidates  []Candidate      `json:"candidates"`
	Providers   []ProviderReport `json:"providers"`
}

// ProviderRequest carries only domain/discovery information. No PostgreSQL
// types, no CLI types and no HTTP request objects reach a provider.
type ProviderRequest struct {
	Primitive  model.Primitive
	Contract   model.Contract
	Queries    []Query
	Budget     Budget
	ObservedAt time.Time
}

// Provider is the replaceable discovery boundary. Concrete provider packages
// implement it; the discovery service depends only on this interface.
//
// A provider returns a non-nil error only when it produced no usable result
// at all (authentication failure, total timeout, every query failed). Partial
// failure is reported through ProviderResult.Issues with Incomplete set and a
// nil error, so one provider failing does not destroy its own successful
// queries or another provider's results.
type Provider interface {
	ID() string
	Discover(context.Context, ProviderRequest) (ProviderResult, error)
}

// Clock supplies the single observation timestamp for one discovery run.
type Clock func() time.Time

// ClassifyStatus maps an HTTP status onto an inspectable issue kind. A 403
// that reports exhausted rate-limit budget is classified as rate limiting
// rather than a permissions problem, matching how the providers actually
// fail.
func ClassifyStatus(status int, rateLimitRemaining string) IssueKind {
	switch status {
	case http.StatusTooManyRequests:
		return IssueRateLimited
	case http.StatusUnauthorized:
		return IssueAuthentication
	case http.StatusForbidden:
		if rateLimitRemaining == "0" {
			return IssueRateLimited
		}
		return IssueForbidden
	case http.StatusBadRequest, 422:
		return IssueInvalidQuery
	case http.StatusNotFound, http.StatusNotAcceptable, http.StatusUnsupportedMediaType:
		return IssueInvalidResponse
	}
	if status >= 500 {
		return IssueUnavailable
	}
	if status >= 400 {
		return IssueUnavailable
	}
	return IssueUnavailable
}
