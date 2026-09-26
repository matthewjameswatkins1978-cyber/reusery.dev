package mcpserver

import (
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// This file is the MCP transport contract. It is deliberately separate from
// the HTTP DTOs in internal/api: MCP is its own transport, not a JSON copy of
// the HTTP API, and an internal struct change must not silently rewrite an
// agent-facing schema.
//
// Domain *vocabulary* is reused one-for-one (model.ReuseMode, model.Outcome,
// resolver.DecisionStatus, resolver.Disposition, policy.Action,
// policy.FeedbackReason, outcome.Kind); only shapes are mapped here.

// ------------------------------------------------------------------ catalog

// CatalogInput selects one primitive, or lists the catalogue when empty.
type CatalogInput struct {
	PrimitiveID string `json:"primitive_id,omitempty" jsonschema:"Opaque Reusery primitive id. Omit to list the known capabilities instead."`
}

// CatalogPrimitive is one line of the capability list.
type CatalogPrimitive struct {
	PrimitiveID     string   `json:"primitive_id" jsonschema:"Opaque Reusery primitive id."`
	Name            string   `json:"name" jsonschema:"Human-readable capability name."`
	Description     string   `json:"description" jsonschema:"What the capability means."`
	ContractID      string   `json:"contract_id" jsonschema:"The contract that describes this primitive."`
	ContractSummary string   `json:"contract_summary" jsonschema:"One-paragraph contract summary, empty when the contract is not stored."`
	Tags            []string `json:"tags" jsonschema:"Free-form tags; never null."`
}

// CatalogContract is a contract with its ordered requirements.
type CatalogContract struct {
	ContractID   string               `json:"contract_id" jsonschema:"Opaque Reusery contract id."`
	PrimitiveID  string               `json:"primitive_id" jsonschema:"Owning primitive id."`
	Version      string               `json:"version" jsonschema:"Contract version."`
	Summary      string               `json:"summary" jsonschema:"One-paragraph summary."`
	Requirements []CatalogRequirement `json:"requirements" jsonschema:"Requirements in authored order; never null."`
}

// CatalogRequirement is one independently testable contract claim.
type CatalogRequirement struct {
	RequirementID string `json:"requirement_id" jsonschema:"Opaque requirement id."`
	Description   string `json:"description" jsonschema:"The claim."`
	Kind          string `json:"kind" jsonschema:"Requirement kind from the intent vocabulary."`
	Required      bool   `json:"required" jsonschema:"Whether satisfaction is mandatory for direct reuse."`
}

// CatalogResult is the reusery_catalog output.
//
// List mode fills Primitives and leaves Primitive/Contract null. Detail mode
// fills Primitive and Contract and leaves Primitives empty. Arrays are never
// null; optional single objects are null when absent.
type CatalogResult struct {
	Primitives []CatalogPrimitive `json:"primitives" jsonschema:"Capability list; empty in detail mode."`
	Primitive  *CatalogPrimitive  `json:"primitive" jsonschema:"The requested primitive, or null in list mode."`
	Contract   *CatalogContract   `json:"contract" jsonschema:"The requested contract, or null in list mode."`
}

// ----------------------------------------------------------------- discover

// DiscoveredCandidate is one plausible specimen without its evidence body.
// Evidence stays behind reusery_inspect_evidence so a discovery call does not
// have to pay for every observation it produced.
type DiscoveredCandidate struct {
	SpecimenID string            `json:"specimen_id" jsonschema:"Opaque Reusery specimen id."`
	Name       string            `json:"name" jsonschema:"Human-readable specimen name."`
	ProviderID string            `json:"provider_id" jsonschema:"Provider that surfaced it."`
	ReuseModes []model.ReuseMode `json:"reuse_modes" jsonschema:"Reuse modes the specimen declares; never null."`
	SourceURL  string            `json:"source_url" jsonschema:"Attributable source URL."`
	Revision   string            `json:"revision" jsonschema:"Pinned revision when one is known."`
}

// DiscoverProviderReport is one provider's contribution, including failures.
type DiscoverProviderReport struct {
	ID             string          `json:"id" jsonschema:"Provider id."`
	Succeeded      bool            `json:"succeeded" jsonschema:"Whether the provider completed without operational failure."`
	Requests       int             `json:"requests" jsonschema:"HTTP requests made."`
	CandidateCount int             `json:"candidate_count" jsonschema:"Candidates returned."`
	Incomplete     bool            `json:"incomplete" jsonschema:"Whether results were truncated."`
	Issues         []DiscoverIssue `json:"issues" jsonschema:"Safe provider issues; never null."`
}

// DiscoverIssue is one inspectable provider problem.
type DiscoverIssue struct {
	Kind       string `json:"kind" jsonschema:"Issue kind."`
	Provider   string `json:"provider" jsonschema:"Provider that raised it."`
	Query      string `json:"query" jsonschema:"Query involved, when applicable."`
	StatusCode int    `json:"status_code" jsonschema:"Upstream status code, when one exists."`
	Message    string `json:"message" jsonschema:"Safe human-readable explanation."`
}

// DiscoverResult is the reusery_discover output.
type DiscoverResult struct {
	PrimitiveID string                   `json:"primitive_id" jsonschema:"Primitive discovered for."`
	ContractID  string                   `json:"contract_id" jsonschema:"Contract discovered for."`
	ObservedAt  time.Time                `json:"observed_at" format:"date-time" jsonschema:"Observation time for this run."`
	Candidates  []DiscoveredCandidate    `json:"candidates" jsonschema:"Discovered candidates; never null."`
	Providers   []DiscoverProviderReport `json:"providers" jsonschema:"Per-provider reports including partial failures; never null."`
}

// ------------------------------------------------------------------ enrich

// EnrichInput names the specimens to enrich.
type EnrichInput struct {
	SpecimenIDs []string `json:"specimen_ids" minItems:"1" jsonschema:"Opaque Reusery specimen ids to enrich; must be non-empty, unique and within the run budget."`
}

// EnrichProviderReport is one enrichment provider's contribution.
type EnrichProviderReport struct {
	ID            string        `json:"id" jsonschema:"Provider id."`
	Succeeded     bool          `json:"succeeded" jsonschema:"Whether the provider completed without operational failure."`
	Requests      int           `json:"requests" jsonschema:"HTTP requests made."`
	EvidenceCount int           `json:"evidence_count" jsonschema:"Observations recorded."`
	Issues        []EnrichIssue `json:"issues" jsonschema:"Safe provider issues; never null."`
}

// EnrichIssue is one inspectable enrichment problem.
type EnrichIssue struct {
	Kind       string `json:"kind" jsonschema:"Issue kind."`
	Provider   string `json:"provider" jsonschema:"Provider that raised it."`
	StatusCode int    `json:"status_code" jsonschema:"Upstream status code, when one exists."`
	Message    string `json:"message" jsonschema:"Safe human-readable explanation."`
}

// EnrichSpecimenReport summarises one specimen's enrichment.
//
// Observation bodies are deliberately not included: the agent asks
// reusery_inspect_evidence when it actually needs them.
type EnrichSpecimenReport struct {
	SpecimenID    string                 `json:"specimen_id" jsonschema:"Specimen enriched."`
	Supported     bool                   `json:"supported" jsonschema:"Whether any provider supported this specimen."`
	EvidenceCount int                    `json:"evidence_count" jsonschema:"Observations recorded for this specimen."`
	Providers     []EnrichProviderReport `json:"providers" jsonschema:"Per-provider reports; never null."`
}

// EnrichResult is the reusery_enrich output.
type EnrichResult struct {
	ObservedAt time.Time              `json:"observed_at" format:"date-time" jsonschema:"Observation time for this run."`
	Evidence   int                    `json:"evidence" jsonschema:"Total new observations recorded across all specimens."`
	Specimens  []EnrichSpecimenReport `json:"specimens" jsonschema:"Per-specimen reports; never null."`
}

// ------------------------------------------------------- resolve and refine

// Policy is the structured policy an agent may supply instead of the built-in
// baseline. Its JSON shape is the same one the HTTP API accepts, so a policy
// written for one surface works on the other.
//
// Omitted action fields default to review, exactly as an omitted YAML action
// does: an incomplete profile can never silently allow something.
type Policy struct {
	SchemaVersion int             `json:"schema_version" required:"true" example:"1" jsonschema:"Policy schema version; must be 1."`
	ID            string          `json:"id" minLength:"1" example:"public-go-baseline/v1" jsonschema:"Policy profile identifier recorded on the resolution."`
	Reuse         PolicyReuse     `json:"reuse" jsonschema:"Reuse mode rules."`
	Licence       PolicyLicence   `json:"license" jsonschema:"Licence rules."`
	Security      PolicySecurity  `json:"security" jsonschema:"Advisory rules."`
	Dependencies  PolicyDeps      `json:"dependencies" jsonschema:"Dependency burden rules."`
	Maintenance   PolicyMaint     `json:"maintenance" jsonschema:"Maintenance rules."`
	Source        PolicySource    `json:"source" jsonschema:"Provenance rules."`
	Selection     PolicySelection `json:"selection" jsonschema:"Selection bounds."`
}

// PolicyReuse lists permitted and preferred reuse modes.
type PolicyReuse struct {
	Allowed   []model.ReuseMode `json:"allowed" jsonschema:"Reuse modes the policy permits; never null."`
	Preferred []model.ReuseMode `json:"preferred" jsonschema:"Permitted modes in preference order; never null."`
}

// PolicyLicence holds licence allow and deny lists.
type PolicyLicence struct {
	Allow    []string      `json:"allow" jsonschema:"Exact licence expressions that are allowed; never null."`
	Deny     []string      `json:"deny" jsonschema:"Exact licence expressions that are denied; never null."`
	Unknown  policy.Action `json:"unknown" jsonschema:"Action when no licence is established."`
	Multiple policy.Action `json:"multiple" jsonschema:"Action when several licence expressions are observed."`
	Unlisted policy.Action `json:"unlisted" jsonschema:"Action when the licence is known but not listed."`
}

// PolicySecurity holds advisory rules.
type PolicySecurity struct {
	KnownAdvisory policy.Action `json:"known_advisory" jsonschema:"Action when a known advisory identifier was reported."`
	Unknown       policy.Action `json:"unknown" jsonschema:"Action when the advisory picture is unknown."`
}

// PolicyDeps holds dependency burden rules.
type PolicyDeps struct {
	Unknown   policy.Action `json:"unknown" jsonschema:"Action when the dependency count is unknown."`
	MaxDirect *int          `json:"max_direct,omitempty" minimum:"0" jsonschema:"Maximum permitted direct dependencies. Omit for no quantity rule."`
}

// PolicyMaint holds maintenance rules.
type PolicyMaint struct {
	Archived            policy.Action `json:"archived" jsonschema:"Action when the source repository is archived."`
	Deprecated          policy.Action `json:"deprecated" jsonschema:"Action when the package is deprecated."`
	Stale               policy.Action `json:"stale" jsonschema:"Action when the last push exceeds the configured window."`
	Unknown             policy.Action `json:"unknown" jsonschema:"Action when a configured maintenance fact is unknown."`
	MaxDaysSincePush    *int          `json:"max_days_since_push,omitempty" minimum:"0" jsonschema:"Staleness window in days. Omit to disable the rule."`
	MaxDaysSinceRelease *int          `json:"max_days_since_release,omitempty" minimum:"0" jsonschema:"Release age window in days. Omit to disable the rule."`
}

// PolicySource holds provenance rules.
type PolicySource struct {
	RequireRevisionFor []model.ReuseMode `json:"require_revision_for" jsonschema:"Reuse modes that require a pinned revision; never null."`
	MissingRevision    policy.Action     `json:"missing_revision" jsonschema:"Action when a required revision is missing."`
}

// PolicySelection holds selection bounds.
type PolicySelection struct {
	MaxOptions int `json:"max_options" minimum:"1" maximum:"5" jsonschema:"Maximum number of candidates in the shortlist."`
}

// CandidateRef names one candidate and the reuse mode under consideration.
type CandidateRef struct {
	SpecimenID string          `json:"specimen_id" minLength:"1" jsonschema:"Opaque Reusery specimen id."`
	ReuseMode  model.ReuseMode `json:"reuse_mode" jsonschema:"Reuse mode under consideration: copy, dependency, adapt or reference."`
}

// ResolveInput is the reusery_resolve request.
type ResolveInput struct {
	PrimitiveID string         `json:"primitive_id" minLength:"1" jsonschema:"Primitive being resolved."`
	ContractID  string         `json:"contract_id" minLength:"1" jsonschema:"Contract being resolved."`
	Candidates  []CandidateRef `json:"candidates" jsonschema:"Candidates to consider; send [] when there are none."`
	Policy      *Policy        `json:"policy,omitempty" jsonschema:"Structured policy. Omit to use the built-in public-go-baseline/v1."`
	ProjectID   string         `json:"project_id,omitempty" jsonschema:"Optional project identity. When supplied the decision is project-aware; when omitted Packet 9 semantics apply exactly."`
}

// RefineInput is the reusery_refine request. Refinement is stateless: the
// caller resends the original base policy, the same bounded candidate set and
// the complete accumulated feedback history.
type RefineInput struct {
	PrimitiveID string            `json:"primitive_id" minLength:"1" jsonschema:"Primitive being resolved."`
	ContractID  string            `json:"contract_id" minLength:"1" jsonschema:"Contract being resolved."`
	Candidates  []CandidateRef    `json:"candidates" jsonschema:"The bounded candidate set; send [] when there are none."`
	Policy      *Policy           `json:"policy,omitempty" jsonschema:"The ORIGINAL base policy, not a previously derived effective policy. Omit for the built-in baseline."`
	ProjectID   string            `json:"project_id,omitempty" jsonschema:"Optional project identity. When supplied the decision is project-aware; when omitted Packet 9 semantics apply exactly."`
	Feedback    []policy.Feedback `json:"feedback" jsonschema:"Complete accumulated feedback history using the supported vocabulary: not_quite, too_many_dependencies, licence_not_allowed, avoid_dependency, avoid_reference, archived_project. At least one item."`
}

// SelectedCandidate is the compact view of the chosen candidate.
type SelectedCandidate struct {
	SpecimenID  string               `json:"specimen_id" jsonschema:"Opaque Reusery specimen id."`
	ReuseMode   model.ReuseMode      `json:"reuse_mode" jsonschema:"Reuse mode selected."`
	Disposition resolver.Disposition `json:"disposition" jsonschema:"Why this candidate could be used."`
	SourceURL   string               `json:"source_url" jsonschema:"Attributable source URL."`
	Revision    string               `json:"revision" jsonschema:"Pinned revision when one is known."`
	License     string               `json:"license" jsonschema:"Declared licence when the source declares one."`
}

// ShortlistEntry is one ranked alternative with what is still missing.
type ShortlistEntry struct {
	SpecimenID          string               `json:"specimen_id" jsonschema:"Opaque Reusery specimen id."`
	ReuseMode           model.ReuseMode      `json:"reuse_mode" jsonschema:"Reuse mode considered."`
	Disposition         resolver.Disposition `json:"disposition" jsonschema:"Why this candidate could or could not be used."`
	UnknownRequirements []string             `json:"unknown_requirements" jsonschema:"Required requirement ids still behaviourally unknown; never null."`
}

// RejectedEntry preserves negative knowledge about one candidate.
type RejectedEntry struct {
	SpecimenID string   `json:"specimen_id" jsonschema:"Rejected candidate."`
	Reasons    []string `json:"reasons" jsonschema:"Why it was rejected; never null."`
}

// DecisionResult is the compact agent decision shared by resolve and refine.
//
// It is deliberately not Packet 7's whole assessment graph: an agent needs
// enough to steer, not every Evidence record.
type DecisionResult struct {
	Status       resolver.DecisionStatus `json:"status" jsonschema:"resolved or needs_verification. Both are successful tool results."`
	PolicyID     string                  `json:"policy_id" jsonschema:"Policy profile identifier."`
	ResolutionID *int64                  `json:"resolution_id" jsonschema:"Persisted resolution identity, or null when nothing was persisted."`
	Outcome      *model.Outcome          `json:"outcome" jsonschema:"reuse, adapt, depend, reference or build_locally; null when nothing was persisted."`
	Selected     *SelectedCandidate      `json:"selected" jsonschema:"Selected candidate, or null."`
	Shortlist    []ShortlistEntry        `json:"shortlist" jsonschema:"Ranked alternatives; never null."`
	Unknowns     []string                `json:"unknowns" jsonschema:"Required requirement ids still unknown across the assessed candidates; never null."`
	Reasons      []string                `json:"reasons" jsonschema:"Deterministic reasons for the decision; empty when nothing was persisted."`
	Rejected     []RejectedEntry         `json:"rejected" jsonschema:"Negative knowledge preserved on the resolution; never null."`
	// ProjectID, ProjectContextHash and ProjectEffects are present only for a
	// project-aware decision. Without project_id the output is exactly the
	// Packet 9 output.
	ProjectID          string                  `json:"project_id,omitempty" jsonschema:"Project whose context applied, or absent."`
	ProjectContextHash string                  `json:"project_context_hash,omitempty" jsonschema:"Immutable context snapshot the decision saw."`
	ProjectEffects     []project.ProjectEffect `json:"project_effects,omitempty" jsonschema:"Per-candidate project dependency fit; absent without project context."`
}

// RefineResult is the reusery_refine output: the shared decision plus what the
// feedback actually changed.
type RefineResult struct {
	DecisionResult
	AppliedFeedback []policy.AppliedFeedback `json:"applied_feedback" jsonschema:"Feedback the server actually applied; never null."`
	EffectivePolicy policy.EffectiveSummary  `json:"effective_policy" jsonschema:"Policy actually applied, after refinement."`
}

// ----------------------------------------------------------------- inspect

// EvidenceCursor is the native continuation key for evidence paging. It is
// deliberately not the HTTP cursor encoding: MCP is its own transport.
type EvidenceCursor struct {
	ObservedAt *time.Time `json:"observed_at,omitempty" format:"date-time" jsonschema:"observed_at of the last returned record."`
	EvidenceID string     `json:"evidence_id,omitempty" jsonschema:"id of the last returned record."`
}

// EvidenceInput selects a bounded page of evidence for one subject.
type EvidenceInput struct {
	SubjectID string          `json:"subject_id" minLength:"1" jsonschema:"Opaque Reusery specimen id whose evidence is wanted."`
	Limit     int             `json:"limit,omitempty" minimum:"1" maximum:"50" jsonschema:"Page size; defaults to 20 and may not exceed 50."`
	After     *EvidenceCursor `json:"after,omitempty" jsonschema:"Continuation key from a previous page; omit for the first page."`
}

// EvidenceSource is the attributable source of one observation.
type EvidenceSource struct {
	URL      string `json:"url" jsonschema:"Attributable source URL."`
	Revision string `json:"revision" jsonschema:"Pinned revision when one is known."`
	Path     string `json:"path" jsonschema:"Path within the source when applicable."`
	License  string `json:"license" jsonschema:"Declared licence when the source declares one."`
}

// EvidenceRecord is one bounded observation. Artifact is omitted: it carries
// machine-internal fact JSON that the human-readable claim already states.
type EvidenceRecord struct {
	ID          string               `json:"id" jsonschema:"Opaque evidence id."`
	Kind        string               `json:"kind" jsonschema:"Observation kind."`
	Claim       string               `json:"claim" jsonschema:"The observed claim, unabridged."`
	Result      model.EvidenceResult `json:"result" jsonschema:"pass, fail, unknown or info."`
	Source      EvidenceSource       `json:"source" jsonschema:"Attributable source."`
	ObservedAt  time.Time            `json:"observed_at" format:"date-time" jsonschema:"When the observation was recorded."`
	AppliesTo   string               `json:"applies_to" jsonschema:"Requirement id this observation applies to; empty for non-behavioural evidence."`
	Methodology string               `json:"methodology" jsonschema:"How the observation was obtained."`
}

// EvidenceResult is the reusery_inspect_evidence output.
type EvidenceResultPage struct {
	SubjectID string           `json:"subject_id" jsonschema:"Subject the page belongs to."`
	Evidence  []EvidenceRecord `json:"evidence" jsonschema:"Observations ordered by observed_at then id; never null."`
	Limit     int              `json:"limit" jsonschema:"Page size used."`
	NextAfter *EvidenceCursor  `json:"next_after" jsonschema:"Continuation key for the next page, or null when the page is the last one."`
}

// StoredResolution is a remembered decision, returned exactly as recorded.
type StoredResolution struct {
	PrimitiveID string            `json:"primitive_id" jsonschema:"Primitive resolved."`
	ContractID  string            `json:"contract_id" jsonschema:"Contract resolved."`
	Outcome     model.Outcome     `json:"outcome" jsonschema:"reuse, adapt, depend, reference or build_locally."`
	SpecimenID  string            `json:"specimen_id" jsonschema:"Selected specimen, empty for build_locally."`
	Reasons     []string          `json:"reasons" jsonschema:"Deterministic reasons; never null."`
	Rejected    []StoredRejection `json:"rejected" jsonschema:"Negative knowledge preserved; never null."`
	Unknowns    []string          `json:"unknowns" jsonschema:"Unresolved required requirements; never null."`
	EvidenceIDs []string          `json:"evidence_ids" jsonschema:"Evidence consulted; never null."`
	PolicyID    string            `json:"policy_id" jsonschema:"Policy profile that justified the decision."`
	ResolvedAt  time.Time         `json:"resolved_at" format:"date-time" jsonschema:"When the decision was recorded."`
}

// StoredRejection preserves why one candidate was not chosen.
type StoredRejection struct {
	SpecimenID string   `json:"specimen_id" jsonschema:"Rejected candidate."`
	Reasons    []string `json:"reasons" jsonschema:"Why it was rejected; never null."`
}

// OutcomeEvent is one factual post-resolution event.
type OutcomeEvent struct {
	ID           int64        `json:"id" jsonschema:"Storage identity of this event."`
	ResolutionID int64        `json:"resolution_id" jsonschema:"Resolution this event reports on."`
	Kind         outcome.Kind `json:"kind" jsonschema:"adopted, rejected, integration_succeeded, integration_failed or abandoned."`
	Note         string       `json:"note" jsonschema:"Optional caller-supplied note; never logged."`
	RecordedAt   time.Time    `json:"recorded_at" format:"date-time" jsonschema:"When the event was recorded."`
}

// ResolutionResult is the reusery_inspect_resolution output: remembered
// history plus the outcome events recorded against it.
type ResolutionResult struct {
	ResolutionID    int64            `json:"resolution_id" jsonschema:"Storage identity of the resolution."`
	Resolution      StoredResolution `json:"resolution" jsonschema:"The remembered decision, not re-evaluated."`
	OutcomeFeedback []OutcomeEvent   `json:"outcome_feedback" jsonschema:"Post-resolution events in chronological order; never null."`
}

// ------------------------------------------------------------------ outcome

// InspectResolutionInput selects one remembered resolution.
type InspectResolutionInput struct {
	ResolutionID int64 `json:"resolution_id" minimum:"1" jsonschema:"PostgreSQL storage identity of the resolution."`
}

// OutcomeInput records what actually happened after a Resolution was used.
type OutcomeInput struct {
	ResolutionID int64        `json:"resolution_id" minimum:"1" jsonschema:"Storage identity of the resolution being reported on."`
	Kind         outcome.Kind `json:"kind" jsonschema:"adopted, rejected, integration_succeeded, integration_failed or abandoned."`
	Note         string       `json:"note,omitempty" maxLength:"1000" jsonschema:"Optional note of at most 1000 characters; never logged."`
}

// OutcomeResult acknowledges one recorded event.
type OutcomeResult struct {
	ID           int64        `json:"id" jsonschema:"Storage identity of the recorded event."`
	ResolutionID int64        `json:"resolution_id" jsonschema:"Resolution the event reports on."`
	Kind         outcome.Kind `json:"kind" jsonschema:"Recorded kind."`
	Note         string       `json:"note" jsonschema:"Echo of the supplied note."`
	RecordedAt   time.Time    `json:"recorded_at" format:"date-time" jsonschema:"When the event was recorded."`
}
