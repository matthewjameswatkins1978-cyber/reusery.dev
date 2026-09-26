// Package enrichment fetches attributable external facts about specimens that
// discovery has already found.
//
// Enrichment is metadata gathering, not verification. Every observation it
// produces is attributable INFO or UNKNOWN evidence with an empty AppliesTo,
// because the Packet 2 behavioural evaluator remains the only authority over
// requirement satisfaction. No enrichment provider can emit PASS or FAIL, no
// provider result is candidate selection, and a provider failure never becomes
// evidence about any candidate.
package enrichment

import (
	"context"
	"errors"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Provider IDs this packet can construct.
const (
	ProviderDepsDev        = "deps.dev"
	ProviderGitHubMetadata = "github-metadata"
)

// KnownProviderIDs returns the provider IDs supported by this packet.
func KnownProviderIDs() []string {
	return []string{ProviderDepsDev, ProviderGitHubMetadata}
}

// UserAgent identifies Reusery to every enrichment provider.
const UserAgent = "reusery-enrichment (+https://reusery.dev)"

// Budget bounds one enrichment run. These are conservative fixed defaults, not
// a configuration surface: no provider may exceed them, there is no pagination
// anywhere and there is no retry loop anywhere.
type Budget struct {
	// MaxSpecimens caps specimens accepted for one run.
	MaxSpecimens int
	// MaxProviders caps applicable providers used per specimen.
	MaxProviders int
	// MaxHTTPRequests caps HTTP requests one provider may issue for one
	// specimen.
	MaxHTTPRequests int
	// Timeout is the wall-clock budget for one provider on one specimen.
	Timeout time.Duration
	// RunTimeout is the wall-clock budget for the whole run.
	RunTimeout time.Duration
	// MaxResponseBytes caps one HTTP response body.
	MaxResponseBytes int64
}

// DefaultBudget returns the bounded defaults used by this packet.
func DefaultBudget() Budget {
	return Budget{
		MaxSpecimens:     24,
		MaxProviders:     2,
		MaxHTTPRequests:  3,
		Timeout:          10 * time.Second,
		RunTimeout:       30 * time.Second,
		MaxResponseBytes: 2 << 20, // 2 MiB
	}
}

// Request is one provider's input for one specimen. It carries domain values
// only: no CLI type, no PostgreSQL type and no HTTP request object.
type Request struct {
	Specimen         model.Specimen
	ExistingEvidence []model.Evidence
	ObservedAt       time.Time
	Budget           Budget
}

// ProviderResult is one provider's bounded output for one specimen.
type ProviderResult struct {
	Evidence []model.Evidence
	Requests int
	Issues   []ProviderIssue
}

// IssueKind classifies an operational problem. Operational failure is never
// candidate evidence: it says nothing about any candidate.
type IssueKind string

const (
	IssueRateLimited     IssueKind = "rate_limited"
	IssueAuthentication  IssueKind = "authentication"
	IssueForbidden       IssueKind = "forbidden"
	IssueTimeout         IssueKind = "timeout"
	IssueUnavailable     IssueKind = "unavailable"
	IssueInvalidResponse IssueKind = "invalid_response"
	IssueBudgetExhausted IssueKind = "budget_exhausted"
	IssueUnsupported     IssueKind = "unsupported"
	IssueIdentity        IssueKind = "identity_unresolved"
)

// ProviderIssue is an inspectable operational problem carrying only safe
// information: never tokens, authorization headers, credentials or response
// bodies.
type ProviderIssue struct {
	Kind       IssueKind `json:"kind"`
	Provider   string    `json:"provider"`
	SpecimenID string    `json:"specimen_id,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	RetryAfter string    `json:"retry_after,omitempty"`
	Message    string    `json:"message"`
}

// ProviderReport summarises one provider's work on one specimen.
type ProviderReport struct {
	ID            string          `json:"id"`
	Succeeded     bool            `json:"succeeded"`
	Requests      int             `json:"requests"`
	EvidenceCount int             `json:"evidence_count"`
	Issues        []ProviderIssue `json:"issues"`
}

// SpecimenReport summarises what enrichment did for one specimen.
type SpecimenReport struct {
	SpecimenID    string           `json:"specimen_id"`
	Supported     bool             `json:"supported"`
	EvidenceCount int              `json:"evidence_count"`
	Providers     []ProviderReport `json:"providers"`
}

// Result is the canonical enrichment output: what was observed and what each
// provider did. It contains no score, no ranking and no candidate choice.
type Result struct {
	ObservedAt time.Time        `json:"observed_at"`
	Evidence   []model.Evidence `json:"evidence"`
	Specimens  []SpecimenReport `json:"specimens"`
}

// Provider is the replaceable enrichment boundary. Concrete provider packages
// implement it; the enrichment service depends only on this interface.
//
// A provider returns a non-nil error only when it produced nothing usable at
// all. Partial failure is reported through ProviderResult.Issues with a nil
// error, so one provider failing never erases another provider's evidence.
type Provider interface {
	ID() string
	Supports(model.Specimen) bool
	Enrich(context.Context, Request) (ProviderResult, error)
}

// Clock supplies the single observation timestamp for one enrichment run.
type Clock func() time.Time

// ProvidersFailedError reports that every applicable provider call failed
// operationally. The accompanying Result still carries the provider reports so
// the failure stays inspectable.
type ProvidersFailedError struct {
	Providers []string
}

func (e *ProvidersFailedError) Error() string {
	return "enrichment: every applicable provider call failed operationally"
}

// Request validation errors. These are request problems, not provider
// failures.
var (
	ErrNoSpecimens       = errors.New("enrichment: no specimen ids were supplied")
	ErrTooManySpecimens  = errors.New("enrichment: specimen list exceeds the run budget")
	ErrDuplicateSpecimen = errors.New("enrichment: duplicate specimen id")
	// ErrUnsafeOutput reports provider-produced evidence that violates the
	// enrichment trust rules. It is checked before anything is persisted.
	ErrUnsafeOutput = errors.New("enrichment: unsafe provider output")
)

// ClassifyStatus maps an HTTP status onto an inspectable issue kind.
func ClassifyStatus(status int, rateLimitRemaining string) IssueKind {
	switch status {
	case 429:
		return IssueRateLimited
	case 401:
		return IssueAuthentication
	case 403:
		if rateLimitRemaining == "0" {
			return IssueRateLimited
		}
		return IssueForbidden
	case 400, 422:
		return IssueInvalidResponse
	case 404, 406, 415:
		return IssueInvalidResponse
	}
	return IssueUnavailable
}

// IssueFromError classifies one transport or response problem. The message is
// deliberately safe: no headers, no tokens and no response bodies.
func IssueFromError(providerID, specimenID string, err error) ProviderIssue {
	issue := ProviderIssue{Provider: providerID, SpecimenID: specimenID, Kind: IssueUnavailable}

	var statusErr *httpx.StatusError
	switch {
	case errors.As(err, &statusErr):
		issue.Kind = ClassifyStatus(statusErr.StatusCode, statusErr.RateLimitRemaining)
		issue.StatusCode = statusErr.StatusCode
		issue.RetryAfter = statusErr.RetryAfter
		issue.Message = statusErr.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		issue.Kind = IssueTimeout
		issue.Message = "request exceeded the provider budget"
	case errors.Is(err, httpx.ErrBodyTooLarge),
		errors.Is(err, httpx.ErrNotJSON),
		errors.Is(err, httpx.ErrDecode),
		errors.Is(err, httpx.ErrBadBaseURL),
		errors.Is(err, httpx.ErrBadTarget),
		errors.Is(err, httpx.ErrRedirectRefused):
		issue.Kind = IssueInvalidResponse
		issue.Message = err.Error()
	default:
		issue.Message = "provider request failed: " + err.Error()
	}
	return issue
}
