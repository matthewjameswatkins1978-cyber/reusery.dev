// Package github discovers public GitHub repositories and source files
// through the GitHub REST API.
//
// The two providers share one client but keep separate provider IDs, because
// a repository hit and a file hit are meaningfully different candidate shapes.
//
// Everything they return is attributable INFO or UNKNOWN observation. A code
// hit containing exec.CommandContext is not evidence that a candidate supports
// cancellation, and a hit containing StdoutPipe is not evidence that stdout
// and stderr are drained concurrently: search relevance is not verification.
package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/httpx"
)

const (
	// DefaultBaseURL is the code-owned production endpoint. It is never taken
	// from user configuration.
	DefaultBaseURL = "https://api.github.com"
	// APIVersion pins GitHub's media type version for the whole packet.
	APIVersion = "2026-03-10"

	repositorySearchPath = "/search/repositories"
	codeSearchPath       = "/search/code"
)

// Client is the shared GitHub REST client used by both GitHub providers.
//
// The token is optional: without it GitHub's public unauthenticated limits
// apply. It is only ever placed in an Authorization header — never in a URL,
// never in an error, never logged and never persisted.
type Client struct {
	baseURL string
	token   string
}

// NewClient builds the production client.
func NewClient(token string) *Client { return NewClientWithBaseURL(DefaultBaseURL, token) }

// NewClientWithBaseURL builds a client against an explicit base URL. Tests
// inject an httptest server here; production never calls it.
func NewClientWithBaseURL(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
	}
}

// HasToken reports whether an optional token is configured. Readiness never
// depends on it.
func (c *Client) HasToken() bool { return c.token != "" }

// headers returns the fixed GitHub headers plus the optional bearer token.
func (c *Client) headers() map[string]string {
	headers := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": APIVersion,
	}
	if c.token != "" {
		headers["Authorization"] = "Bearer " + c.token
	}
	return headers
}

// get issues one bounded authenticated-or-public GET.
func (c *Client) get(ctx context.Context, maxResponseBytes int64, path string, into any) (httpx.Response, error) {
	api := httpx.New(c.baseURL, discovery.UserAgent, maxResponseBytes)
	return api.GetJSON(ctx, path, c.headers(), into)
}

// Get issues one bounded authenticated-or-public GET against the fixed GitHub
// host. Packet 7's metadata enrichment calls it so there is exactly one GitHub
// HTTP stack: same token handling, same API version header, same rate-limit
// fields, same safe redirects and the same rule that a token is only ever
// placed in an Authorization header.
func (c *Client) Get(ctx context.Context, maxResponseBytes int64, path string, into any) (httpx.Response, error) {
	return c.get(ctx, maxResponseBytes, path, into)
}

// searchResult is the envelope both GitHub search endpoints share.
type searchResult struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
}

// issue classifies a transport or response problem for one provider.
func issue(providerID string, err error, query string) discovery.ProviderIssue {
	return discovery.IssueFromError(providerID, err, query)
}

// incompleteIssue records GitHub's own admission that it truncated the result
// set. It is an operational fact about the search, not a claim about any
// candidate.
func incompleteIssue(providerID, query string) discovery.ProviderIssue {
	return discovery.ProviderIssue{
		Kind:     discovery.IssueIncompleteResults,
		Provider: providerID,
		Query:    query,
		Message:  "GitHub reported incomplete_results for this query; the returned set is a partial view",
	}
}

// budgetIssue records that a provider stopped early.
func budgetIssue(providerID, query string, budget int) discovery.ProviderIssue {
	return discovery.ProviderIssue{
		Kind:     discovery.IssueBudgetExhausted,
		Provider: providerID,
		Query:    query,
		Message:  fmt.Sprintf("request budget of %d reached; query %q was not sent", budget, query),
	}
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
