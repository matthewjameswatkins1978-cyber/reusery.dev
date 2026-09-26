package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// operation builds the shared parts of an API v1 operation descriptor.
func operation(id, method, path, tag, summary, description string) huma.Operation {
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Tags:        []string{tag},
		Summary:     summary,
		Description: description,
	}
}

// registerRoutes declares every API v1 operation.
//
// Operation ids are part of the frozen contract: renaming one is a breaking
// change and belongs in /v2.
func (h *Handler) registerRoutes() {
	// ------------------------------------------------------------- operations
	op := operation("healthCheck", http.MethodGet, "/health", "Operations",
		"Liveness probe.",
		"Always HTTP 200 while the process is running. It never checks PostgreSQL, "+
			"OpenAI, GitHub, pkg.go.dev or deps.dev, so a degradable provider outage "+
			"never makes this endpoint fail.")
	register[struct{}, HealthOutput](h, op, h.handleHealth)

	op = operation("readyCheck", http.MethodGet, "/ready", "Operations",
		"Readiness probe.",
		"HTTP 200 when every registered readiness checker passes; HTTP 503 when one "+
			"fails. Only PostgreSQL is a readiness dependency; model and discovery "+
			"providers are degradable and are never checked here.")
	register[struct{}, ReadyOutput](h, op, h.handleReady)

	// ---------------------------------------------------------------- intent
	op = operation("normalizeIntent", http.MethodPost, "/v1/normalize", "Intent",
		"Normalise ordinary engineering intent into a structured Packet 6 result.",
		"Converts natural language into a provisional capability, contract, constraints, "+
			"ambiguities and assumptions. `ready`, `needs_clarification` and `unsupported` "+
			"are all valid product results and all return HTTP 200. Only a model-provider "+
			"or configuration failure is an HTTP error.\n\n"+
			"This operation does not persist intent, does not discover candidates, does "+
			"not enrich and does not resolve: the stages remain explicit and the client "+
			"chooses them.\n\nRequest body limit: 16 KiB.")
	op.MaxBodyBytes = MaxBodyNormalize
	op.Errors = []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	register[NormalizeInput, NormalizeOutput](h, op, h.handleNormalize)

	// ------------------------------------------------------------- discovery
	op = operation("discoverCandidates", http.MethodPost, "/v1/discover", "Discovery",
		"Run one bounded public discovery profile.",
		"Accepts the structured Packet 5 discovery profile as JSON; the API never "+
			"accepts a filesystem path, a --root or a profile filename. Provider data is "+
			"INFO/UNKNOWN only, partial provider failure remains inspectable in the "+
			"response, and discovery never resolves a candidate. Discovery is not "+
			"verification.\n\nRequest body limit: 64 KiB.")
	op.MaxBodyBytes = MaxBodyDiscover
	op.Errors = []int{http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	register[DiscoveryInput, DiscoveryOutput](h, op, h.handleDiscover)

	// ----------------------------------------------------------- enrichment
	op = operation("enrichCandidates", http.MethodPost, "/v1/enrich", "Evidence",
		"Record attributable external metadata for named specimens.",
		"Runs the bounded Packet 7 enrichment pass and persists INFO/UNKNOWN "+
			"observations under the Packet 7 trust rules. It never creates behavioural "+
			"PASS or FAIL evidence, never selects a candidate and never resolves. If at "+
			"least one provider succeeds the response is HTTP 200 with the provider "+
			"issues intact; only an all-provider failure is an upstream error.\n\nRequest body limit: 32 KiB.")
	op.MaxBodyBytes = MaxBodyEnrich
	op.Errors = []int{http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	register[EnrichInput, EnrichOutput](h, op, h.handleEnrich)

	// -------------------------------------------------------------- resolver
	op = operation("resolveCandidates", http.MethodPost, "/v1/resolve", "Resolver",
		"Compare candidates under a structured policy and produce a Packet 7 decision.",
		"Uses the Packet 7 quality service. The policy is supplied as structured JSON; "+
			"clients never submit a filesystem policy path. Initial resolve requests carry "+
			"no feedback.\n\n"+
			"Both `resolved` and `needs_verification` return HTTP 200. When resolved, "+
			"`resolution_id` and `resolution` are present and exactly one Resolution is "+
			"persisted. When `needs_verification`, both are null and nothing is persisted: "+
			"needs_verification is not a failure and not BUILD LOCALLY.\n\n"+
			"A `reference` outcome preserves every unresolved required behavioural "+
			"requirement as an unknown and never claims contract satisfaction.\n\nRequest body limit: 256 KiB.")
	op.MaxBodyBytes = MaxBodyResolve
	op.Errors = []int{http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge}
	register[ResolveInput, ResolveOutput](h, op, h.handleResolve)

	op = operation("refineResolution", http.MethodPost, "/v1/refine", "Resolver",
		"Reject a result with structured feedback and re-resolve deterministically.",
		"Stateless refinement: the caller resends the same base policy, the same bounded "+
			"candidate set and the complete accumulated feedback history. The server derives "+
			"the effective policy from scratch on every call, so feedback is never applied "+
			"twice and clients must not feed a previously derived effective policy back as "+
			"the base. At least one feedback item is required. This calls the same Packet 7 "+
			"quality service as /v1/resolve and implements no feedback logic of its own.\n\nRequest body limit: 256 KiB.")
	op.MaxBodyBytes = MaxBodyRefine
	op.Errors = []int{http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge}
	register[RefineInput, RefineOutput](h, op, h.handleRefine)

	// ------------------------------------------------------------ inspection
	op = operation("getPrimitive", http.MethodGet, "/v1/primitives", "Inspection",
		"Fetch one stored primitive.",
		"Selects a primitive by opaque Reusery id. Domain ids contain characters such "+
			"as / @ : and % and are passed as a query parameter rather than a path "+
			"segment, because they must be treated as opaque strings.")
	op.Errors = []int{http.StatusNotFound}
	register[GetPrimitiveInput, GetPrimitiveOutput](h, op, h.handleGetPrimitive)

	op = operation("getContract", http.MethodGet, "/v1/contracts", "Inspection",
		"Fetch one stored contract with its requirements.",
		"Selects a contract by opaque Reusery id supplied as a query parameter.")
	op.Errors = []int{http.StatusNotFound}
	register[GetContractInput, GetContractOutput](h, op, h.handleGetContract)

	op = operation("getSpecimen", http.MethodGet, "/v1/specimens", "Inspection",
		"Fetch one stored specimen.",
		"Selects a specimen by opaque Reusery id supplied as a query parameter.")
	op.Errors = []int{http.StatusNotFound}
	register[GetSpecimenInput, GetSpecimenOutput](h, op, h.handleGetSpecimen)

	op = operation("listEvidence", http.MethodGet, "/v1/evidence", "Evidence",
		"Fetch a bounded, ordered page of evidence for one subject.",
		"Results are ordered by observed_at ascending then evidence id ascending and "+
			"paginated with an opaque cursor. The default limit is 50 and the maximum is "+
			"100; the response never contains an unbounded evidence array. `next_cursor` "+
			"is null on the final page.")
	op.Errors = []int{http.StatusUnprocessableEntity}
	register[ListEvidenceInput, ListEvidenceOutput](h, op, h.handleListEvidence)

	op = operation("getResolution", http.MethodGet, "/v1/resolutions/{resolution_id}", "Inspection",
		"Fetch a previously persisted Resolution.",
		"Returns the remembered decision exactly as it was recorded. It does not "+
			"re-evaluate current evidence. The id is the PostgreSQL storage identity, "+
			"which is numeric and therefore safe as a path parameter.")
	op.Errors = []int{http.StatusNotFound}
	register[GetResolutionInput, GetResolutionOutput](h, op, h.handleGetResolution)

	h.documentReadyFallback()
	h.documentNullableObjects()
}
