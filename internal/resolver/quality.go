package resolver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// Disposition is the higher-level quality verdict for one candidate option.
// It sits above model.Outcome: Packet 4's kernel still decides what a chosen
// mode means, while a disposition says whether this candidate may be chosen at
// all.
type Disposition string

const (
	// ImplementationEligible means every required behavioural requirement is
	// satisfied, no hard policy deny applies and no policy rule still needs
	// review. Only this disposition and ReferenceOnly are automatically
	// selectable.
	ImplementationEligible Disposition = "implementation_eligible"
	// ReferenceOnly means the candidate is attributable, relevant and
	// policy-clean enough to learn from, without any claim that it satisfies
	// the complete behavioural contract.
	ReferenceOnly Disposition = "reference_only"
	// NeedsVerification means behaviour is missing, not rejected. Absence of
	// evidence must never become "local code is better".
	NeedsVerification Disposition = "needs_verification"
	// NeedsReview means behaviour is fully satisfied but a policy rule awaits
	// a human. Review is not deny and is not automatic approval.
	NeedsReview Disposition = "needs_review"
	// Blocked means an explicit required failure, a conflicting observation or
	// a hard policy deny rules this candidate out.
	Blocked Disposition = "blocked"
)

// Tradeoff is one inspectable dimension of a candidate. It is text attached to
// evidence, never a number, and it is never written by a model.
type Tradeoff struct {
	Dimension   string   `json:"dimension"`
	Message     string   `json:"message"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// CandidateAssessment is one candidate's inspectable comparison record. There
// is no quality score, no confidence value and no weighted total anywhere in
// this structure. Source and Facts ride along so a caller can show provenance,
// licence, advisory, dependency and maintenance facts without a second lookup.
type CandidateAssessment struct {
	SpecimenID  string                  `json:"specimen_id"`
	ReuseMode   model.ReuseMode         `json:"reuse_mode"`
	Source      model.SourceRef         `json:"source"`
	Behaviour   CandidateEvaluation     `json:"behaviour"`
	Facts       policy.Facts            `json:"facts"`
	Policy      []policy.PolicyDecision `json:"policy"`
	Disposition Disposition             `json:"disposition"`
	Pros        []Tradeoff              `json:"pros"`
	Cons        []Tradeoff              `json:"cons"`
	Unknowns    []Tradeoff              `json:"unknowns"`
	EvidenceIDs []string                `json:"evidence_ids"`
}

// DecisionStatus is the quality layer's top-level state. It is deliberately
// distinct from model.Outcome: NEEDS_VERIFICATION is an intermediate state,
// not a resolution, and it never produces a model.Resolution.
type DecisionStatus string

const (
	// StatusResolved means a candidate was selected and a Resolution is
	// justified, including BUILD LOCALLY.
	StatusResolved DecisionStatus = "resolved"
	// StatusNeedsVerification means plausible candidates remain but required
	// behavioural evidence is missing, so no candidate may be selected yet and
	// nothing is persisted.
	StatusNeedsVerification DecisionStatus = "needs_verification"
)

// QualityDecision is the whole quality output: an ordered, bounded shortlist,
// every assessment that was actually made, and either a justified Resolution or
// an honest needs_verification.
type QualityDecision struct {
	Status      DecisionStatus        `json:"status"`
	Resolution  *model.Resolution     `json:"resolution,omitempty"`
	Selected    *CandidateAssessment  `json:"selected,omitempty"`
	Shortlist   []CandidateAssessment `json:"shortlist"`
	Assessments []CandidateAssessment `json:"assessments"`
	PolicyID    string                `json:"policy_id"`
}

// DependencyFit describes how a dependency-mode candidate relates to the
// project manifest that would receive it. It is a bounded project fact, never
// Evidence and never a behavioural claim.
type DependencyFit string

const (
	// FitNotApplicable means the candidate is not considered in dependency
	// mode, so the project manifest says nothing about it.
	FitNotApplicable DependencyFit = "not_applicable"
	// FitNewDependency means no currently required module provides the
	// candidate package. It is neutral: not good, not bad.
	FitNewDependency DependencyFit = "new_dependency"
	// FitExistingExact means the project already requires the matching module
	// at exactly the candidate revision. A deterministic positive integration
	// fact and nothing more.
	FitExistingExact DependencyFit = "existing_exact"
	// FitExistingVersionChange means the module exists at another version.
	// Exact comparison only: no semantic-version reasoning happens here, so a
	// change simply requires review rather than a silent upgrade.
	FitExistingVersionChange DependencyFit = "existing_version_change"
	// FitExistingReplaced means the module has a Go replace directive, so
	// upstream package metadata may not describe what the project actually
	// depends on. Requires review.
	FitExistingReplaced DependencyFit = "existing_replaced"
)

// CandidateContext is the bounded project context available to one candidate
// during a decision. It is derived from a scanned manifest by Reusery and is
// never caller-supplied.
type CandidateContext struct {
	DependencyFit DependencyFit
	// ModulePath is the project module the candidate package belongs to.
	ModulePath string
	// ModuleVersion is the version that module currently requires.
	ModuleVersion string
}

// DimensionProjectDependency is the assessment dimension for bounded project
// context facts. These are project inputs, not provider Evidence, so no
// EvidenceID is ever attached to them.
const DimensionProjectDependency = "project_dependency"

// projectTieBreakMessage is the disclosed reason when project context, and
// only project context, separated two otherwise equivalent candidates.
const projectTieBreakMessage = "candidate was preferred among otherwise equivalent " +
	"options because its exact module version is already present in project %s"

// QualityInput is the pure input to the quality layer. Candidate order in the
// request is deliberately not quality order: Decide reorders everything through
// deterministic policy semantics.
//
// ProjectID, ProjectContextHash and Context are all optional. When they are
// empty the decision is byte-for-byte the Packets 1-9 decision.
type QualityInput struct {
	Primitive  model.Primitive
	Contract   model.Contract
	Candidates []CandidateOption
	Policy     policy.Policy
	Feedback   []policy.Feedback
	Now        time.Time

	// ProjectID names the project whose context applies; empty means none.
	ProjectID string
	// ProjectContextHash pins the immutable context snapshot recorded on the
	// Resolution.
	ProjectContextHash string
	// Context is keyed by specimen ID. A key with no matching candidate is
	// ignored; a candidate with no key sees no project context at all.
	Context map[string]CandidateContext
}

// QualityOutcome is the decision plus the policy actually applied and the
// feedback that changed it.
type QualityOutcome struct {
	Decision        QualityDecision
	EffectivePolicy policy.Policy
	AppliedFeedback []policy.AppliedFeedback
}

// qualityCandidate pairs one request entry with everything Decide derived
// from it.
type qualityCandidate struct {
	option     CandidateOption
	assessment CandidateAssessment
	facts      policy.Facts
}

// decision status helpers shared with the application service.
func dispositionRank(disposition Disposition) int {
	switch disposition {
	case ImplementationEligible:
		return 0
	case ReferenceOnly:
		return 1
	case NeedsReview:
		return 2
	case NeedsVerification:
		return 3
	default:
		return 4
	}
}

// Assess builds one candidate's assessment from its behavioural evaluation,
// its typed facts and the explicit policy.
func Assess(
	specimen model.Specimen,
	mode model.ReuseMode,
	contract model.Contract,
	evaluation CandidateEvaluation,
	facts policy.Facts,
	pol policy.Policy,
	now time.Time,
) CandidateAssessment {
	decisions := policy.Evaluate(pol, specimen, mode, facts, now)
	denies, reviews := policy.CountActions(decisions)
	required := requiredStatuses(contract, evaluation)
	declared := declaresReuseMode(specimen, mode)

	assessment := CandidateAssessment{
		SpecimenID:  specimen.ID,
		ReuseMode:   mode,
		Source:      specimen.Source,
		Behaviour:   evaluation,
		Facts:       facts,
		Policy:      decisions,
		Pros:        []Tradeoff{},
		Cons:        []Tradeoff{},
		Unknowns:    []Tradeoff{},
		EvidenceIDs: []string{},
	}
	assessment.Disposition = dispositionFor(
		declared,
		mode,
		required,
		denies,
		reviews,
		facts,
		specimen.Source.URL != "" || specimen.Source.Path != "",
	)

	for _, decision := range decisions {
		if decision.Dimension == policy.DimensionReuse && decision.Action == policy.ActionAllow {
			continue
		}
		tradeoff := Tradeoff{Dimension: decision.Dimension, Message: decision.Reason, EvidenceIDs: decision.EvidenceIDs}
		switch decision.Action {
		case policy.ActionDeny, policy.ActionReview:
			assessment.Cons = append(assessment.Cons, tradeoff)
		case policy.ActionAllow:
			assessment.Pros = append(assessment.Pros, tradeoff)
		}
	}

	if allRequiredSatisfied(required) {
		assessment.Pros = append(assessment.Pros, Tradeoff{
			Dimension: "behaviour",
			Message:   "all required behavioural requirements are satisfied",
		})
	}
	assessment.Unknowns = append(assessment.Unknowns, behaviouralUnknowns(contract, evaluation, required)...)
	assessment.Unknowns = append(assessment.Unknowns, factUnknowns(facts)...)
	assessment.EvidenceIDs = assessmentEvidenceIDs(evaluation, decisions)
	return assessment
}

// dispositionFor applies the Packet 7 disposition rules. Review blocks
// automatic direct selection; unknown behaviour blocks selection without
// becoming BUILD LOCALLY; a hard deny or an explicit required failure blocks
// outright.
func dispositionFor(
	declared bool,
	mode model.ReuseMode,
	required map[string]RequirementStatus,
	denies, reviews int,
	facts policy.Facts,
	provenanceInspectable bool,
) Disposition {
	hasFailed, hasUnknown := requiredSummary(required)

	switch {
	case !declared:
		return Blocked
	case denies > 0:
		return Blocked
	case hasFailed:
		return Blocked
	case mode == model.ReuseReference:
		// Reference requires genuine documented relevance and inspectable
		// provenance. Required UNKNOWN behaviour is explicitly allowed here:
		// REFERENCE never claims the contract is satisfied.
		if !facts.DiscoveryRelevance.Matched || !provenanceInspectable {
			return Blocked
		}
		return ReferenceOnly
	case hasUnknown:
		return NeedsVerification
	case reviews > 0:
		return NeedsReview
	default:
		return ImplementationEligible
	}
}

// Decide runs the whole quality layer over ordered candidate options.
//
// It is deterministic and side-effect free: no network, no clock read beyond
// in.Now, no PostgreSQL and no score. Candidate order supplied by the caller
// never becomes quality order.
func Decide(in QualityInput) (QualityOutcome, error) {
	if err := validateQualityInput(in); err != nil {
		return QualityOutcome{}, err
	}

	factsByCandidate := make(map[string]policy.Facts, len(in.Candidates))
	for _, option := range in.Candidates {
		factsByCandidate[option.Specimen.ID] = policy.ExtractFacts(option.Specimen, option.Evidence)
	}

	feedbackOutcome, err := policy.ApplyFeedback(in.Policy, factsByCandidate, in.Feedback)
	if err != nil {
		return QualityOutcome{}, err
	}
	effective := feedbackOutcome.Policy
	exclusions := make(map[string]policy.Exclusion, len(feedbackOutcome.Excluded))
	for _, exclusion := range feedbackOutcome.Excluded {
		exclusions[exclusion.CandidateID] = exclusion
	}

	candidates := make([]qualityCandidate, 0, len(in.Candidates))
	pinned := make(map[string]bool, len(in.Candidates))
	for _, option := range in.Candidates {
		evaluation, err := Evaluate(in.Contract, option.Specimen, option.Evidence)
		if err != nil {
			return QualityOutcome{}, err
		}
		facts := factsByCandidate[option.Specimen.ID]
		assessment := Assess(option.Specimen, option.ReuseMode, in.Contract, evaluation, facts, effective, in.Now)
		// Project context may only lower a disposition or add a trade-off;
		// it runs before the feedback exclusion so an explicit exclusion
		// still wins outright.
		applyCandidateContext(&assessment, in.Context[option.Specimen.ID])
		if exclusion, excluded := exclusions[option.Specimen.ID]; excluded {
			assessment.Disposition = Blocked
			assessment.Cons = append(assessment.Cons, Tradeoff{
				Dimension: "user_feedback",
				Message:   exclusion.RejectionReason(),
			})
		}
		candidates = append(candidates, qualityCandidate{option: option, assessment: assessment, facts: facts})
		pinned[option.Specimen.ID] = option.Specimen.Source.Revision != ""
	}

	ordered := orderCandidates(candidates, effective, pinned, in.Context)

	selection := selectCandidate(ordered)
	note := ""
	if selection >= 0 && selection+1 < len(ordered) {
		if indistinguishable(ordered[selection].assessment, ordered[selection+1].assessment, effective, pinned) {
			switch {
			case in.ProjectID != "" && dependencyFitRank(in.Context[ordered[selection].assessment.SpecimenID]) !=
				dependencyFitRank(in.Context[ordered[selection+1].assessment.SpecimenID]):
				// The two are equivalent on policy, behaviour and preferred
				// mode; only project context separated them, and that has to
				// be disclosed rather than passed off as "better".
				note = fmt.Sprintf(projectTieBreakMessage, in.ProjectID)
			default:
				note = tieBreakMessage
			}
			ordered[selection].assessment.Unknowns = append(ordered[selection].assessment.Unknowns, Tradeoff{
				Dimension: "ordering",
				Message:   note,
			})
		}
	}

	assessments := make([]CandidateAssessment, 0, len(ordered))
	for _, candidate := range ordered {
		assessments = append(assessments, candidate.assessment)
	}

	decision := QualityDecision{
		Status:      StatusResolved,
		Shortlist:   buildShortlist(ordered, effective),
		Assessments: assessments,
		PolicyID:    effective.ID,
	}

	switch {
	case selection >= 0:
		resolution := selectedResolution(in, ordered, selection, exclusions, effective, note)
		decision.Resolution = &resolution
		decision.Selected = &assessments[selection]
	case anyPlausible(ordered):
		decision.Status = StatusNeedsVerification
	default:
		resolution := buildLocallyResolution(in, ordered, exclusions, effective)
		decision.Resolution = &resolution
	}

	return QualityOutcome{
		Decision:        decision,
		EffectivePolicy: effective,
		AppliedFeedback: feedbackOutcome.Applied,
	}, nil
}

func validateQualityInput(in QualityInput) error {
	if in.Primitive.ID == "" {
		return ErrEmptyPrimitiveID
	}
	if in.Contract.ID == "" {
		return ErrEmptyContractID
	}
	if in.Primitive.ContractID != in.Contract.ID {
		return fmt.Errorf("%w: primitive %q declares %q", ErrPrimitiveContractMismatch, in.Primitive.ID, in.Primitive.ContractID)
	}
	if in.Contract.PrimitiveID != in.Primitive.ID {
		return fmt.Errorf("%w: contract %q declares %q", ErrContractPrimitiveMismatch, in.Contract.ID, in.Contract.PrimitiveID)
	}
	if in.Now.IsZero() {
		return ErrZeroResolvedAt
	}
	if err := validateContract(in.Contract); err != nil {
		return err
	}
	if err := in.Policy.Validate(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(in.Candidates))
	for _, option := range in.Candidates {
		if option.Specimen.ID == "" {
			return ErrEmptySpecimenID
		}
		if _, duplicate := seen[option.Specimen.ID]; duplicate {
			return fmt.Errorf("%w: %q", ErrDuplicateCandidateID, option.Specimen.ID)
		}
		seen[option.Specimen.ID] = struct{}{}
		if option.Specimen.PrimitiveID != in.Primitive.ID {
			return fmt.Errorf("%w: %q declares %q", ErrCandidatePrimitiveMismatch, option.Specimen.ID, option.Specimen.PrimitiveID)
		}
		if _, supported := reuseModeOutcomes[option.ReuseMode]; !supported {
			return fmt.Errorf("%w: %q", ErrUnsupportedReuseMode, option.ReuseMode)
		}
		if !declaresReuseMode(option.Specimen, option.ReuseMode) {
			return fmt.Errorf("%w: specimen %q does not declare %q", ErrReuseModeNotDeclared, option.Specimen.ID, option.ReuseMode)
		}
	}
	return nil
}

func orderCandidates(candidates []qualityCandidate, pol policy.Policy, pinned map[string]bool, context map[string]CandidateContext) []qualityCandidate {
	ordered := append([]qualityCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].assessment, ordered[j].assessment
		ra, rb := dispositionRank(a.Disposition), dispositionRank(b.Disposition)
		if ra != rb {
			return ra < rb
		}
		switch a.Disposition {
		case ImplementationEligible:
			pa, pb := pol.PreferredIndex(a.ReuseMode), pol.PreferredIndex(b.ReuseMode)
			if pa != pb {
				return pa < pb
			}
			// Late lexicographic tie-break: among otherwise equivalent
			// eligible candidates, reuse what the project already carries.
			// It never outranks behavioural fit, policy eligibility or an
			// authored preferred reuse mode, and it is not a score.
			fa := dependencyFitRank(context[a.SpecimenID])
			fb := dependencyFitRank(context[b.SpecimenID])
			if fa != fb {
				return fa < fb
			}
		case ReferenceOnly:
			pa, pb := pinnedRank(pinned[a.SpecimenID]), pinnedRank(pinned[b.SpecimenID])
			if pa != pb {
				return pa < pb
			}
		}
		if a.SpecimenID != b.SpecimenID {
			return a.SpecimenID < b.SpecimenID
		}
		return a.ReuseMode < b.ReuseMode
	})
	return ordered
}

// dependencyFitRank orders an exact-present dependency ahead of any other fit.
// Everything except existing_exact ranks equally, because new_dependency is
// neutral rather than bad.
func dependencyFitRank(context CandidateContext) int {
	if context.DependencyFit == FitExistingExact {
		return 0
	}
	return 1
}

// ProjectTradeoffMessage renders the disclosed trade-off a project fact adds
// to one candidate assessment. It is exported so the project layer can carry
// exactly the same sentence in its per-candidate effect: one wording, one
// meaning. It returns an empty string when the fit adds no trade-off.
func ProjectTradeoffMessage(fit DependencyFit, modulePath, moduleVersion, candidateRevision string) string {
	switch fit {
	case FitExistingExact:
		return "exact candidate module version is already present in the project manifest"
	case FitExistingVersionChange:
		return fmt.Sprintf("project requires %s at %s; candidate is %s; version change requires review",
			modulePath, moduleVersion, candidateRevision)
	case FitExistingReplaced:
		return fmt.Sprintf("project replaces %s; dependency semantics require review", modulePath)
	default:
		return ""
	}
}

// applyCandidateContext overlays bounded project facts onto one assessment.
//
// It may only lower a disposition, add a trade-off, or (via orderCandidates)
// break a tie between already-eligible candidates. It can never turn blocked
// into eligible, unknown behaviour into satisfied, or policy review into
// allow. A candidate whose behaviour is already needs_verification stays
// needs_verification: uncertainty is never upgraded into review or approval.
func applyCandidateContext(assessment *CandidateAssessment, context CandidateContext) {
	message := ProjectTradeoffMessage(context.DependencyFit,
		context.ModulePath, context.ModuleVersion, assessment.Source.Revision)
	if message == "" {
		return
	}
	tradeoff := Tradeoff{Dimension: DimensionProjectDependency, Message: message}
	if context.DependencyFit == FitExistingExact {
		assessment.Pros = append(assessment.Pros, tradeoff)
		return
	}
	assessment.Cons = append(assessment.Cons, tradeoff)
	// A silent upgrade or downgrade of the project is never our call.
	if assessment.Disposition == ImplementationEligible {
		assessment.Disposition = NeedsReview
	}
}

// pinnedRank sorts a pinned or versioned provenance before an unpinned one.
func pinnedRank(isPinned bool) int {
	if isPinned {
		return 0
	}
	return 1
}

// selectCandidate returns the index of the candidate that may be resolved:
// the first implementation-eligible one, otherwise the first reference-only
// one, otherwise -1. Ordering already places those first.
func selectCandidate(ordered []qualityCandidate) int {
	for index, candidate := range ordered {
		switch candidate.assessment.Disposition {
		case ImplementationEligible, ReferenceOnly:
			return index
		}
	}
	return -1
}

// anyPlausible reports whether some candidate is waiting on verification or
// review rather than being rejected. That distinction is what keeps UNKNOWN
// from becoming BUILD LOCALLY.
func anyPlausible(ordered []qualityCandidate) bool {
	for _, candidate := range ordered {
		switch candidate.assessment.Disposition {
		case NeedsVerification, NeedsReview:
			return true
		}
	}
	return false
}

const tieBreakMessage = "available policy and evidence do not distinguish these candidates; " +
	"stable specimen ID was used as the deterministic tie-break"

// indistinguishable reports whether two candidates differ only by specimen ID
// for ordering purposes. When they do, selection is still deterministic but it
// is not evidence that the chosen one is better, and that has to be said out
// loud rather than hidden behind "best".
func indistinguishable(a, b CandidateAssessment, pol policy.Policy, pinned map[string]bool) bool {
	if a.Disposition != b.Disposition {
		return false
	}
	switch a.Disposition {
	case ImplementationEligible:
		if pol.PreferredIndex(a.ReuseMode) != pol.PreferredIndex(b.ReuseMode) {
			return false
		}
	case ReferenceOnly:
		if pinned[a.SpecimenID] != pinned[b.SpecimenID] {
			return false
		}
	}
	return policySignature(a) == policySignature(b) && behaviourSignature(a) == behaviourSignature(b)
}

func policySignature(a CandidateAssessment) string {
	parts := make([]string, 0, len(a.Policy))
	for _, decision := range a.Policy {
		parts = append(parts, decision.Dimension+"="+string(decision.Action))
	}
	return strings.Join(parts, ",")
}

func behaviourSignature(a CandidateAssessment) string {
	parts := make([]string, 0, len(a.Behaviour.Requirements))
	for _, requirement := range a.Behaviour.Requirements {
		parts = append(parts, requirement.RequirementID+"="+string(requirement.Status))
	}
	return strings.Join(parts, ",")
}

// buildShortlist returns a small, materially useful option set bounded by
// policy.selection.max_options and a hard ceiling of five.
//
// Options are diversified by reuse mode first, then filled in deterministic
// order. Blocked candidates are never shortlisted, but they remain in
// assessments and in Resolution.Rejected.
func buildShortlist(ordered []qualityCandidate, pol policy.Policy) []CandidateAssessment {
	limit := pol.Selection.MaxOptions
	if limit < 1 {
		limit = 1
	}
	if limit > policy.MaxShortlistCeiling {
		limit = policy.MaxShortlistCeiling
	}

	pool := make([]qualityCandidate, 0, len(ordered))
	for _, candidate := range ordered {
		if candidate.assessment.Disposition != Blocked {
			pool = append(pool, candidate)
		}
	}

	picked := make([]qualityCandidate, 0, limit)
	taken := make(map[string]struct{}, limit)

	take := func(candidate qualityCandidate) {
		if len(picked) >= limit {
			return
		}
		key := candidate.assessment.SpecimenID + "\x00" + string(candidate.assessment.ReuseMode)
		if _, duplicate := taken[key]; duplicate {
			return
		}
		taken[key] = struct{}{}
		picked = append(picked, candidate)
	}

	// First diversify by reuse mode across automatically selectable options.
	seenModes := make(map[model.ReuseMode]struct{}, 4)
	for _, candidate := range pool {
		if dispositionRank(candidate.assessment.Disposition) > 1 {
			continue
		}
		if _, seen := seenModes[candidate.assessment.ReuseMode]; seen {
			continue
		}
		seenModes[candidate.assessment.ReuseMode] = struct{}{}
		take(candidate)
	}
	// Then fill remaining slots from the selectable pool in order.
	for _, candidate := range pool {
		if dispositionRank(candidate.assessment.Disposition) > 1 {
			continue
		}
		take(candidate)
	}
	// Then surface anything still awaiting verification or review.
	for _, candidate := range pool {
		take(candidate)
	}

	shortlist := make([]CandidateAssessment, 0, len(picked))
	for _, candidate := range picked {
		shortlist = append(shortlist, candidate.assessment)
	}
	return shortlist
}

// selectedResolution builds the durable Resolution for a selected candidate.
func selectedResolution(
	in QualityInput,
	ordered []qualityCandidate,
	selection int,
	exclusions map[string]policy.Exclusion,
	pol policy.Policy,
	note string,
) model.Resolution {
	selected := ordered[selection]
	resolution := newResolution(in, pol)

	resolution.SpecimenID = selected.assessment.SpecimenID
	resolution.Outcome = reuseModeOutcomes[selected.option.ReuseMode]

	if selected.assessment.Disposition == ReferenceOnly {
		resolution.Reasons = append(resolution.Reasons,
			fmt.Sprintf("candidate %q selected as reference-only engineering knowledge", selected.assessment.SpecimenID),
			"behavioural contract satisfaction is not established",
			fmt.Sprintf("candidate considered as reuse mode %q", selected.option.ReuseMode),
		)
	} else {
		resolution.Reasons = append(resolution.Reasons,
			fmt.Sprintf("candidate %q satisfies all required contract requirements", selected.assessment.SpecimenID),
			fmt.Sprintf("candidate assessed as %q under policy %s", selected.assessment.Disposition, pol.ID),
			fmt.Sprintf("candidate considered as reuse mode %q", selected.option.ReuseMode),
		)
		resolution.Reasons = append(resolution.Reasons,
			optionalKnownLimitations(in.Contract, selected.assessment.Behaviour)...)
	}
	if note != "" {
		resolution.Reasons = append(resolution.Reasons, note)
	}

	resolution.Unknowns = requiredUnknowns(in.Contract, selected)

	for index, candidate := range ordered {
		if index == selection {
			continue
		}
		reasons := qualityRejectionReasons(in.Contract, candidate, exclusions[candidate.assessment.SpecimenID], selected.assessment)
		resolution.Rejected = append(resolution.Rejected, model.Rejection{
			SpecimenID: candidate.assessment.SpecimenID,
			Reasons:    reasons,
		})
	}
	resolution.EvidenceIDs = selected.assessment.EvidenceIDs
	return resolution
}

// buildLocallyResolution records why local code is the answer. Each reason is
// specific: a generic "no candidate worked" would hide the actual negative
// knowledge.
func buildLocallyResolution(
	in QualityInput,
	ordered []qualityCandidate,
	exclusions map[string]policy.Exclusion,
	pol policy.Policy,
) model.Resolution {
	resolution := newResolution(in, pol)
	resolution.Outcome = model.OutcomeBuildLocally

	switch {
	case len(ordered) == 0:
		resolution.Reasons = []string{"no candidate options were supplied"}
	case excludedAll(ordered, exclusions):
		resolution.Reasons = []string{"all remaining candidate options were excluded by user feedback"}
	default:
		resolution.Reasons = buildLocallyReasons(in.Contract, ordered, exclusions)
	}

	for _, candidate := range ordered {
		reasons := qualityRejectionReasons(in.Contract, candidate, exclusions[candidate.assessment.SpecimenID], CandidateAssessment{})
		resolution.Rejected = append(resolution.Rejected, model.Rejection{
			SpecimenID: candidate.assessment.SpecimenID,
			Reasons:    reasons,
		})
	}
	return resolution
}

func buildLocallyReasons(contract model.Contract, ordered []qualityCandidate, exclusions map[string]policy.Exclusion) []string {
	anyPolicyDeny := false
	anyBehaviourFailure := false
	for _, candidate := range ordered {
		if _, excluded := exclusions[candidate.assessment.SpecimenID]; excluded {
			continue
		}
		for _, decision := range candidate.assessment.Policy {
			if decision.Action == policy.ActionDeny {
				anyPolicyDeny = true
			}
		}
		status := statusByRequirementID(candidate.assessment.Behaviour)
		for _, requirement := range contract.Requirements {
			if !requirement.Required {
				continue
			}
			if status[requirement.ID] == RequirementFailed || status[requirement.ID] == RequirementConflicting {
				anyBehaviourFailure = true
			}
		}
	}

	reasons := make([]string, 0, 4)
	if len(exclusions) > 0 {
		reasons = append(reasons, "candidate options were excluded by user feedback")
	}
	if anyPolicyDeny {
		reasons = append(reasons, "all remaining candidate options were denied by policy")
	}
	if anyBehaviourFailure {
		reasons = append(reasons, "all remaining candidate options had explicit required behavioural failures")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no candidate option satisfied all required contract requirements")
	}
	return reasons
}

func excludedAll(ordered []qualityCandidate, exclusions map[string]policy.Exclusion) bool {
	if len(ordered) == 0 || len(exclusions) == 0 {
		return false
	}
	for _, candidate := range ordered {
		if _, excluded := exclusions[candidate.assessment.SpecimenID]; !excluded {
			return false
		}
	}
	return true
}

// rejectionReasons explains one non-selected candidate without inventing a
// quality judgement.
func qualityRejectionReasons(contract model.Contract, candidate qualityCandidate, exclusion policy.Exclusion, selected CandidateAssessment) []string {
	if exclusion.CandidateID != "" {
		return []string{exclusion.RejectionReason()}
	}
	if selected.SpecimenID != "" && candidate.assessment.SpecimenID == selected.SpecimenID {
		return nil
	}

	assessment := candidate.assessment
	reasons := make([]string, 0)
	switch assessment.Disposition {
	case Blocked:
		reasons = blockedReasons(contract, candidate)
	case NeedsVerification:
		reasons = append(reasons, "not selected: required behavioural evidence is missing")
	case NeedsReview:
		reasons = append(reasons, "not selected: policy review is unresolved")
	case ImplementationEligible:
		reasons = append(reasons, "implementation-eligible but a higher-ranked candidate was selected by deterministic ordering")
	case ReferenceOnly:
		reasons = append(reasons, "reference-only candidate not selected")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "not selected")
	}
	return reasons
}

func blockedReasons(contract model.Contract, candidate qualityCandidate) []string {
	assessment := candidate.assessment
	reasons := make([]string, 0)

	if !declaresReuseMode(candidate.option.Specimen, assessment.ReuseMode) {
		reasons = append(reasons, fmt.Sprintf("specimen does not declare reuse mode %q", assessment.ReuseMode))
	}
	for _, decision := range assessment.Policy {
		if decision.Action == policy.ActionDeny {
			reasons = append(reasons, decision.Reason)
		}
	}

	status := statusByRequirementID(assessment.Behaviour)
	for _, requirement := range contract.Requirements {
		if !requirement.Required {
			continue
		}
		switch status[requirement.ID] {
		case RequirementFailed, RequirementConflicting:
			reasons = append(reasons, fmt.Sprintf("required requirement %q %s", requirement.ID, status[requirement.ID]))
		}
	}

	if assessment.ReuseMode == model.ReuseReference &&
		!candidate.facts.DiscoveryRelevance.Matched &&
		len(reasons) == 0 {
		reasons = append(reasons, "no attributable discovery or relevance observation supports this candidate as a reference")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "blocked by policy")
	}
	return reasons
}

func newResolution(in QualityInput, pol policy.Policy) model.Resolution {
	return model.Resolution{
		PrimitiveID:        in.Primitive.ID,
		ContractID:         in.Contract.ID,
		Reasons:            []string{},
		Rejected:           []model.Rejection{},
		Unknowns:           []string{},
		EvidenceIDs:        []string{},
		PolicyID:           pol.ID,
		ProjectID:          in.ProjectID,
		ProjectContextHash: in.ProjectContextHash,
		ResolvedAt:         in.Now,
	}
}

// requiredUnknowns records the unresolved required requirements of the
// selected candidate in the same format the Packet 4 kernel uses, so a
// REFERENCE resolution visibly preserves what was never established.
func requiredUnknowns(contract model.Contract, selected qualityCandidate) []string {
	status := statusByRequirementID(selected.assessment.Behaviour)
	unknowns := make([]string, 0)
	for _, requirement := range contract.Requirements {
		if !requirement.Required {
			continue
		}
		current := status[requirement.ID]
		if current == RequirementUnknown || current == RequirementConflicting {
			unknowns = append(unknowns, specimenUnknown(selected.assessment.SpecimenID, requirement.ID, current))
		}
	}
	return unknowns
}

func requiredStatuses(contract model.Contract, evaluation CandidateEvaluation) map[string]RequirementStatus {
	status := statusByRequirementID(evaluation)
	required := make(map[string]RequirementStatus, len(contract.Requirements))
	for _, requirement := range contract.Requirements {
		if requirement.Required {
			required[requirement.ID] = status[requirement.ID]
		}
	}
	return required
}

func requiredSummary(required map[string]RequirementStatus) (hasFailed, hasUnknown bool) {
	for _, status := range required {
		switch status {
		case RequirementFailed, RequirementConflicting:
			hasFailed = true
		case RequirementUnknown:
			hasUnknown = true
		}
	}
	return hasFailed, hasUnknown
}

func allRequiredSatisfied(required map[string]RequirementStatus) bool {
	for _, status := range required {
		if status != RequirementSatisfied {
			return false
		}
	}
	return true
}

func behaviouralUnknowns(contract model.Contract, evaluation CandidateEvaluation, required map[string]RequirementStatus) []Tradeoff {
	status := statusByRequirementID(evaluation)
	unknowns := make([]Tradeoff, 0)
	for _, requirement := range contract.Requirements {
		if !requirement.Required {
			continue
		}
		current, tracked := required[requirement.ID]
		if !tracked {
			current = status[requirement.ID]
		}
		if current != RequirementUnknown && current != RequirementConflicting {
			continue
		}
		unknowns = append(unknowns, Tradeoff{
			Dimension:   "behaviour",
			Message:     fmt.Sprintf("%s remains behaviourally %s", requirement.ID, current),
			EvidenceIDs: evaluationEvidenceFor(evaluation, requirement.ID),
		})
	}
	return unknowns
}

func factUnknowns(facts policy.Facts) []Tradeoff {
	unknowns := make([]Tradeoff, 0, 8)
	appendUnknown := func(dimension, message string, established bool, ids []string) {
		if established {
			return
		}
		unknowns = append(unknowns, Tradeoff{Dimension: dimension, Message: message, EvidenceIDs: ids})
	}
	appendUnknown(policy.DimensionLicence, "licence could not be established",
		facts.Licence.Status != policy.FactUnknown, facts.Licence.EvidenceIDs)
	appendUnknown(policy.DimensionSecurity, "advisory state is not established",
		facts.Advisory.Status != policy.FactUnknown, facts.Advisory.EvidenceIDs)
	appendUnknown("dependencies", "direct dependency count is not established",
		facts.Dependency.DirectCountKnown, facts.Dependency.EvidenceIDs)
	appendUnknown(policy.DimensionMaintenanceArchived, "repository archived state is not established",
		facts.Archived.Status != policy.FactUnknown, facts.Archived.EvidenceIDs)
	appendUnknown(policy.DimensionMaintenanceDeprecated, "package deprecation state is not established",
		facts.Deprecated.Status != policy.FactUnknown, facts.Deprecated.EvidenceIDs)
	appendUnknown(policy.DimensionMaintenanceStale, "last push time is not established",
		facts.LastPush.Status != policy.FactUnknown, facts.LastPush.EvidenceIDs)
	appendUnknown(policy.DimensionSourceRevision, "source revision is not pinned",
		facts.Revision.Status != policy.FactUnknown, facts.Revision.EvidenceIDs)
	appendUnknown("discovery_relevance", "no discovery relevance observation exists",
		facts.DiscoveryRelevance.Status != policy.FactUnknown, facts.DiscoveryRelevance.EvidenceIDs)
	return unknowns
}

func evaluationEvidenceFor(evaluation CandidateEvaluation, requirementID string) []string {
	for _, requirement := range evaluation.Requirements {
		if requirement.RequirementID == requirementID {
			return requirement.EvidenceIDs
		}
	}
	return nil
}

// assessmentEvidenceIDs records behavioural determining evidence first, in
// requirement order, then the evidence behind each policy decision, in
// decision order. Popularity signals never appear here because they are never
// collected.
func assessmentEvidenceIDs(evaluation CandidateEvaluation, decisions []policy.PolicyDecision) []string {
	seen := make(map[string]struct{}, len(evaluation.Requirements)*2)
	ids := make([]string, 0, len(evaluation.Requirements)*2)
	add := func(values []string) {
		for _, value := range values {
			if value == "" {
				continue
			}
			if _, duplicate := seen[value]; duplicate {
				continue
			}
			seen[value] = struct{}{}
			ids = append(ids, value)
		}
	}
	for _, requirement := range evaluation.Requirements {
		add(requirement.EvidenceIDs)
	}
	for _, decision := range decisions {
		add(decision.EvidenceIDs)
	}
	return ids
}
