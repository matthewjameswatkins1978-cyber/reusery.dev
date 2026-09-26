package api

import (
	"context"
)

// handleGetPrimitive returns one stored primitive.
func (h *Handler) handleGetPrimitive(ctx context.Context, in *GetPrimitiveInput) (*GetPrimitiveOutput, error) {
	if h.deps.Inspector == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	ctx, cancel := budget(ctx, BudgetInspect)
	defer cancel()

	primitive, err := h.deps.Inspector.GetPrimitive(ctx, in.ID)
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &GetPrimitiveOutput{Body: mapPrimitive(primitive)}, nil
}

// handleGetContract returns one stored contract with its requirements.
func (h *Handler) handleGetContract(ctx context.Context, in *GetContractInput) (*GetContractOutput, error) {
	if h.deps.Inspector == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	ctx, cancel := budget(ctx, BudgetInspect)
	defer cancel()

	contract, err := h.deps.Inspector.GetContract(ctx, in.ID)
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &GetContractOutput{Body: mapContract(contract)}, nil
}

// handleGetSpecimen returns one stored specimen.
func (h *Handler) handleGetSpecimen(ctx context.Context, in *GetSpecimenInput) (*GetSpecimenOutput, error) {
	if h.deps.Inspector == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	ctx, cancel := budget(ctx, BudgetInspect)
	defer cancel()

	specimen, err := h.deps.Inspector.GetSpecimen(ctx, in.ID)
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &GetSpecimenOutput{Body: mapSpecimen(specimen)}, nil
}

// handleGetResolution returns a remembered resolution without re-evaluating
// current evidence.
func (h *Handler) handleGetResolution(ctx context.Context, in *GetResolutionInput) (*GetResolutionOutput, error) {
	if h.deps.Resolver == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	ctx, cancel := budget(ctx, BudgetInspect)
	defer cancel()

	resolution, err := h.deps.Resolver.Resolution(ctx, in.ResolutionID)
	if err != nil {
		return nil, opError(ctx, err)
	}
	return &GetResolutionOutput{Body: StoredResolution{
		ResolutionID: in.ResolutionID,
		Resolution:   mapResolution(resolution),
	}}, nil
}

// handleListEvidence returns one bounded, ordered page of evidence.
//
// It never emits an unbounded evidence array: the store fetches limit+1 rows
// to decide whether a next cursor exists, and the cursor is an opaque
// versioned payload rather than a SQL offset.
func (h *Handler) handleListEvidence(ctx context.Context, in *ListEvidenceInput) (*ListEvidenceOutput, error) {
	if h.deps.Inspector == nil {
		return nil, problem(500, CodeInternalError, "the request could not be completed")
	}
	position, err := decodeCursor(in.Cursor)
	if err != nil {
		return nil, classify(err)
	}

	ctx, cancel := budget(ctx, BudgetInspect)
	defer cancel()

	rows, err := h.deps.Inspector.ListEvidenceAfter(ctx, in.SubjectID,
		position.ObservedAt, position.EvidenceID, in.Limit+1)
	if err != nil {
		return nil, opError(ctx, err)
	}

	hasNext := len(rows) > in.Limit
	if hasNext {
		rows = rows[:in.Limit]
	}

	page := EvidencePage{
		SubjectID: in.SubjectID,
		Evidence:  mapEvidenceList(rows),
		Limit:     in.Limit,
	}
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor = nextCursorString(last.ObservedAt, last.ID, true)
	}
	return &ListEvidenceOutput{Body: page}, nil
}
