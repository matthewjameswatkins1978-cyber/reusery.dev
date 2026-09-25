package discovery

import (
	"context"
	"errors"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
)

// IssueFromError classifies one transport or response problem as an
// inspectable provider issue.
//
// The message is deliberately safe: provider errors never carry authorization
// headers, tokens or credential-bearing URLs, and no response body is echoed.
func IssueFromError(providerID string, err error, query string) ProviderIssue {
	issue := ProviderIssue{Provider: providerID, Query: query, Kind: IssueUnavailable}

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
