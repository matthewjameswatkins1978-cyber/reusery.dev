package intent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Normalize turns ordinary engineering language into a bounded Result.
//
// Flow: validate local input (no paid call) → render the versioned prompt and
// schema → one provider call → decode the structured draft → deterministic
// semantic validation → at most one bounded repair call → validate again →
// deterministic identifiers and domain mapping.
//
// It touches no database, no discovery provider and no resolver, and it never
// produces reuse, adapt, depend, reference or build_locally.
func (n *Normalizer) Normalize(ctx context.Context, raw string) (Result, error) {
	input, err := prepareInput(raw, n.bounds)
	if err != nil {
		return Result{}, err
	}

	response, err := n.generate(ctx, ProviderRequest{
		Instructions:    Prompt(),
		Input:           input,
		JSONSchema:      JSONSchema(),
		SchemaName:      SchemaName,
		MaxOutputTokens: n.bounds.MaxOutputTokens,
	})
	if err != nil {
		return Result{}, err
	}

	draft, err := DecodeDraft(response.StructuredJSON)
	if err != nil {
		return Result{}, err
	}

	calls := 1
	usage := response.Usage

	failures := Validate(draft)
	if len(failures) > 0 {
		if n.bounds.MaxCalls < 2 {
			return Result{}, fmt.Errorf("%w: %s", ErrValidation, strings.Join(failures, "; "))
		}

		repairResponse, err := n.generate(ctx, ProviderRequest{
			Instructions:    RenderRepairPrompt(response.StructuredJSON, failures),
			Input:           input,
			JSONSchema:      JSONSchema(),
			SchemaName:      SchemaName,
			MaxOutputTokens: n.bounds.MaxOutputTokens,
			Repair: &RepairContext{
				PriorDraft: response.StructuredJSON,
				Failures:   failures,
			},
		})
		if err != nil {
			return Result{}, fmt.Errorf("intent: bounded repair call failed: %w", err)
		}

		calls = 2
		usage = usage.Add(repairResponse.Usage)
		response = repairResponse

		draft, err = DecodeDraft(response.StructuredJSON)
		if err != nil {
			return Result{}, err
		}
		failures = Validate(draft)
		if len(failures) > 0 {
			return Result{}, fmt.Errorf("%w: %s", ErrValidation, strings.Join(failures, "; "))
		}
	}

	metadata := GenerationMetadata{
		Provider:      response.ProviderID,
		Model:         response.Model,
		ResponseID:    response.ResponseID,
		PromptVersion: PromptVersion,
		SchemaVersion: SchemaVersion,
		Calls:         calls,
		Repaired:      calls > 1,
		Usage:         usage,
	}
	return Build(input, draft, metadata), nil
}

// generate makes exactly one provider call inside the per-call timeout. There
// is no operational retry: a hidden retry would be another paid model call and
// could amplify an outage. The classified error is returned for the caller to
// decide about.
func (n *Normalizer) generate(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, n.bounds.Timeout)
	defer cancel()

	response, err := n.provider.Generate(callCtx, request)
	if err != nil {
		// The bound is this service's own rule, so exceeding it is classified
		// here rather than left for every provider to invent its own wording.
		if callCtx.Err() == context.DeadlineExceeded {
			return ProviderResponse{}, NewProviderError(
				ErrorTimeout, 0, "call exceeded its per-call time budget")
		}
		return ProviderResponse{}, err
	}
	return response, nil
}

// prepareInput validates local input before any paid model call. Invalid local
// input must consume zero model calls.
func prepareInput(raw string, bounds Bounds) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%w: input is not valid UTF-8", ErrInvalidInput)
	}
	input := strings.TrimSpace(raw)
	if input == "" {
		return "", fmt.Errorf("%w: input is empty", ErrInvalidInput)
	}
	if len(input) > bounds.MaxInputBytes {
		return "", fmt.Errorf("%w: input is %d bytes, maximum is %d",
			ErrInvalidInput, len(input), bounds.MaxInputBytes)
	}
	return input, nil
}
