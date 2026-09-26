package mcpserver

import (
	"strings"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// The mapping layer is the only place that knows both sides of this boundary.
// Handlers never touch an internal struct directly, so an internal refactor
// cannot silently rewrite an agent-facing schema.

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

// ------------------------------------------------------------------- policy

// defaultedAction mirrors the policy loader's conservative default: an omitted
// action means review, never allow.
func defaultedAction(action policy.Action) policy.Action {
	if strings.TrimSpace(string(action)) == "" {
		return policy.ActionReview
	}
	return action
}

func modesOrNil(values []model.ReuseMode) []model.ReuseMode {
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

// mapPolicy converts the supplied structured policy, or falls back to the
// code-owned public baseline when the caller omitted one. It adds no MCP
// policy rules: the result is validated by the same deterministic profile
// validation the CLI and HTTP surfaces use.
func mapPolicy(p *Policy) (policy.Policy, error) {
	if p == nil {
		return policy.PublicGoBaseline(), nil
	}
	mapped := policy.Policy{
		SchemaVersion: p.SchemaVersion,
		ID:            p.ID,
		Reuse: policy.ReusePolicy{
			Allowed:   modesOrNil(p.Reuse.Allowed),
			Preferred: modesOrNil(p.Reuse.Preferred),
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
			RequireRevisionFor: modesOrNil(p.Source.RequireRevisionFor),
			MissingRevision:    defaultedAction(p.Source.MissingRevision),
		},
		Selection: policy.SelectionPolicy{MaxOptions: p.Selection.MaxOptions},
	}
	if err := mapped.Validate(); err != nil {
		return policy.Policy{}, err
	}
	return mapped, nil
}

// ------------------------------------------------------------------- catalog

// mapCapability projects one shared capability listing entry onto the MCP DTO.
func mapCapability(c app.Capability) CatalogPrimitive {
	return mapCatalogPrimitive(c.Primitive, c.ContractSummary)
}

func mapCatalogPrimitive(p model.Primitive, contractSummary string) CatalogPrimitive {
	return CatalogPrimitive{
		PrimitiveID:     p.ID,
		Name:            p.Name,
		Description:     p.Description,
		ContractID:      p.ContractID,
		ContractSummary: contractSummary,
		Tags:            nonNilStrings(p.Tags),
	}
}

func mapCatalogContract(c model.Contract) CatalogContract {
	out := CatalogContract{
		ContractID:   c.ID,
		PrimitiveID:  c.PrimitiveID,
		Version:      c.Version,
		Summary:      c.Summary,
		Requirements: make([]CatalogRequirement, 0, len(c.Requirements)),
	}
	for _, requirement := range c.Requirements {
		out.Requirements = append(out.Requirements, CatalogRequirement{
			RequirementID: requirement.ID,
			Description:   requirement.Description,
			Kind:          requirement.Kind,
			Required:      requirement.Required,
		})
	}
	return out
}

// ----------------------------------------------------------------- discovery

func mapDiscoverResult(r discovery.Result) DiscoverResult {
	out := DiscoverResult{
		PrimitiveID: r.PrimitiveID,
		ContractID:  r.ContractID,
		ObservedAt:  r.ObservedAt,
		Candidates:  make([]DiscoveredCandidate, 0, len(r.Candidates)),
		Providers:   make([]DiscoverProviderReport, 0, len(r.Providers)),
	}
	for _, candidate := range r.Candidates {
		out.Candidates = append(out.Candidates, DiscoveredCandidate{
			SpecimenID: candidate.Specimen.ID,
			Name:       candidate.Specimen.Name,
			ProviderID: candidate.ProviderID,
			ReuseModes: nonNilModes(candidate.Specimen.ReuseMode),
			SourceURL:  candidate.Specimen.Source.URL,
			Revision:   candidate.Specimen.Source.Revision,
		})
	}
	for _, report := range r.Providers {
		out.Providers = append(out.Providers, DiscoverProviderReport{
			ID:             report.ID,
			Succeeded:      report.Succeeded,
			Requests:       report.Requests,
			CandidateCount: report.CandidateCount,
			Incomplete:     report.Incomplete,
			Issues:         mapDiscoverIssues(report.Issues),
		})
	}
	return out
}

func mapDiscoverIssues(values []discovery.ProviderIssue) []DiscoverIssue {
	out := make([]DiscoverIssue, 0, len(values))
	for _, issue := range values {
		out = append(out, DiscoverIssue{
			Kind:       string(issue.Kind),
			Provider:   issue.Provider,
			Query:      issue.Query,
			StatusCode: issue.StatusCode,
			Message:    issue.Message,
		})
	}
	return out
}

// ----------------------------------------------------------------- enrichment

func mapEnrichResult(r enrichment.Result) EnrichResult {
	out := EnrichResult{
		ObservedAt: r.ObservedAt,
		Evidence:   len(r.Evidence),
		Specimens:  make([]EnrichSpecimenReport, 0, len(r.Specimens)),
	}
	for _, report := range r.Specimens {
		mapped := EnrichSpecimenReport{
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

func mapEnrichIssues(values []enrichment.ProviderIssue) []EnrichIssue {
	out := make([]EnrichIssue, 0, len(values))
	for _, issue := range values {
		out = append(out, EnrichIssue{
			Kind:       string(issue.Kind),
			Provider:   issue.Provider,
			StatusCode: issue.StatusCode,
			Message:    issue.Message,
		})
	}
	return out
}

// -------------------------------------------------------------------- decide

// mapCandidateRefs converts the transport candidate list onto the Packet 7
// request type. Candidate order never becomes quality order.
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

// requiredRequirementIDs indexes the contract's required claims in authored
// order so every derived list is deterministic.
func requiredRequirementIDs(contract model.Contract) []string {
	out := make([]string, 0, len(contract.Requirements))
	for _, requirement := range contract.Requirements {
		if requirement.Required {
			out = append(out, requirement.ID)
		}
	}
	return out
}

// unknownRequirementsBySpecimen records, for each assessed candidate, which
// required requirements are still behaviourally unknown.
func unknownRequirementsBySpecimen(decision resolver.QualityDecision) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(decision.Assessments))
	for _, assessment := range decision.Assessments {
		unknown := make(map[string]bool, len(assessment.Behaviour.Requirements))
		for _, requirement := range assessment.Behaviour.Requirements {
			if requirement.Status == resolver.RequirementUnknown {
				unknown[requirement.RequirementID] = true
			}
		}
		out[assessment.SpecimenID] = unknown
	}
	return out
}

// buildDecision converts a Packet 7 outcome into the compact agent view.
//
// It deliberately drops the assessment graph: an agent needs enough to steer,
// not every Evidence record. Behavioural unknowns are preserved in full
// because that is what keeps REFERENCE and needs_verification honest.
func buildDecision(stored resolver.StoredQualityDecision, contract model.Contract) DecisionResult {
	decision := stored.Outcome.Decision
	required := requiredRequirementIDs(contract)
	perCandidate := unknownRequirementsBySpecimen(decision)

	out := DecisionResult{
		Status:    decision.Status,
		PolicyID:  decision.PolicyID,
		Shortlist: make([]ShortlistEntry, 0, len(decision.Shortlist)),
		Unknowns:  []string{},
		Reasons:   []string{},
		Rejected:  []RejectedEntry{},
	}
	if stored.ResolutionID > 0 {
		id := stored.ResolutionID
		out.ResolutionID = &id
	}

	// Unknowns are reported as bare requirement ids in authored order, unioned
	// across every assessed candidate. The union can never exceed the contract,
	// so the field stays bounded however many candidates the caller sent.
	seen := make(map[string]bool, len(required))
	for _, assessment := range decision.Assessments {
		for requirementID := range perCandidate[assessment.SpecimenID] {
			seen[requirementID] = true
		}
	}
	for _, requirementID := range required {
		if seen[requirementID] {
			out.Unknowns = append(out.Unknowns, requirementID)
		}
	}

	for _, entry := range decision.Shortlist {
		unknown := perCandidate[entry.SpecimenID]
		list := make([]string, 0, len(required))
		for _, requirementID := range required {
			if unknown[requirementID] {
				list = append(list, requirementID)
			}
		}
		out.Shortlist = append(out.Shortlist, ShortlistEntry{
			SpecimenID:          entry.SpecimenID,
			ReuseMode:           entry.ReuseMode,
			Disposition:         entry.Disposition,
			UnknownRequirements: list,
		})
	}

	if decision.Selected != nil {
		selected := mapSelected(*decision.Selected)
		out.Selected = &selected
	}
	if decision.Resolution != nil {
		resolution := decision.Resolution
		outcome := resolution.Outcome
		out.Outcome = &outcome
		out.Reasons = nonNilStrings(resolution.Reasons)
		out.Rejected = make([]RejectedEntry, 0, len(resolution.Rejected))
		for _, rejected := range resolution.Rejected {
			out.Rejected = append(out.Rejected, RejectedEntry{
				SpecimenID: rejected.SpecimenID,
				Reasons:    nonNilStrings(rejected.Reasons),
			})
		}
	}
	return out
}

func mapSelected(a resolver.CandidateAssessment) SelectedCandidate {
	return SelectedCandidate{
		SpecimenID:  a.SpecimenID,
		ReuseMode:   a.ReuseMode,
		Disposition: a.Disposition,
		SourceURL:   a.Source.URL,
		Revision:    a.Source.Revision,
		License:     a.Source.License,
	}
}

// buildRefine adds what the feedback actually changed to the shared decision.
func buildRefine(stored resolver.StoredQualityDecision, contract model.Contract) RefineResult {
	return RefineResult{
		DecisionResult:  buildDecision(stored, contract),
		AppliedFeedback: mapAppliedFeedback(stored.Outcome.AppliedFeedback),
		EffectivePolicy: policy.Summarize(stored.Outcome.EffectivePolicy),
	}
}

func mapAppliedFeedback(values []policy.AppliedFeedback) []policy.AppliedFeedback {
	if values == nil {
		return []policy.AppliedFeedback{}
	}
	return values
}

// -------------------------------------------------------------------- inspect

// mapEvidencePage renders one bounded page. Artifact is omitted: it carries
// machine-internal fact JSON that the human-readable claim already states.
func mapEvidencePage(subjectID string, rows []model.Evidence, limit int, hasNext bool) EvidenceResultPage {
	page := EvidenceResultPage{
		SubjectID: subjectID,
		Evidence:  make([]EvidenceRecord, 0, len(rows)),
		Limit:     limit,
	}
	for _, row := range rows {
		page.Evidence = append(page.Evidence, EvidenceRecord{
			ID:          row.ID,
			Kind:        row.Kind,
			Claim:       row.Claim,
			Result:      row.Result,
			Source:      EvidenceSource{URL: row.Source.URL, Revision: row.Source.Revision, Path: row.Source.Path, License: row.Source.License},
			ObservedAt:  row.ObservedAt,
			AppliesTo:   row.AppliesTo,
			Methodology: row.Methodology,
		})
	}
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		observedAt := last.ObservedAt
		page.NextAfter = &EvidenceCursor{ObservedAt: &observedAt, EvidenceID: last.ID}
	}
	return page
}

func mapResolution(r model.Resolution) StoredResolution {
	rejected := make([]StoredRejection, 0, len(r.Rejected))
	for _, entry := range r.Rejected {
		rejected = append(rejected, StoredRejection{
			SpecimenID: entry.SpecimenID,
			Reasons:    nonNilStrings(entry.Reasons),
		})
	}
	return StoredResolution{
		PrimitiveID: r.PrimitiveID,
		ContractID:  r.ContractID,
		Outcome:     r.Outcome,
		SpecimenID:  r.SpecimenID,
		Reasons:     nonNilStrings(r.Reasons),
		Rejected:    rejected,
		Unknowns:    nonNilStrings(r.Unknowns),
		EvidenceIDs: nonNilStrings(r.EvidenceIDs),
		PolicyID:    r.PolicyID,
		ResolvedAt:  r.ResolvedAt,
	}
}

func mapOutcomeEvents(values []outcome.StoredFeedback) []OutcomeEvent {
	out := make([]OutcomeEvent, 0, len(values))
	for _, value := range values {
		out = append(out, OutcomeEvent{
			ID:           value.ID,
			ResolutionID: value.ResolutionID,
			Kind:         value.Kind,
			Note:         value.Note,
			RecordedAt:   value.RecordedAt,
		})
	}
	return out
}
