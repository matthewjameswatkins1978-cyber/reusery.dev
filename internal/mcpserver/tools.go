package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// The exact Packet 9 tool surface. These names are a frozen public contract:
// renaming one is a breaking change that belongs in a new contract revision.
const (
	ToolCatalog           = "reusery_catalog"
	ToolDiscover          = "reusery_discover"
	ToolEnrich            = "reusery_enrich"
	ToolResolve           = "reusery_resolve"
	ToolRefine            = "reusery_refine"
	ToolInspectEvidence   = "reusery_inspect_evidence"
	ToolInspectResolution = "reusery_inspect_resolution"
	ToolReportOutcome     = "reusery_report_outcome"
)

// ToolNames returns the tool surface in alphabetical order. It is the single
// source of truth for the contract snapshot and its drift test.
func ToolNames() []string {
	return []string{
		ToolCatalog,
		ToolDiscover,
		ToolEnrich,
		ToolInspectEvidence,
		ToolInspectResolution,
		ToolRefine,
		ToolReportOutcome,
		ToolResolve,
	}
}

// There is deliberately no reusery_normalize tool. The caller of this server
// is already an AI agent: routing its structured reasoning through another
// model call would add cost and latency for no value. Packet 6 normalisation
// stays available to humans, the CLI and HTTP clients. There is also no
// health, ready or openapi tool: the tool surface is resolver-native, not an
// HTTP mirror.

var falseHint = false

// readHints describes a tool that only observes stored state.
func readHints(openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld}
}

// writeHints describes a tool that appends state without destroying anything.
func writeHints(openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &falseHint,
		IdempotentHint:  false,
		OpenWorldHint:   &openWorld,
	}
}

// registerTools adds exactly the eight Packet 9 tools.
func registerTools(s *mcp.Server, deps Dependencies) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolCatalog,
		Description: "List the engineering capabilities Reusery knows, or inspect one primitive with its contract and ordered requirements. Read-only capability discovery, not a registry dump.",
		Annotations: readHints(false),
	}, deps.handleCatalog)

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolDiscover,
		Description: "Discover plausible public specimens for a known primitive and contract through the bounded Packet 5 providers. " +
			"Provider data is INFO/UNKNOWN only: discovery is not verification and can never produce behavioural evidence.",
		Annotations: writeHints(true),
	}, deps.handleDiscover)

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolEnrich,
		Description: "Record attributable licence, advisory, dependency and maintenance metadata for discovered specimens. " +
			"Metadata is never behavioural evidence and can never satisfy a contract requirement.",
		Annotations: writeHints(true),
	}, deps.handleEnrich)

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolResolve,
		Description: "Compare candidates under an explicit policy and produce a Packet 7 decision. " +
			"Returns resolved or needs_verification; both are successful results. " +
			"REFERENCE means engineering knowledge, never a verified implementation.",
		Annotations: writeHints(false),
	}, deps.handleResolve)

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolRefine,
		Description: "Reject a result with a supported structured reason and re-resolve. " +
			"Stateless: resend the original base policy, the same bounded candidate set and the complete accumulated feedback history.",
		Annotations: writeHints(false),
	}, deps.handleRefine)

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolInspectEvidence,
		Description: "Read a bounded, ordered page of stored evidence for one subject. Call this only when uncertainty actually matters; claims are returned unabridged.",
		Annotations: readHints(false),
	}, deps.handleInspectEvidence)

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolInspectResolution,
		Description: "Read a remembered Resolution exactly as recorded, together with any outcome events reported against it. Never re-evaluates current evidence.",
		Annotations: readHints(false),
	}, deps.handleInspectResolution)

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolReportOutcome,
		Description: "Record what actually happened after a Resolution was used. Append-only factual feedback: it is not Evidence, never changes a policy and never triggers a re-resolution. Report success only when integration really succeeded.",
		Annotations: writeHints(false),
	}, deps.handleReportOutcome)
}

// summaryText carries the short human-readable summary that accompanies the
// structured output. The structured JSON is never echoed into this block: that
// would double the token cost of every call.
func summaryText(summary string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}
}

// ------------------------------------------------------------------- catalog

