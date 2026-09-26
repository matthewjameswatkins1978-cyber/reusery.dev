package api

import (
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// This file declares every API v1 transport type. They are deliberately
// distinct from the internal domain structs: an innocent internal refactor in
// a later packet must not silently change a frozen public contract.

// FactStatusValue is how a machine-readable fact was established. It maps
// one-for-one onto the internal fact status vocabulary.
type FactStatusValue = policy.FactStatus

// ---------------------------------------------------------------- primitives

// Primitive is the stored capability record.
type Primitive struct {
	ID          string   `json:"id" example:"process/bounded-subprocess" doc:"Opaque Reusery primitive id."`
	Name        string   `json:"name" doc:"Human-readable capability name."`
	Description string   `json:"description" doc:"What the capability means."`
	Tags        []string `json:"tags" doc:"Free-form tags; never null."`
	ContractID  string   `json:"contract_id" doc:"The contract this primitive is described by."`
}

// Requirement is one independently testable contract claim.
type Requirement struct {
	ID          string `json:"id" example:"req-001" doc:"Opaque requirement id."`
	Description string `json:"description" doc:"The claim."`
	Kind        string `json:"kind" example:"behavior" doc:"Requirement kind from the intent vocabulary."`
	Required    bool   `json:"required" doc:"Whether satisfaction is mandatory for direct reuse."`
}

// Contract describes observable behaviour and invariants for a primitive.
type Contract struct {
	ID           string        `json:"id" doc:"Opaque Reusery contract id."`
	PrimitiveID  string        `json:"primitive_id" doc:"The owning primitive id."`
	Version      string        `json:"version" doc:"Contract version."`
	Summary      string        `json:"summary" doc:"One-paragraph summary."`
	Requirements []Requirement `json:"requirements" doc:"Ordered requirements; never null."`
}

// SourceRef pins evidence or code to an attributable source.
type SourceRef struct {
	URL      string `json:"url" doc:"Attributable source URL."`
	Revision string `json:"revision" doc:"Pinned revision when one is known."`
	Path     string `json:"path" doc:"Path within the source when applicable."`
	License  string `json:"license" doc:"Declared licence when the source declares one."`
}

// Specimen is a concrete implementation that may satisfy a contract.
type Specimen struct {
	ID          string            `json:"id" doc:"Opaque Reusery specimen id."`
	PrimitiveID string            `json:"primitive_id" doc:"The primitive this specimen was discovered for."`
	Name        string            `json:"name" doc:"Human-readable specimen name."`
	Source      SourceRef         `json:"source" doc:"Attributable source."`
	ReuseModes  []model.ReuseMode `json:"reuse_modes" enum:"copy,dependency,adapt,reference" doc:"Reuse modes the specimen declares; never null."`
}

// Evidence is one attributable observation. It is never a score.
type Evidence struct {
	ID          string               `json:"id" doc:"Opaque evidence id."`
	SubjectID   string               `json:"subject_id" doc:"The specimen this observation is about."`
	Kind        string               `json:"kind" doc:"Observation kind."`
	Claim       string               `json:"claim" doc:"The observed claim."`
	Result      model.EvidenceResult `json:"result" enum:"pass,fail,unknown,info" doc:"Observation result. Discovery and enrichment only ever emit info or unknown."`
	Source      SourceRef            `json:"source" doc:"Attributable source."`
	ObservedAt  time.Time            `json:"observed_at" format:"date-time" doc:"When the observation was recorded."`
	AppliesTo   string               `json:"applies_to" doc:"Requirement id this observation applies to; empty for non-behavioural evidence."`
	Methodology string               `json:"methodology" doc:"How the observation was obtained."`
	Artifact    string               `json:"artifact" doc:"Machine-readable artifact backing the claim."`
}

// ------------------------------------------------------------------ intents

// NormalizeRequest is the POST /v1/normalize body.
type NormalizeRequest struct {
	Input string `json:"input" minLength:"1" maxLength:"8192" example:"I need a Go component that runs child processes with timeouts" doc:"Ordinary engineering intent in natural language."`
}

// IntentUsage reports model token consumption for one normalisation.
type IntentUsage struct {
	InputTokens     int `json:"input_tokens" doc:"Prompt tokens consumed."`
	OutputTokens    int `json:"output_tokens" doc:"Completion tokens consumed."`
	ReasoningTokens int `json:"reasoning_tokens" doc:"Reasoning tokens consumed."`
	TotalTokens     int `json:"total_tokens" doc:"Total tokens consumed."`
}

// IntentGenerationMetadata records how the result was produced.
type IntentGenerationMetadata struct {
	Provider      string      `json:"provider" doc:"Model provider id."`
	Model         string      `json:"model" doc:"Model identifier."`
	ResponseID    string      `json:"response_id" doc:"Provider response identifier."`
	PromptVersion string      `json:"prompt_version" doc:"Prompt version."`
	SchemaVersion int         `json:"schema_version" doc:"Normalisation schema version."`
	Calls         int         `json:"calls" doc:"Model calls used, including any repair."`
	Repaired      bool        `json:"repaired" doc:"Whether a repair pass ran."`
	Usage         IntentUsage `json:"usage" doc:"Token usage."`
}

// IntentRequirement is a provisional behavioural requirement.
type IntentRequirement struct {
	Description string `json:"description" doc:"The claim."`
	Kind        string `json:"kind" example:"behavior" doc:"Requirement kind."`
	Required    bool   `json:"required" doc:"Whether the claim is mandatory."`
}

// IntentConstraint is a non-behavioural constraint.
type IntentConstraint struct {
	Kind        string `json:"kind" example:"language" doc:"Constraint kind."`
	Description string `json:"description" doc:"The constraint."`
	Required    bool   `json:"required" doc:"Whether the constraint is mandatory."`
}

// IntentAmbiguity records a material ambiguity and why it matters.
type IntentAmbiguity struct {
	Question     string `json:"question" doc:"The clarifying question."`
	WhyItMatters string `json:"why_it_matters" doc:"How the answer changes the required behaviour."`
}

// IntentResult is the POST /v1/normalize response.
//
// ready, needs_clarification and unsupported are all valid product results and
// all return HTTP 200. Only provider or configuration failure is an error.
type IntentResult struct {
	Input                  string                   `json:"input" doc:"Echo of the original input."`
	Status                 intent.Status            `json:"status" enum:"ready,needs_clarification,unsupported" doc:"Normalisation status."`
	RequestedArtifactLevel intent.ArtifactLevel     `json:"requested_artifact_level" enum:"unspecified,code,package,library,framework,cross_level" doc:"Requested artifact level."`
	Capability             string                   `json:"capability" doc:"Structured capability statement."`
	Summary                string                   `json:"summary" doc:"Plain-language summary."`
	Primitive              *Primitive               `json:"primitive" doc:"Provisional primitive, or null."`
	Contract               *Contract                `json:"contract" doc:"Provisional contract, or null."`
	Constraints            []IntentConstraint       `json:"constraints" doc:"Constraints; never null."`
	Ambiguities            []IntentAmbiguity        `json:"ambiguities" doc:"Material ambiguities; never null."`
	Assumptions            []string                 `json:"assumptions" doc:"Assumptions made; never null."`
	UnsupportedReason      string                   `json:"unsupported_reason" doc:"Why the input was unsupported, when status is unsupported."`
	Metadata               IntentGenerationMetadata `json:"metadata" doc:"How this result was produced."`
}

// --------------------------------------------------------------- discovery

// DiscoveryQuery is one provider search request.
type DiscoveryQuery struct {
	Text  string `json:"text" minLength:"1" maxLength:"200" example:"subprocess runner cancellation" doc:"Search text."`
	Limit int    `json:"limit" minimum:"1" maximum:"6" example:"5" doc:"Maximum results requested from this query."`
}

// DiscoveryProviderPlan groups the queries one provider runs.
type DiscoveryProviderPlan struct {
	ID      string           `json:"id" enum:"pkg.go.dev,github-repositories,github-code" doc:"Provider id."`
	Queries []DiscoveryQuery `json:"queries" minItems:"1" maxItems:"3" doc:"Queries for this provider; never null."`
}

// DiscoveryProfile is the POST /v1/discover body: the structured Packet 5
// discovery profile supplied directly as JSON. The API never accepts a
// filesystem path or a profile filename.
type DiscoveryProfile struct {
	SchemaVersion int                     `json:"schema_version" required:"true" const:"1" example:"1" doc:"Discovery profile schema version; must be 1."`
	PrimitiveID   string                  `json:"primitive_id" minLength:"1" doc:"Primitive being discovered for."`
	ContractID    string                  `json:"contract_id" minLength:"1" doc:"Contract being discovered for."`
	Providers     []DiscoveryProviderPlan `json:"providers" minItems:"1" maxItems:"3" doc:"Bounded provider plans; never null."`
}

// DiscoveryIssue is one safe, inspectable provider problem.
type DiscoveryIssue struct {
	Kind       discovery.IssueKind `json:"kind" doc:"Issue kind."`
	Provider   string              `json:"provider" doc:"Provider that raised it."`
	Query      string              `json:"query" doc:"Query involved, when applicable."`
	StatusCode int                 `json:"status_code" doc:"Upstream status code, when one exists."`
	RetryAfter string              `json:"retry_after" doc:"Upstream Retry-After value, when one exists."`
	Message    string              `json:"message" doc:"Safe human-readable explanation."`
}

// DiscoveryProviderReport summarises one provider's contribution.
type DiscoveryProviderReport struct {
	ID             string           `json:"id" doc:"Provider id."`
	Succeeded      bool             `json:"succeeded" doc:"Whether the provider completed without operational failure."`
	Requests       int              `json:"requests" doc:"HTTP requests made."`
	CandidateCount int              `json:"candidate_count" doc:"Candidates returned."`
	Incomplete     bool             `json:"incomplete" doc:"Whether results were truncated."`
	Issues         []DiscoveryIssue `json:"issues" doc:"Issues raised; never null."`
}

// DiscoveryCandidate is one plausible specimen with its attributable evidence.
type DiscoveryCandidate struct {
	ProviderID string     `json:"provider_id" doc:"Provider that found it."`
	Specimen   Specimen   `json:"specimen" doc:"The discovered specimen."`
	Evidence   []Evidence `json:"evidence" doc:"Discovery observations; never null."`
}

// DiscoveryResult is the POST /v1/discover response.
//
// Discovery records INFO/UNKNOWN observations only. It never verifies a
// candidate and never resolves.
type DiscoveryResult struct {
	PrimitiveID string                    `json:"primitive_id" doc:"Primitive discovered for."`
	ContractID  string                    `json:"contract_id" doc:"Contract discovered for."`
	ObservedAt  time.Time                 `json:"observed_at" format:"date-time" doc:"Observation time for this run."`
	Candidates  []DiscoveryCandidate      `json:"candidates" doc:"Discovered candidates; never null."`
	Providers   []DiscoveryProviderReport `json:"providers" doc:"Per-provider reports, including partial failures; never null."`
}

// -------------------------------------------------------------- enrichment

// EnrichRequest is the POST /v1/enrich body.
type EnrichRequest struct {
	SpecimenIDs []string `json:"specimen_ids" minItems:"1" maxItems:"24" doc:"Specimens to enrich; must be non-empty, unique and within the run budget."`
}

// EnrichIssue is one safe, inspectable enrichment problem.
type EnrichIssue struct {
	Kind       enrichment.IssueKind `json:"kind" doc:"Issue kind."`
	Provider   string               `json:"provider" doc:"Provider that raised it."`
	SpecimenID string               `json:"specimen_id" doc:"Specimen involved, when applicable."`
	StatusCode int                  `json:"status_code" doc:"Upstream status code, when one exists."`
	RetryAfter string               `json:"retry_after" doc:"Upstream Retry-After value, when one exists."`
	Message    string               `json:"message" doc:"Safe human-readable explanation."`
}

// EnrichProviderReport summarises one enrichment provider's contribution.
type EnrichProviderReport struct {
	ID            string        `json:"id" doc:"Provider id."`
	Succeeded     bool          `json:"succeeded" doc:"Whether the provider completed without operational failure."`
	Requests      int           `json:"requests" doc:"HTTP requests made."`
	EvidenceCount int           `json:"evidence_count" doc:"Observations recorded."`
	Issues        []EnrichIssue `json:"issues" doc:"Issues raised; never null."`
}

// SpecimenEnrichReport summarises one specimen's enrichment.
type SpecimenEnrichReport struct {
	SpecimenID    string                 `json:"specimen_id" doc:"Specimen enriched."`
	Supported     bool                   `json:"supported" doc:"Whether any provider supported this specimen."`
	EvidenceCount int                    `json:"evidence_count" doc:"Observations recorded for this specimen."`
	Providers     []EnrichProviderReport `json:"providers" doc:"Per-provider reports; never null."`
}

// EnrichResult is the POST /v1/enrich response.
//
// Enrichment records INFO/UNKNOWN observations under Packet 7 trust rules. It
// never creates behavioural PASS or FAIL evidence and never resolves.
type EnrichResult struct {
	ObservedAt time.Time              `json:"observed_at" format:"date-time" doc:"Observation time for this run."`
	Evidence   []Evidence             `json:"evidence" doc:"New observations; never null."`
	Specimens  []SpecimenEnrichReport `json:"specimens" doc:"Per-specimen reports; never null."`
}

// ------------------------------------------------------------ policy input

// Policy is the stable API v1 structured policy profile.
//
// It maps deterministically onto the internal policy profile. Omitted action
// fields default to review, exactly as an omitted YAML action does, so an
// incomplete profile can never silently allow something.
type Policy struct {
	SchemaVersion int             `json:"schema_version" required:"true" const:"1" example:"1" doc:"Policy schema version; must be 1."`
	ID            string          `json:"id" minLength:"1" example:"public-go-baseline/v1" doc:"Policy profile identifier recorded on the resolution."`
	Reuse         PolicyReuse     `json:"reuse" doc:"Reuse mode rules."`
	Licence       PolicyLicence   `json:"license" doc:"Licence rules."`
	Security      PolicySecurity  `json:"security" doc:"Advisory rules."`
	Dependencies  PolicyDeps      `json:"dependencies" doc:"Dependency burden rules."`
	Maintenance   PolicyMaint     `json:"maintenance" doc:"Maintenance rules."`
	Source        PolicySource    `json:"source" doc:"Provenance rules."`
	Selection     PolicySelection `json:"selection" doc:"Selection bounds."`
}

// PolicyReuse lists permitted and preferred reuse modes.
type PolicyReuse struct {
	Allowed   []model.ReuseMode `json:"allowed" enum:"copy,dependency,adapt,reference" doc:"Reuse modes the policy permits; never null."`
	Preferred []model.ReuseMode `json:"preferred" enum:"copy,dependency,adapt,reference" doc:"Permitted modes in preference order; never null."`
}

// PolicyLicence holds licence allow and deny lists.
type PolicyLicence struct {
	Allow    []string      `json:"allow" doc:"Exact licence expressions that are allowed; never null."`
	Deny     []string      `json:"deny" doc:"Exact licence expressions that are denied; never null."`
	Unknown  policy.Action `json:"unknown" enum:"allow,review,deny" doc:"Action when no licence is established."`
	Multiple policy.Action `json:"multiple" enum:"allow,review,deny" doc:"Action when several licence expressions are observed."`
	Unlisted policy.Action `json:"unlisted" enum:"allow,review,deny" doc:"Action when the licence is known but not listed."`
}

// PolicySecurity holds advisory rules.
type PolicySecurity struct {
	KnownAdvisory policy.Action `json:"known_advisory" enum:"allow,review,deny" doc:"Action when a known advisory identifier was reported."`
	Unknown       policy.Action `json:"unknown" enum:"allow,review,deny" doc:"Action when the advisory picture is unknown."`
}

// PolicyDeps holds dependency burden rules.
type PolicyDeps struct {
	Unknown   policy.Action `json:"unknown" enum:"allow,review,deny" doc:"Action when the dependency count is unknown."`
	MaxDirect *int          `json:"max_direct,omitempty" nullable:"true" minimum:"0" example:"7" doc:"Maximum permitted direct dependencies. Omit or send null for no quantity rule."`
}

// PolicyMaint holds maintenance rules.
type PolicyMaint struct {
	Archived            policy.Action `json:"archived" enum:"allow,review,deny" doc:"Action when the source repository is archived."`
	Deprecated          policy.Action `json:"deprecated" enum:"allow,review,deny" doc:"Action when the package is deprecated."`
	Stale               policy.Action `json:"stale" enum:"allow,review,deny" doc:"Action when the last push exceeds the configured window."`
	Unknown             policy.Action `json:"unknown" enum:"allow,review,deny" doc:"Action when a configured maintenance fact is unknown."`
	MaxDaysSincePush    *int          `json:"max_days_since_push,omitempty" nullable:"true" minimum:"0" doc:"Staleness window in days. Omit or send null to disable the rule."`
	MaxDaysSinceRelease *int          `json:"max_days_since_release,omitempty" nullable:"true" minimum:"0" doc:"Release age window in days. Omit or send null to disable the rule."`
}

// PolicySource holds provenance rules.
type PolicySource struct {
	RequireRevisionFor []model.ReuseMode `json:"require_revision_for" enum:"copy,dependency,adapt,reference" doc:"Reuse modes that require a pinned revision; never null."`
	MissingRevision    policy.Action     `json:"missing_revision" enum:"allow,review,deny" doc:"Action when a required revision is missing."`
}

// PolicySelection holds selection bounds.
type PolicySelection struct {
	MaxOptions int `json:"max_options" minimum:"1" maximum:"5" doc:"Maximum number of candidates in the shortlist."`
}

// Feedback is one structured "not quite" item.
type Feedback struct {
	CandidateID string                `json:"candidate_id" minLength:"1" doc:"Candidate the feedback applies to."`
	Reason      policy.FeedbackReason `json:"reason" enum:"not_quite,too_many_dependencies,licence_not_allowed,avoid_dependency,avoid_reference,archived_project" doc:"Structured feedback reason."`
}

// ------------------------------------------------------------ resolve/refine

// CandidateRef names one candidate and the reuse mode under consideration.
type CandidateRef struct {
	SpecimenID string          `json:"specimen_id" minLength:"1" doc:"Opaque Reusery specimen id."`
	ReuseMode  model.ReuseMode `json:"reuse_mode" enum:"copy,dependency,adapt,reference" doc:"Reuse mode under consideration."`
}

// ResolveRequest is the POST /v1/resolve body.
//
// The policy is supplied as structured JSON: API clients never submit a
// filesystem policy path. Initial resolve requests carry no feedback.
type ResolveRequest struct {
	PrimitiveID string         `json:"primitive_id" minLength:"1" doc:"Primitive being resolved."`
	ContractID  string         `json:"contract_id" minLength:"1" doc:"Contract being resolved."`
	Candidates  []CandidateRef `json:"candidates" doc:"Candidates to consider; never null."`
	Policy      Policy         `json:"policy" doc:"Structured policy profile to evaluate under."`
}

// RefineRequest is the POST /v1/refine body.
//
// Refinement is stateless: the caller resends the same base policy, the same
// bounded candidate set and the complete accumulated feedback history. The
// server derives the effective policy from scratch each time, so feedback is
// never applied twice.
type RefineRequest struct {
	PrimitiveID string         `json:"primitive_id" minLength:"1" doc:"Primitive being resolved."`
	ContractID  string         `json:"contract_id" minLength:"1" doc:"Contract being resolved."`
	Candidates  []CandidateRef `json:"candidates" doc:"The bounded candidate set; never null."`
	Policy      Policy         `json:"policy" doc:"The original base policy, not a previously derived effective policy."`
	Feedback    []Feedback     `json:"feedback" minItems:"1" doc:"Complete accumulated feedback history; at least one item."`
}

// PolicyDecision is one dimension's deterministic verdict.
type PolicyDecision struct {
	Dimension   string        `json:"dimension" doc:"Evaluated dimension."`
	Action      policy.Action `json:"action" enum:"allow,review,deny" doc:"Verdict."`
	Reason      string        `json:"reason" doc:"Deterministic explanation."`
	EvidenceIDs []string      `json:"evidence_ids" doc:"Evidence backing the verdict; never null."`
}

// LicenceFact is the observed licence picture for a candidate.
type LicenceFact struct {
	Status              FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Values              []string        `json:"values" doc:"Exact licence expressions observed; never null."`
	RelationshipUnknown bool            `json:"relationship_unknown" doc:"True when several expressions were observed and their relationship is not established."`
	EvidenceIDs         []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// AdvisoryFact is the observed advisory picture for a candidate. A reported
// zero is a fact: it never becomes a behavioural pass.
type AdvisoryFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	KnownIDs    []string        `json:"known_ids" doc:"Known advisory identifiers; never null."`
	Count       int             `json:"count" doc:"Reported advisory count."`
	CountKnown  bool            `json:"count_known" doc:"Whether a count was actually reported."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// DependencyFact is the observed dependency picture for a candidate. Counts
// are conservative: disagreement keeps the larger count.
type DependencyFact struct {
	Status             FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Direct             []string        `json:"direct" doc:"Direct dependencies observed; never null."`
	Indirect           []string        `json:"indirect" doc:"Indirect dependencies observed; never null."`
	DirectCount        int             `json:"direct_count" doc:"Conservative direct dependency count."`
	DirectCountKnown   bool            `json:"direct_count_known" doc:"Whether the direct count is established."`
	IndirectCount      int             `json:"indirect_count" doc:"Conservative indirect dependency count."`
	IndirectCountKnown bool            `json:"indirect_count_known" doc:"Whether the indirect count is established."`
	EvidenceIDs        []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// ArchivedFact records whether the source repository is archived.
type ArchivedFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Archived    bool            `json:"archived" doc:"Whether the repository is archived."`
	Known       bool            `json:"known" doc:"Whether the archive state was observed."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// LastPushFact records the attributable last-push observation. Conflicting
// observations keep the oldest push, never the newest.
type LastPushFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	PushedAt    *time.Time      `json:"pushed_at" format:"date-time" doc:"Observed last push, or null when unknown."`
	Known       bool            `json:"known" doc:"Whether a last push was observed."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// DeprecatedFact records whether the package is deprecated.
type DeprecatedFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Deprecated  bool            `json:"deprecated" doc:"Whether the package is deprecated."`
	Reason      string          `json:"reason" doc:"Deprecation reason when one was reported."`
	Known       bool            `json:"known" doc:"Whether deprecation state was observed."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// RevisionFact records the observed source revision.
type RevisionFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Revision    string          `json:"revision" doc:"Observed revision, or empty."`
	Known       bool            `json:"known" doc:"Whether a revision was observed."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// PublishedAtFact records the observed package publication time.
type PublishedAtFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	PublishedAt *time.Time      `json:"published_at" format:"date-time" doc:"Observed publication time, or null when unknown."`
	Known       bool            `json:"known" doc:"Whether a publication time was observed."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// DiscoveryRelevanceFact records whether any provider surfaced this
// candidate. Reference requires documented relevance, so this fact matters.
type DiscoveryRelevanceFact struct {
	Status      FactStatusValue `json:"status" enum:"known,unknown,multiple,conflicting" doc:"Fact status."`
	Matched     bool            `json:"matched" doc:"Whether discovery surfaced the candidate."`
	EvidenceIDs []string        `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// Facts is the machine-readable fact picture extracted from evidence.
type Facts struct {
	Licence            LicenceFact            `json:"licence" doc:"Licence picture."`
	Advisory           AdvisoryFact           `json:"advisory" doc:"Advisory picture."`
	Dependency         DependencyFact         `json:"dependency" doc:"Dependency picture."`
	Archived           ArchivedFact           `json:"archived" doc:"Archived picture."`
	LastPush           LastPushFact           `json:"last_push" doc:"Last push picture."`
	Deprecated         DeprecatedFact         `json:"deprecated" doc:"Deprecation picture."`
	Revision           RevisionFact           `json:"revision" doc:"Revision picture."`
	PublishedAt        PublishedAtFact        `json:"published_at" doc:"Publication picture."`
	DiscoveryRelevance DiscoveryRelevanceFact `json:"discovery_relevance" doc:"Discovery relevance picture."`
}

// RequirementEvaluation is one requirement's behavioural verdict.
type RequirementEvaluation struct {
	RequirementID string                     `json:"requirement_id" doc:"Requirement evaluated."`
	Status        resolver.RequirementStatus `json:"status" enum:"satisfied,failed,unknown,conflicting" doc:"Behavioural verdict."`
	EvidenceIDs   []string                   `json:"evidence_ids" doc:"Behavioural evidence used; never null."`
}

// BehaviourEvaluation is the behavioural evidence picture for a candidate.
//
// Behavioural PASS is only ever produced by the Packet 2 evaluator over
// requirement-scoped evidence. Provider metadata can never create it.
type BehaviourEvaluation struct {
	SpecimenID   string                  `json:"specimen_id" doc:"Specimen evaluated."`
	Requirements []RequirementEvaluation `json:"requirements" doc:"Per-requirement verdicts; never null."`
}

// Tradeoff is one deterministic, non-numeric consideration.
type Tradeoff struct {
	Dimension   string   `json:"dimension" doc:"Dimension the tradeoff belongs to."`
	Message     string   `json:"message" doc:"Deterministic human-readable statement."`
	EvidenceIDs []string `json:"evidence_ids" doc:"Backing evidence; never null."`
}

// CandidateAssessment explains one candidate under one policy.
type CandidateAssessment struct {
	SpecimenID  string               `json:"specimen_id" doc:"Candidate assessed."`
	ReuseMode   model.ReuseMode      `json:"reuse_mode" enum:"copy,dependency,adapt,reference" doc:"Reuse mode considered."`
	Source      SourceRef            `json:"source" doc:"Attributable source."`
	Behaviour   BehaviourEvaluation  `json:"behaviour" doc:"Behavioural evidence picture."`
	Facts       Facts                `json:"facts" doc:"Machine-readable facts extracted from evidence."`
	Policy      []PolicyDecision     `json:"policy" doc:"Per-dimension policy verdicts; never null."`
	Disposition resolver.Disposition `json:"disposition" enum:"implementation_eligible,reference_only,needs_verification,needs_review,blocked" doc:"Why this candidate can or cannot be used."`
	Pros        []Tradeoff           `json:"pros" doc:"Supporting considerations; never null."`
	Cons        []Tradeoff           `json:"cons" doc:"Weakening considerations; never null."`
	Unknowns    []Tradeoff           `json:"unknowns" doc:"Preserved unknowns; never null."`
	EvidenceIDs []string             `json:"evidence_ids" doc:"All evidence consulted; never null."`
}

// Rejection preserves negative knowledge about a candidate.
type Rejection struct {
	SpecimenID string   `json:"specimen_id" doc:"Rejected candidate."`
	Reasons    []string `json:"reasons" doc:"Why it was rejected; never null."`
}

// Resolution remembers a decision and why it was reached.
type Resolution struct {
	PrimitiveID string        `json:"primitive_id" doc:"Primitive resolved."`
	ContractID  string        `json:"contract_id" doc:"Contract resolved."`
	Outcome     model.Outcome `json:"outcome" enum:"reuse,adapt,depend,reference,build_locally" doc:"Resolution outcome."`
	SpecimenID  string        `json:"specimen_id" doc:"Selected specimen, empty for build_locally."`
	Reasons     []string      `json:"reasons" doc:"Deterministic reasons; never null."`
	Rejected    []Rejection   `json:"rejected" doc:"Negative knowledge preserved; never null."`
	Unknowns    []string      `json:"unknowns" doc:"Unresolved required requirements; never null."`
	EvidenceIDs []string      `json:"evidence_ids" doc:"Evidence consulted; never null."`
	PolicyID    string        `json:"policy_id" doc:"Policy profile that justified the decision."`
	ResolvedAt  time.Time     `json:"resolved_at" format:"date-time" doc:"When the decision was recorded."`
}

// EffectivePolicy summarises the policy actually applied.
type EffectivePolicy struct {
	ID                  string            `json:"id" doc:"Policy profile identifier."`
	SchemaVersion       int               `json:"schema_version" doc:"Policy schema version."`
	AllowedReuseModes   []model.ReuseMode `json:"allowed_reuse_modes" enum:"copy,dependency,adapt,reference" doc:"Reuse modes permitted after refinement; never null."`
	PreferredReuseModes []model.ReuseMode `json:"preferred_reuse_modes" enum:"copy,dependency,adapt,reference" doc:"Preference order after refinement; never null."`
	LicenceDeny         []string          `json:"licence_deny" doc:"Licence deny list after refinement; never null."`
	LicenceAllow        []string          `json:"licence_allow" doc:"Licence allow list after refinement; never null."`
	MaxDirect           *int              `json:"max_direct" minimum:"0" doc:"Direct dependency ceiling after refinement, or null."`
	ArchivedAction      policy.Action     `json:"archived_action" enum:"allow,review,deny" doc:"Archived action after refinement."`
	MaxDaysSincePush    *int              `json:"max_days_since_push" minimum:"0" doc:"Staleness window after refinement, or null."`
	MaxDaysSinceRelease *int              `json:"max_days_since_release" minimum:"0" doc:"Release age window after refinement, or null."`
	MaxOptions          int               `json:"max_options" doc:"Shortlist bound after refinement."`
}

// AppliedFeedback records one feedback item the server actually applied.
type AppliedFeedback struct {
	CandidateID string                `json:"candidate_id" doc:"Candidate the feedback targeted."`
	Reason      policy.FeedbackReason `json:"reason" enum:"not_quite,too_many_dependencies,licence_not_allowed,avoid_dependency,avoid_reference,archived_project" doc:"Feedback reason."`
	Refinement  string                `json:"refinement" doc:"What the feedback changed, when it changed a rule."`
	Warning     string                `json:"warning" doc:"Non-fatal caveat, when one applies."`
}

// DecisionResponse is the POST /v1/resolve and POST /v1/refine body.
//
// status is resolved or needs_verification; both return HTTP 200.
// needs_verification carries a null resolution_id and a null resolution and
// persists nothing. It is not a failure and not BUILD LOCALLY.
type DecisionResponse struct {
	Status          resolver.DecisionStatus `json:"status" enum:"resolved,needs_verification" doc:"Decision status."`
	PolicyID        string                  `json:"policy_id" doc:"Policy profile identifier."`
	ResolutionID    *int64                  `json:"resolution_id" example:"12" doc:"Persisted resolution identity, or null when nothing was persisted."`
	Resolution      *Resolution             `json:"resolution" doc:"Persisted resolution, or null when nothing was persisted."`
	Selected        *CandidateAssessment    `json:"selected" doc:"Selected candidate assessment, or null."`
	Shortlist       []CandidateAssessment   `json:"shortlist" doc:"Ranked shortlist; never null."`
	Assessments     []CandidateAssessment   `json:"assessments" doc:"Every candidate assessed; never null."`
	EffectivePolicy EffectivePolicy         `json:"effective_policy" doc:"Policy actually applied, after refinement."`
	AppliedFeedback []AppliedFeedback       `json:"applied_feedback" doc:"Feedback actually applied; never null."`
}

// ------------------------------------------------------------- inspection

// GetPrimitiveInput selects one primitive by opaque id.
type GetPrimitiveInput struct {
	ID string `query:"id" required:"true" minLength:"1" maxLength:"1024" doc:"Opaque Reusery primitive id."`
}

// GetPrimitiveOutput returns one stored primitive.
type GetPrimitiveOutput struct {
	Body Primitive
}

// GetContractInput selects one contract by opaque id.
type GetContractInput struct {
	ID string `query:"id" required:"true" minLength:"1" maxLength:"1024" doc:"Opaque Reusery contract id."`
}

// GetContractOutput returns one stored contract.
type GetContractOutput struct {
	Body Contract
}

// GetSpecimenInput selects one specimen by opaque id.
type GetSpecimenInput struct {
	ID string `query:"id" required:"true" minLength:"1" maxLength:"1024" doc:"Opaque Reusery specimen id."`
}

// GetSpecimenOutput returns one stored specimen.
type GetSpecimenOutput struct {
	Body Specimen
}

// ListEvidenceInput selects a bounded page of evidence for one subject.
type ListEvidenceInput struct {
	SubjectID string `query:"subject_id" required:"true" minLength:"1" maxLength:"1024" doc:"Opaque Reusery specimen id whose evidence is wanted."`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100" doc:"Page size; defaults to 50 and may not exceed 100."`
	Cursor    string `query:"cursor" maxLength:"2048" doc:"Opaque cursor from a previous page; omit for the first page."`
}

// ListEvidenceOutput returns one bounded, ordered page of evidence.
type ListEvidenceOutput struct {
	Body EvidencePage
}

// EvidencePage is one page of ordered evidence.
type EvidencePage struct {
	SubjectID  string     `json:"subject_id" doc:"Subject the page belongs to."`
	Evidence   []Evidence `json:"evidence" doc:"Observations ordered by observed_at then id; never null."`
	Limit      int        `json:"limit" doc:"Page size used."`
	NextCursor *string    `json:"next_cursor" doc:"Opaque cursor for the next page, or null when the page is the last one."`
}

// GetResolutionInput selects one stored resolution by storage identity.
type GetResolutionInput struct {
	ResolutionID int64 `path:"resolution_id" minimum:"1" doc:"PostgreSQL storage identity of the resolution."`
}

// GetResolutionOutput returns a remembered resolution without re-evaluating
// current evidence.
type GetResolutionOutput struct {
	Body StoredResolution
}

// StoredResolution is a persisted resolution and its storage identity.
type StoredResolution struct {
	ResolutionID int64      `json:"resolution_id" doc:"PostgreSQL storage identity."`
	Resolution   Resolution `json:"resolution" doc:"The remembered resolution."`
}

// ----------------------------------------------------------- health/ready

// StatusResponse is the operational health and readiness body.
type StatusResponse struct {
	Status string `json:"status" example:"ok" doc:"ok or not ready."`
}

// HealthOutput is the GET /health response.
type HealthOutput struct {
	Body StatusResponse
}
