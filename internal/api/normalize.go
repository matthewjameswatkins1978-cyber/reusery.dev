package api

import (
	"context"
	"errors"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
)

// NormalizeInput is the POST /v1/normalize request.
type NormalizeInput struct {
	Body NormalizeRequest
}

// NormalizeOutput is the POST /v1/normalize response.
type NormalizeOutput struct {
	Body IntentResult
}

// handleNormalize runs one Packet 6 normalisation.
//
// It does not persist intent, does not discover, does not enrich and does not
// resolve: HTTP exposes the stages separately and the client chooses them.
func (h *Handler) handleNormalize(ctx context.Context, in *NormalizeInput) (*NormalizeOutput, error) {
	if gate := externalOperationsGate(h.deps.ExternalOperationsEnabled); gate != nil {
		return nil, gate
	}
	if h.deps.NormalizerFactory == nil {
		return nil, problem(503, CodeModelProviderUnconfigured,
			"the model provider is not configured; set REUSERY_OPENAI_API_KEY")
	}
	normalizer, err := h.deps.NormalizerFactory()
	if err != nil {
		if errors.Is(err, config.ErrMissingOpenAIAPIKey) {
			return nil, problem(503, CodeModelProviderUnconfigured,
				"the model provider is not configured; set REUSERY_OPENAI_API_KEY")
		}
		return nil, classify(err)
	}

	ctx, cancel := budget(ctx, BudgetNormalize)
	defer cancel()

	result, err := normalizer.Normalize(ctx, in.Body.Input)
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &NormalizeOutput{Body: mapIntentResult(result)}, nil
}