func (d Dependencies) handleCatalog(ctx context.Context, _ *mcp.CallToolRequest, in CatalogInput) (*mcp.CallToolResult, CatalogResult, error) {
	out := CatalogResult{Primitives: []CatalogPrimitive{}, Primitive: nil, Contract: nil}
	if d.Catalog == nil {
		return nil, out, classify(errMissingService)
	}

	if strings.TrimSpace(in.PrimitiveID) == "" {
		capabilities, err := app.ListCapabilities(ctx, d.Catalog, MaxCatalogPrimitives)
		if err != nil {
			return nil, out, classify(err)
		}
		for _, capability := range capabilities {
			out.Primitives = append(out.Primitives, mapCapability(capability))
		}
		return summaryText(fmt.Sprintf(
			"Reusery knows %d engineering capability(ies). Ask again with primitive_id to inspect one contract.",
			len(out.Primitives))), out, nil
	}

	primitive, err := d.Catalog.GetPrimitive(ctx, in.PrimitiveID)
	if err != nil {
		return nil, out, classify(err)
	}
	contract, err := d.Catalog.GetContract(ctx, primitive.ContractID)
	if err != nil {
		return nil, out, classify(err)
	}
	mappedPrimitive := mapCatalogPrimitive(primitive, contract.Summary)
	mappedContract := mapCatalogContract(contract)
	out.Primitive = &mappedPrimitive
	out.Contract = &mappedContract

	required := 0
	for _, requirement := range contract.Requirements {
		if requirement.Required {
			required++
		}
	}
	return summaryText(fmt.Sprintf(
		"Primitive %s has %d requirement(s), %d required. Contract satisfaction is not established by this tool.",
		primitive.ID, len(contract.Requirements), required)), out, nil
}

// ------------------------------------------------------------------ discover

func (d Dependencies) handleDiscover(ctx context.Context, _ *mcp.CallToolRequest, in discovery.Profile) (*mcp.CallToolResult, DiscoverResult, error) {
	var out DiscoverResult
	if !d.ExternalOperationsEnabled {
		return nil, out, externalOperationsDisabled()
	}
	if d.Discoverer == nil {
		return nil, out, classify(errMissingService)
	}

	profile := discovery.Profile{
		SchemaVersion: discovery.SchemaVersion,
		PrimitiveID:   in.PrimitiveID,
		ContractID:    in.ContractID,
		Providers:     in.Providers,
	}
	if err := profile.Validate(); err != nil {
		return nil, out, classify(err)
	}

	result, err := d.Discoverer.Discover(ctx, profile)
	if err != nil {
		var failed *discovery.ProvidersFailedError
		if errors.As(err, &failed) {
			return nil, out, allProvidersFailed(discoveryIssueMessages(result.Providers))
		}
		return nil, out, classify(err)
	}

	mapped := mapDiscoverResult(result)
	return summaryText(fmt.Sprintf(
		"Discovered %d candidate(s) across %d provider(s). Discovery is not verification and records no behavioural evidence.",
		len(mapped.Candidates), len(mapped.Providers))), mapped, nil
}

func discoveryIssueMessages(reports []discovery.ProviderReport) []string {
	messages := make([]string, 0, maxIssueDetail)
	for _, report := range reports {
		for _, issue := range report.Issues {
			if len(messages) >= maxIssueDetail {
				return messages
			}
			messages = append(messages, issue.Message)
		}
	}
	return messages
}

// -------------------------------------------------------------------- enrich

func (d Dependencies) handleEnrich(ctx context.Context, _ *mcp.CallToolRequest, in EnrichInput) (*mcp.CallToolResult, EnrichResult, error) {
	var out EnrichResult
	if !d.ExternalOperationsEnabled {
		return nil, out, externalOperationsDisabled()
	}
	if d.Enricher == nil {
		return nil, out, classify(errMissingService)
	}

	result, err := d.Enricher.Enrich(ctx, in.SpecimenIDs)
	if err != nil {
		var failed *enrichment.ProvidersFailedError
		if errors.As(err, &failed) {
			return nil, out, allProvidersFailed(enrichmentIssueMessages(result.Specimens))
		}
		return nil, out, classify(err)
	}

	mapped := mapEnrichResult(result)
	return summaryText(fmt.Sprintf(
		"Recorded %d observation(s) across %d specimen(s). Metadata can never satisfy a behavioural requirement.",
		mapped.Evidence, len(mapped.Specimens))), mapped, nil
}

