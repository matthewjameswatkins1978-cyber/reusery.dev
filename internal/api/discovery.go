package api

import (
	"context"
	"errors"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
)

// DiscoveryInput is the POST /v1/discover request.
type DiscoveryInput struct {
	Body DiscoveryProfile
}

// DiscoveryOutput is the POST /v1/discover response.
type DiscoveryOutput struct {
	Body DiscoveryResult
}

// maxIssueDetails bounds how many provider issues are echoed inside an
// all-providers-failed error body.
const maxIssueDetails = 8

// handleDiscover runs one bounded Packet 5 discovery profile.
//
// Partial provider failure is a valid product result: as long as at least one
// provider succeeded the response is HTTP 200 with the provider reports and
// issues intact. Only an all-provider failure becomes an upstream error, and
// even then the safe provider issues are preserved in the error details.
func (h *Handler) handleDiscover(ctx context.Context, in *DiscoveryInput) (*DiscoveryOutput, error) {
	if gate := externalOperationsGate(h.deps.ExternalOperationsEnabled); gate != nil {
		return nil, gate
	}
	if h.deps.Discoverer == nil {
		return nil, problem(503, CodeUpstreamUnavailable, "discovery is not configured for this server")
	}

	profile := mapDiscoveryProfile(in.Body)
	if err := profile.Validate(); err != nil {
		return nil, classify(err)
	}

	ctx, cancel := budget(ctx, BudgetDiscover)
	defer cancel()

	result, err := h.deps.Discoverer.Discover(ctx, profile)
	if err != nil {
		if errors.Is(err, discovery.ErrProfile) || errors.Is(err, discovery.ErrProviderUnavailable) {
			return nil, classify(err)
		}
		var failed *discovery.ProvidersFailedError
		if errors.As(err, &failed) {
			return nil, problem(502, CodeAllProvidersFailed,
				"every configured provider failed operationally; inspect the provider issues for detail",
				issueDetails(discoveryIssues(result.Providers))...)
		}
		return nil, opError(ctx, err)
	}
	return &DiscoveryOutput{Body: mapDiscoveryResult(result)}, nil
}

// discoveryIssues flattens the safe provider issue messages from a run.
func discoveryIssues(reports []discovery.ProviderReport) []string {
	messages := make([]string, 0, maxIssueDetails)
	for _, report := range reports {
		for _, issue := range report.Issues {
			if len(messages) >= maxIssueDetails {
				return messages
			}
			messages = append(messages, issue.Message)
		}
	}
	return messages
}

// issueDetails converts bounded provider issue messages into error detail
// entries. Messages are already sanitised by the discovery and enrichment
// packages: they never contain a token, a database URL or a raw response body.
func issueDetails(messages []string) []error {
	details := make([]error, 0, len(messages))
	for _, message := range messages {
		details = append(details, errors.New(message))
	}
	return details
}
