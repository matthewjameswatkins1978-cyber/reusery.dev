package api

import (
	"context"
	"errors"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
)

// EnrichInput is the POST /v1/enrich request.
type EnrichInput struct {
	Body EnrichRequest
}

// EnrichOutput is the POST /v1/enrich response.
type EnrichOutput struct {
	Body EnrichResult
}

// handleEnrich runs one bounded Packet 7 enrichment pass.
//
// It records INFO/UNKNOWN observations under the Packet 7 trust rules and
// nothing else: no behavioural PASS or FAIL, no selection, no resolution.
// Partial provider failure returns HTTP 200 with the reports intact; only an
// all-provider failure is an upstream error.
func (h *Handler) handleEnrich(ctx context.Context, in *EnrichInput) (*EnrichOutput, error) {
	if gate := externalOperationsGate(h.deps.ExternalOperationsEnabled); gate != nil {
		return nil, gate
	}
	if h.deps.Enricher == nil {
		return nil, problem(503, CodeUpstreamUnavailable, "enrichment is not configured for this server")
	}

	ctx, cancel := budget(ctx, BudgetEnrich)
	defer cancel()

	result, err := h.deps.Enricher.Enrich(ctx, in.Body.SpecimenIDs)
	if err != nil {
		var failed *enrichment.ProvidersFailedError
		if errors.As(err, &failed) {
			return nil, problem(502, CodeAllProvidersFailed,
				"every applicable provider failed operationally; inspect the provider issues for detail",
				issueDetails(enrichmentIssues(result.Specimens))...)
		}
		return nil, opError(ctx, err)
	}
	return &EnrichOutput{Body: mapEnrichResult(result)}, nil
}

// enrichmentIssues flattens the safe provider issue messages from a run.
func enrichmentIssues(reports []enrichment.SpecimenReport) []string {
	messages := make([]string, 0, maxIssueDetails)
	for _, report := range reports {
		for _, provider := range report.Providers {
			for _, issue := range provider.Issues {
				if len(messages) >= maxIssueDetails {
					return messages
				}
				messages = append(messages, issue.Message)
			}
		}
	}
	return messages
}