func enrichmentIssueMessages(reports []enrichment.SpecimenReport) []string {
	messages := make([]string, 0, maxIssueDetail)
	for _, report := range reports {
		for _, provider := range report.Providers {
			for _, issue := range provider.Issues {
				if len(messages) >= maxIssueDetail {
					return messages
				}
				messages = append(messages, issue.Message)
			}
		}
	}
	return messages
}

// ------------------------------------------------------------------- resolve

func (d Dependencies) handleResolve(ctx context.Context, _ *mcp.CallToolRequest, in ResolveInput) (*mcp.CallToolResult, DecisionResult, error) {
	var out DecisionResult
	if d.Resolver == nil || d.Inspector == nil {
		return nil, out, classify(errMissingService)
	}
	pol, err := mapPolicy(in.Policy)
	if err != nil {
		return nil, out, classify(err)
	}
	// Load the contract first so a typo cannot persist a resolution and then
	// fail to explain it.
	contract, err := d.Inspector.GetContract(ctx, in.ContractID)
	if err != nil {
		return nil, out, classify(err)
	}

	stored, err := d.Resolver.Choose(ctx, pol, resolver.QualityRequest{
		PrimitiveID: in.PrimitiveID,
		ContractID:  in.ContractID,
		Candidates:  mapCandidateRefs(in.Candidates),
	})
	if err != nil {
		return nil, out, classify(err)
	}

	out = buildDecision(stored, contract)
	return summaryText(decisionSummary(out)), out, nil
}

// -------------------------------------------------------------------- refine

func (d Dependencies) handleRefine(ctx context.Context, _ *mcp.CallToolRequest, in RefineInput) (*mcp.CallToolResult, RefineResult, error) {
	var out RefineResult
	if d.Resolver == nil || d.Inspector == nil {
		return nil, out, classify(errMissingService)
	}
	if len(in.Feedback) == 0 {
		return nil, out, newToolError(CodeInvalidRequest, "refine requires at least one feedback item")
	}
	pol, err := mapPolicy(in.Policy)
	if err != nil {
		return nil, out, classify(err)
	}
	contract, err := d.Inspector.GetContract(ctx, in.ContractID)
	if err != nil {
		return nil, out, classify(err)
	}

	stored, err := d.Resolver.Choose(ctx, pol, resolver.QualityRequest{
		PrimitiveID: in.PrimitiveID,
		ContractID:  in.ContractID,
		Candidates:  mapCandidateRefs(in.Candidates),
		Feedback:    in.Feedback,
	})
	if err != nil {
		return nil, out, classify(err)
	}

	out = buildRefine(stored, contract)
	return summaryText("Refined. " + decisionSummary(out.DecisionResult)), out, nil
}

// ------------------------------------------------------------------- inspect

func (d Dependencies) handleInspectEvidence(ctx context.Context, _ *mcp.CallToolRequest, in EvidenceInput) (*mcp.CallToolResult, EvidenceResultPage, error) {
	var out EvidenceResultPage
	if d.Inspector == nil {
		return nil, out, classify(errMissingService)
	}
	if strings.TrimSpace(in.SubjectID) == "" {
		return nil, out, newToolError(CodeInvalidRequest, "subject_id is required")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultEvidenceLimit
	}
	if limit < 1 || limit > MaxEvidenceLimit {
		return nil, out, newToolError(CodeInvalidRequest,
			"limit must be between 1 and %d", MaxEvidenceLimit)
	}

	var afterTime time.Time
	var afterID string
	if in.After != nil {
		if in.After.ObservedAt != nil {
			afterTime = *in.After.ObservedAt
		}
		afterID = in.After.EvidenceID
		if afterID == "" {
			return nil, out, newToolError(CodeInvalidRequest,
				"after.evidence_id is required when after is supplied")
		}
		if in.After.ObservedAt == nil {
			return nil, out, newToolError(CodeInvalidRequest,
				"after.observed_at is required when after is supplied")
		}
	}

	rows, err := d.Inspector.ListEvidenceAfter(ctx, in.SubjectID, afterTime, afterID, limit+1)
	if err != nil {
		return nil, out, classify(err)
	}
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}

	out = mapEvidencePage(in.SubjectID, rows, limit, hasNext)
	if out.NextAfter != nil {
		return summaryText(fmt.Sprintf(
			"%d observation(s) for %s; more are available.", len(out.Evidence), in.SubjectID)), out, nil
	}
	return summaryText(fmt.Sprintf(
		"%d observation(s) for %s; this is the last page.", len(out.Evidence), in.SubjectID)), out, nil
}

