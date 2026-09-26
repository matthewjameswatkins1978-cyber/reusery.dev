package api

import (
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// The mapping layer is the only place that knows both sides of the boundary.
// Handlers never touch an internal struct directly, so an internal refactor
// cannot silently change a frozen API v1 field.

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilModes(values []model.ReuseMode) []model.ReuseMode {
	if values == nil {
		return []model.ReuseMode{}
	}
	return values
}

func mapSource(src model.SourceRef) SourceRef {
	return SourceRef{URL: src.URL, Revision: src.Revision, Path: src.Path, License: src.License}
}

func mapPrimitive(p model.Primitive) Primitive {
	return Primitive{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Tags:        nonNilStrings(p.Tags),
		ContractID:  p.ContractID,
	}
}

func mapContract(c model.Contract) Contract {
	out := Contract{
		ID:           c.ID,
		PrimitiveID:  c.PrimitiveID,
		Version:      c.Version,
		Summary:      c.Summary,
		Requirements: make([]Requirement, 0, len(c.Requirements)),
	}
	for _, requirement := range c.Requirements {
		out.Requirements = append(out.Requirements, Requirement{
			ID:          requirement.ID,
			Description: requirement.Description,
			Kind:        requirement.Kind,
			Required:    requirement.Required,
		})
	}
	return out
}

func mapSpecimen(s model.Specimen) Specimen {
	return Specimen{
		ID:          s.ID,
		PrimitiveID: s.PrimitiveID,
		Name:        s.Name,
		Source:      mapSource(s.Source),
		ReuseModes:  nonNilModes(s.ReuseMode),
	}
}

func mapEvidence(e model.Evidence) Evidence {
	return Evidence{
		ID:          e.ID,
		SubjectID:   e.SubjectID,
		Kind:        e.Kind,
		Claim:       e.Claim,
		Result:      e.Result,
		Source:      mapSource(e.Source),
		ObservedAt:  e.ObservedAt,
		AppliesTo:   e.AppliesTo,
		Methodology: e.Methodology,
		Artifact:    e.Artifact,
	}
}

func mapEvidenceList(values []model.Evidence) []Evidence {
	out := make([]Evidence, 0, len(values))
	for _, value := range values {
		out = append(out, mapEvidence(value))
	}
	return out
}

func mapRejections(values []model.Rejection) []Rejection {
	out := make([]Rejection, 0, len(values))
	for _, value := range values {
		out = append(out, Rejection{
			SpecimenID: value.SpecimenID,
			Reasons:    nonNilStrings(value.Reasons),
		})
	}
	return out
}

func mapResolution(r model.Resolution) Resolution {
	return Resolution{
		PrimitiveID: r.PrimitiveID,
		ContractID:  r.ContractID,
		Outcome:     r.Outcome,
		SpecimenID:  r.SpecimenID,
		Reasons:     nonNilStrings(r.Reasons),
		Rejected:    mapRejections(r.Rejected),
		Unknowns:    nonNilStrings(r.Unknowns),
		EvidenceIDs: nonNilStrings(r.EvidenceIDs),
		PolicyID:    r.PolicyID,
		ResolvedAt:  r.ResolvedAt,
	}
}

// ------------------------------------------------------------------ intent

func mapIntentResult(r intent.Result) IntentResult {
	out := IntentResult{
		Input:                  r.Input,
		Status:                 r.Status,
		RequestedArtifactLevel: r.RequestedArtifactLevel,
		Capability:             r.Capability,
		Summary:                r.Summary,
		Constraints:            make([]IntentConstraint, 0, len(r.Constraints)),
		Ambiguities:            make([]IntentAmbiguity, 0, len(r.Ambiguities)),
		Assumptions:            nonNilStrings(r.Assumptions),
		UnsupportedReason:      r.UnsupportedReason,
		Metadata: IntentGenerationMetadata{
			Provider:      r.Metadata.Provider,
			Model:         r.Metadata.Model,
			ResponseID:    r.Metadata.ResponseID,
			PromptVersion: r.Metadata.PromptVersion,
			SchemaVersion: r.Metadata.SchemaVersion,
			Calls:         r.Metadata.Calls,
			Repaired:      r.Metadata.Repaired,
			Usage: IntentUsage{
				InputTokens:     r.Metadata.Usage.InputTokens,
				OutputTokens:    r.Metadata.Usage.OutputTokens,
				ReasoningTokens: r.Metadata.Usage.ReasoningTokens,
				TotalTokens:     r.Metadata.Usage.TotalTokens,
			},
		},
	}
	if r.Primitive != nil {
		mapped := mapPrimitive(*r.Primitive)
		out.Primitive = &mapped
	}
	if r.Contract != nil {
		mapped := mapContract(*r.Contract)
		out.Contract = &mapped
	}
	for _, constraint := range r.Constraints {
		out.Constraints = append(out.Constraints, IntentConstraint{
			Kind:        constraint.Kind,
			Description: constraint.Description,
			Required:    constraint.Required,
		})
	}
	for _, ambiguity := range r.Ambiguities {
		out.Ambiguities = append(out.Ambiguities, IntentAmbiguity{
			Question:     ambiguity.Question,
			WhyItMatters: ambiguity.WhyItMatters,
		})
	}
	return out
}

// --------------------------------------------------------------- discovery

func mapDiscoveryProfile(p DiscoveryProfile) discovery.Profile {
	out := discovery.Profile{
		SchemaVersion: p.SchemaVersion,
		PrimitiveID:   p.PrimitiveID,
		ContractID:    p.ContractID,
		Providers:     make([]discovery.ProviderPlan, 0, len(p.Providers)),
	}
	for _, plan := range p.Providers {
		mapped := discovery.ProviderPlan{ID: plan.ID, Queries: make([]discovery.Query, 0, len(plan.Queries))}
		for _, query := range plan.Queries {
			mapped.Queries = append(mapped.Queries, discovery.Query{Text: query.Text, Limit: query.Limit})
		}
		out.Providers = append(out.Providers, mapped)
	}
	return out
}

func mapDiscoveryIssues(values []discovery.ProviderIssue) []DiscoveryIssue {
	out := make([]DiscoveryIssue, 0, len(values))
	for _, issue := range values {
		out = append(out, DiscoveryIssue{
			Kind:       issue.Kind,
			Provider:   issue.Provider,
			Query:      issue.Query,
			StatusCode: issue.StatusCode,
			RetryAfter: issue.RetryAfter,
			Message:    issue.Message,
		})
	}
	return out
}

func mapDiscoveryResult(r discovery.Result) DiscoveryResult {
	out := DiscoveryResult{
		PrimitiveID: r.PrimitiveID,
		ContractID:  r.ContractID,
		ObservedAt:  r.ObservedAt,
		Candidates:  make([]DiscoveryCandidate, 0, len(r.Candidates)),
		Providers:   make([]DiscoveryProviderReport, 0, len(r.Providers)),
	}
	for _, candidate := range r.Candidates {
		out.Candidates = append(out.Candidates, DiscoveryCandidate{
			ProviderID: candidate.ProviderID,
			Specimen:   mapSpecimen(candidate.Specimen),
			Evidence:   mapEvidenceList(candidate.Evidence),
		})
	}
	for _, report := range r.Providers {
		out.Providers = append(out.Providers, DiscoveryProviderReport{
			ID:             report.ID,
			Succeeded:      report.Succeeded,
			Requests:       report.Requests,
			CandidateCount: report.CandidateCount,
			Incomplete:     report.Incomplete,
			Issues:         mapDiscoveryIssues(report.Issues),
		})
	}
	return out
}

// ------------------------------------------------------------- enrichment

func mapEnrichIssues(values []enrichment.ProviderIssue) []EnrichIssue {
	out := make([]EnrichIssue, 0, len(values))
	for _, issue := range values {
		out = append(out, EnrichIssue{
			Kind:       issue.Kind,
			Provider:   issue.Provider,
			SpecimenID: issue.SpecimenID,
			StatusCode: issue.StatusCode,
			RetryAfter: issue.RetryAfter,
			Message:    issue.Message,
		})
	}
	return out
}

func mapEnrichResult(r enrichment.Result) EnrichResult {
	out := EnrichResult{
		ObservedAt: r.ObservedAt,
		Evidence:   mapEvidenceList(r.Evidence),
		Specimens:  make([]SpecimenEnrichReport, 0, len(r.Specimens)),
	}
	for _, report := range r.Specimens {
		mapped := SpecimenEnrichReport{
			SpecimenID:    report.SpecimenID,
			Supported:     report.Supported,
			EvidenceCount: report.EvidenceCount,
			Providers:     make([]EnrichProviderReport, 0, len(report.Providers)),
		}
		for _, provider := range report.Providers {
			mapped.Providers = append(mapped.Providers, EnrichProviderReport{
				ID:            provider.ID,
				Succeeded:     provider.Succeeded,
				Requests:      provider.Requests,
				EvidenceCount: provider.EvidenceCount,
				Issues:        mapEnrichIssues(provider.Issues),
			})
		}
		out.Specimens = append(out.Specimens, mapped)
	}
	return out
}

// ----------------------------------------------------------------- policy

// defaultedAction mirrors the loader's conservative default: an omitted
// action means review, never allow.
func defaultedAction(action policy.Action) policy.Action {
	if strings.TrimSpace(string(action)) == "" {
		return policy.ActionReview
	}
	return action
}

func modes(values []model.ReuseMode) []model.ReuseMode {
	if len(values) == 0 {
		return nil
	}
	return append([]model.ReuseMode(nil), values...)
}

func stringsOrNil(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

// mapPolicy converts the stable v1 DTO onto the internal policy profile and
// then runs the existing deterministic profile validation. It adds no policy
// features of its own.
func mapPolicy(p Policy) (policy.Policy, error) {
	out := policy.Policy{
		SchemaVersion: p.SchemaVersion,
		ID:            p.ID,
		Reuse: policy.ReusePolicy{
			Allowed:   modes(p.Reuse.Allowed),
			Preferred: modes(p.Reuse.Preferred),
		},
		Licence: policy.LicencePolicy{
			Allow:    stringsOrNil(p.Licence.Allow),
			Deny:     stringsOrNil(p.Licence.Deny),
			Unknown:  defaultedAction(p.Licence.Unknown),
			Multiple: defaultedAction(p.Licence.Multiple),
			Unlisted: defaultedAction(p.Licence.Unlisted),
		},
		Security: policy.SecurityPolicy{
			KnownAdvisory: defaultedAction(p.Security.KnownAdvisory),
			Unknown:       defaultedAction(p.Security.Unknown),
		},
		Dependencies: policy.DependencyPolicy{
			Unknown:   defaultedAction(p.Dependencies.Unknown),
			MaxDirect: p.Dependencies.MaxDirect,
		},
		Maintenance: policy.MaintenancePolicy{
			Archived:            defaultedAction(p.Maintenance.Archived),
			Deprecated:          defaultedAction(p.Maintenance.Deprecated),
			Stale:               defaultedAction(p.Maintenance.Stale),
			Unknown:             defaultedAction(p.Maintenance.Unknown),
			MaxDaysSincePush:    p.Maintenance.MaxDaysSincePush,
			MaxDaysSinceRelease: p.Maintenance.MaxDaysSinceRelease,
		},
		Source: policy.SourcePolicy{
			RequireRevisionFor: modes(p.Source.RequireRevisionFor),
			MissingRevision:    defaultedAction(p.Source.MissingRevision),
		},
		Selection: policy.SelectionPolicy{MaxOptions: p.Selection.MaxOptions},
	}
	if err := out.Validate(); err != nil {
		return policy.Policy{}, err
	}
	return out, nil
}

func mapFeedback(values []Feedback) []policy.Feedback {
	if len(values) == 0 {
		return nil
	}
	out := make([]policy.Feedback, 0, len(values))
	for _, value := range values {
		out = append(out, policy.Feedback{CandidateID: value.CandidateID, Reason: value.Reason})
	}
	return out
}

func mapCandidateRefs(values []CandidateRef) []resolver.CandidateRef {
	if len(values) == 0 {
		return nil
	}
	out := make([]resolver.CandidateRef, 0, len(values))
	for _, value := range values {
		out = append(out, resolver.CandidateRef{SpecimenID: value.SpecimenID, ReuseMode: value.ReuseMode})
	}
	return out
}

// ---------------------------------------------------------------- resolver

func mapTradeoffs(values []resolver.Tradeoff) []Tradeoff {
	out := make([]Tradeoff, 0, len(values))
	for _, value := range values {
		out = append(out, Tradeoff{
			Dimension:   value.Dimension,
			Message:     value.Message,
			EvidenceIDs: nonNilStrings(value.EvidenceIDs),
		})
	}
	return out
}

func mapDecisions(values []policy.PolicyDecision) []PolicyDecision {
	out := make([]PolicyDecision, 0, len(values))
	for _, value := range values {
		out = append(out, PolicyDecision{
			Dimension:   value.Dimension,
			Action:      value.Action,
			Reason:      value.Reason,
			EvidenceIDs: nonNilStrings(value.EvidenceIDs),
		})
	}
	return out
}

func mapBehaviour(evaluation resolver.CandidateEvaluation) BehaviourEvaluation {
	out := BehaviourEvaluation{
		SpecimenID:   evaluation.SpecimenID,
		Requirements: make([]RequirementEvaluation, 0, len(evaluation.Requirements)),
	}
	for _, requirement := range evaluation.Requirements {
		out.Requirements = append(out.Requirements, RequirementEvaluation{
			RequirementID: requirement.RequirementID,
			Status:        requirement.Status,
			EvidenceIDs:   nonNilStrings(requirement.EvidenceIDs),
		})
	}
	return out
}

func optionalTime(value time.Time, known bool) *time.Time {
	if !known || value.IsZero() {
		return nil
	}
	out := value
	return &out
}

func mapFacts(f policy.Facts) Facts {
	return Facts{
		Licence: LicenceFact{
			Status:              f.Licence.Status,
			Values:              nonNilStrings(f.Licence.Values),
			RelationshipUnknown: f.Licence.RelationshipUnknown,
			EvidenceIDs:         nonNilStrings(f.Licence.EvidenceIDs),
		},
		Advisory: AdvisoryFact{
			Status:      f.Advisory.Status,
			KnownIDs:    nonNilStrings(f.Advisory.KnownIDs),
			Count:       f.Advisory.Count,
			CountKnown:  f.Advisory.CountKnown,
			EvidenceIDs: nonNilStrings(f.Advisory.EvidenceIDs),
		},
		Dependency: DependencyFact{
			Status:             f.Dependency.Status,
			Direct:             nonNilStrings(f.Dependency.Direct),
			Indirect:           nonNilStrings(f.Dependency.Indirect),
			DirectCount:        f.Dependency.DirectCount,
			DirectCountKnown:   f.Dependency.DirectCountKnown,
			IndirectCount:      f.Dependency.IndirectCount,
			IndirectCountKnown: f.Dependency.IndirectCountKnown,
			EvidenceIDs:        nonNilStrings(f.Dependency.EvidenceIDs),
		},
		Archived: ArchivedFact{
			Status:      f.Archived.Status,
			Archived:    f.Archived.Archived,
			Known:       f.Archived.Known,
			EvidenceIDs: nonNilStrings(f.Archived.EvidenceIDs),
		},
		LastPush: LastPushFact{
			Status:      f.LastPush.Status,
			PushedAt:    optionalTime(f.LastPush.PushedAt, f.LastPush.Known),
			Known:       f.LastPush.Known,
			EvidenceIDs: nonNilStrings(f.LastPush.EvidenceIDs),
		},
		Deprecated: DeprecatedFact{
			Status:      f.Deprecated.Status,
			Deprecated:  f.Deprecated.Deprecated,
			Reason:      f.Deprecated.Reason,
			Known:       f.Deprecated.Known,
			EvidenceIDs: nonNilStrings(f.Deprecated.EvidenceIDs),
		},
		Revision: RevisionFact{
			Status:      f.Revision.Status,
			Revision:    f.Revision.Revision,
			Known:       f.Revision.Known,
			EvidenceIDs: nonNilStrings(f.Revision.EvidenceIDs),
		},
		PublishedAt: PublishedAtFact{
			Status:      f.PublishedAt.Status,
			PublishedAt: optionalTime(f.PublishedAt.PublishedAt, f.PublishedAt.Known),
			Known:       f.PublishedAt.Known,
			EvidenceIDs: nonNilStrings(f.PublishedAt.EvidenceIDs),
		},
		DiscoveryRelevance: DiscoveryRelevanceFact{
			Status:      f.DiscoveryRelevance.Status,
			Matched:     f.DiscoveryRelevance.Matched,
			EvidenceIDs: nonNilStrings(f.DiscoveryRelevance.EvidenceIDs),
		},
	}
}

func mapAssessment(a resolver.CandidateAssessment) CandidateAssessment {
	return CandidateAssessment{
		SpecimenID:  a.SpecimenID,
		ReuseMode:   a.ReuseMode,
		Source:      mapSource(a.Source),
		Behaviour:   mapBehaviour(a.Behaviour),
		Facts:       mapFacts(a.Facts),
		Policy:      mapDecisions(a.Policy),
		Disposition: a.Disposition,
		Pros:        mapTradeoffs(a.Pros),
		Cons:        mapTradeoffs(a.Cons),
		Unknowns:    mapTradeoffs(a.Unknowns),
		EvidenceIDs: nonNilStrings(a.EvidenceIDs),
	}
}

func mapAssessments(values []resolver.CandidateAssessment) []CandidateAssessment {
	out := make([]CandidateAssessment, 0, len(values))
	for _, value := range values {
		out = append(out, mapAssessment(value))
	}
	return out
}

func mapEffectivePolicy(s policy.EffectiveSummary) EffectivePolicy {
	return EffectivePolicy{
		ID:                  s.ID,
		SchemaVersion:       s.SchemaVersion,
		AllowedReuseModes:   nonNilModes(s.AllowedReuseModes),
		PreferredReuseModes: nonNilModes(s.PreferredReuseModes),
		LicenceDeny:         nonNilStrings(s.LicenceDeny),
		LicenceAllow:        nonNilStrings(s.LicenceAllow),
		MaxDirect:           s.MaxDirect,
		ArchivedAction:      s.ArchivedAction,
		MaxDaysSincePush:    s.MaxDaysSincePush,
		MaxDaysSinceRelease: s.MaxDaysSinceRelease,
		MaxOptions:          s.MaxOptions,
	}
}

func mapAppliedFeedback(values []policy.AppliedFeedback) []AppliedFeedback {
	out := make([]AppliedFeedback, 0, len(values))
	for _, value := range values {
		out = append(out, AppliedFeedback{
			CandidateID: value.CandidateID,
			Reason:      value.Reason,
			Refinement:  value.Refinement,
			Warning:     value.Warning,
		})
	}
	return out
}

// mapDecision converts a stored Packet 7 decision onto the v1 transport body.
// resolution_id and resolution are deliberately null together whenever
// nothing was persisted.
func mapDecision(stored resolver.StoredQualityDecision) DecisionResponse {
	decision := stored.Outcome.Decision
	out := DecisionResponse{
		Status:          decision.Status,
		PolicyID:        decision.PolicyID,
		Shortlist:       mapAssessments(decision.Shortlist),
		Assessments:     mapAssessments(decision.Assessments),
		EffectivePolicy: mapEffectivePolicy(policy.Summarize(stored.Outcome.EffectivePolicy)),
		AppliedFeedback: mapAppliedFeedback(stored.Outcome.AppliedFeedback),
	}
	if stored.ResolutionID > 0 {
		id := stored.ResolutionID
		out.ResolutionID = &id
	}
	if decision.Resolution != nil {
		mapped := mapResolution(*decision.Resolution)
		out.Resolution = &mapped
	}
	if decision.Selected != nil {
		mapped := mapAssessment(*decision.Selected)
		out.Selected = &mapped
	}
	return out
}
