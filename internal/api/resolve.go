package api

import (
	"context"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// ResolveInput is the POST /v1/resolve request.
type ResolveInput struct {
	Body ResolveRequest
}

// ResolveOutput is the POST /v1/resolve response.
type ResolveOutput struct {
	Body DecisionResponse
}

// RefineInput is the POST /v1/refine request.
type RefineInput struct {
	Body RefineRequest
}

// RefineOutput is the POST /v1/refine response.
type RefineOutput struct {
	Body DecisionResponse
}

// handleResolve produces a Packet 7 quality decision over HTTP.
//
// It calls resolver.QualityService and nothing else. The Packet 4
// first-acceptable kernel is deliberately not reachable from this endpoint,
// there is no API-specific resolver, and no feedback is accepted on an
// initial resolve.
func (h *Handler) handleResolve(ctx context.Context, in *ResolveInput) (*ResolveOutput, error) {
	if h.deps.Resolver == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	pol, err := mapPolicy(in.Body.Policy)
	if err != nil {
		return nil, classify(err)
	}

	ctx, cancel := budget(ctx, BudgetResolve)
	defer cancel()

	stored, err := h.deps.Resolver.Choose(ctx, pol, resolver.QualityRequest{
		PrimitiveID: in.Body.PrimitiveID,
		ContractID:  in.Body.ContractID,
		Candidates:  mapCandidateRefs(in.Body.Candidates),
	})
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &ResolveOutput{Body: mapDecision(stored)}, nil
}

// handleRefine re-runs the same decision with the complete feedback history.
//
// Refinement is stateless. The client resends the original base policy, the
// same bounded candidate set and every accumulated feedback item; the server
// derives the effective policy from scratch. Feeding a previously derived
// effective policy back in as the base would apply feedback twice, which is
// why the base policy is always the caller's own original.
func (h *Handler) handleRefine(ctx context.Context, in *RefineInput) (*RefineOutput, error) {
	if h.deps.Resolver == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	if len(in.Body.Feedback) == 0 {
		return nil, problem(422, CodeInvalidRequest, "refine requires at least one feedback item")
	}
	pol, err := mapPolicy(in.Body.Policy)
	if err != nil {
		return nil, classify(err)
	}

	ctx, cancel := budget(ctx, BudgetRefine)
	defer cancel()

	stored, err := h.deps.Resolver.Choose(ctx, pol, resolver.QualityRequest{
		PrimitiveID: in.Body.PrimitiveID,
		ContractID:  in.Body.ContractID,
		Candidates:  mapCandidateRefs(in.Body.Candidates),
		Feedback:    mapFeedback(in.Body.Feedback),
	})
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &RefineOutput{Body: mapDecision(stored)}, nil
}