func (d Dependencies) handleInspectResolution(ctx context.Context, _ *mcp.CallToolRequest, in InspectResolutionInput) (*mcp.CallToolResult, ResolutionResult, error) {
	var out ResolutionResult
	if d.Resolver == nil || d.Outcomes == nil {
		return nil, out, classify(errMissingService)
	}
	resolution, err := d.Resolver.Resolution(ctx, in.ResolutionID)
	if err != nil {
		return nil, out, classify(err)
	}
	events, err := d.Outcomes.List(ctx, in.ResolutionID, OutcomeListLimit)
	if err != nil {
		return nil, out, classify(err)
	}

	out = ResolutionResult{
		ResolutionID:    in.ResolutionID,
		Resolution:      mapResolution(resolution),
		OutcomeFeedback: mapOutcomeEvents(events),
	}
	return summaryText(fmt.Sprintf(
		"Resolution %d: %s under policy %s. %d outcome event(s) recorded. This is remembered history, not a fresh evaluation.",
		in.ResolutionID, resolution.Outcome, resolution.PolicyID, len(events))), out, nil
}

// ------------------------------------------------------------------- outcome

func (d Dependencies) handleReportOutcome(ctx context.Context, _ *mcp.CallToolRequest, in OutcomeInput) (*mcp.CallToolResult, OutcomeResult, error) {
	var out OutcomeResult
	if d.Outcomes == nil {
		return nil, out, classify(errMissingService)
	}
	stored, err := d.Outcomes.Report(ctx, in.ResolutionID, in.Kind, in.Note)
	if err != nil {
		if errors.Is(err, outcome.ErrResolutionNotFound) {
			return nil, out, newToolError(CodeNotFound, "the requested resolution does not exist")
		}
		return nil, out, classify(err)
	}

	out = OutcomeResult{
		ID:           stored.ID,
		ResolutionID: stored.ResolutionID,
		Kind:         stored.Kind,
		Note:         stored.Note,
		RecordedAt:   stored.RecordedAt,
	}
	return summaryText(fmt.Sprintf(
		"Recorded %s for resolution %d. Outcome feedback is not Evidence and never changes the decision.",
		stored.Kind, stored.ResolutionID)), out, nil
}

// decisionSummary is the compact text a model reads first. It states the
// outcome honestly: REFERENCE and needs_verification never sound like approval.
func decisionSummary(d DecisionResult) string {
	unknowns := len(d.Unknowns)
	if d.Status == resolver.StatusNeedsVerification {
		return fmt.Sprintf(
			"No direct-use candidate is proven yet; %d required requirement(s) remain unknown.",
			unknowns)
	}
	if d.Outcome == nil || d.Selected == nil {
		if *d.Outcome == model.OutcomeBuildLocally {
			return fmt.Sprintf(
				"BUILD LOCALLY: no candidate was usable; %d required requirement(s) remain unknown.",
				unknowns)
		}
		return "No decision was produced."
	}
	switch *d.Outcome {
	case model.OutcomeReference:
		return fmt.Sprintf(
			"REFERENCE selected: %s; %d behavioural requirement(s) remain unknown and contract satisfaction is not established.",
			d.Selected.SpecimenID, unknowns)
	case model.OutcomeBuildLocally:
		return fmt.Sprintf(
			"BUILD LOCALLY: no candidate was usable; %d required requirement(s) remain unknown.",
			unknowns)
	default:
		return fmt.Sprintf("%s selected: %s; %d unresolved required requirement(s).",
			strings.ToUpper(string(*d.Outcome)), d.Selected.SpecimenID, unknowns)
	}
}
