package policy

import (
	"errors"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// FeedbackReason is one bounded, supported "Not quite" vocabulary entry. Only
// reasons this packet can actually act on are accepted: exposing a reason that
// changes nothing would be a lie about what the product does.
type FeedbackReason string

const (
	// FeedbackNotQuite excludes only the named candidate. It is the generic
	// escape hatch and deliberately infers no broader preference.
	FeedbackNotQuite FeedbackReason = "not_quite"
	// FeedbackTooManyDependencies tightens dependencies.max_direct below the
	// rejected candidate's observed direct dependency count.
	FeedbackTooManyDependencies FeedbackReason = "too_many_dependencies"
	// FeedbackLicenceNotAllowed denies the rejected candidate's single known
	// licence expression.
	FeedbackLicenceNotAllowed FeedbackReason = "licence_not_allowed"
	// FeedbackAvoidDependency removes dependency from the allowed reuse modes.
	FeedbackAvoidDependency FeedbackReason = "avoid_dependency"
	// FeedbackAvoidReference removes reference from the allowed reuse modes.
	FeedbackAvoidReference FeedbackReason = "avoid_reference"
	// FeedbackArchivedProject hard-denies archived repositories.
	FeedbackArchivedProject FeedbackReason = "archived_project"
)

// FeedbackReasons returns the supported vocabulary in a stable order.
func FeedbackReasons() []FeedbackReason {
	return []FeedbackReason{
		FeedbackNotQuite,
		FeedbackTooManyDependencies,
		FeedbackLicenceNotAllowed,
		FeedbackAvoidDependency,
		FeedbackAvoidReference,
		FeedbackArchivedProject,
	}
}

// SupportedFeedback reports whether a reason can actually change resolution
// behaviour in this packet.
func SupportedFeedback(reason FeedbackReason) bool {
	for _, supported := range FeedbackReasons() {
		if supported == reason {
			return true
		}
	}
	return false
}

// Feedback errors. Every one of these is a structural feedback problem: the
// caller reports it as a usage failure rather than guessing an intent.
var (
	ErrUnknownFeedbackCandidate = errors.New("policy: feedback names a candidate that is not in the request")
	ErrUnsupportedFeedback      = errors.New("policy: unsupported feedback reason")
	ErrDependencyCountUnknown   = errors.New("policy: too_many_dependencies needs a known direct dependency count")
	ErrDependencyCountZero      = errors.New("policy: too_many_dependencies cannot refine a candidate with zero direct dependencies")
	ErrNotArchived              = errors.New("policy: archived_project requires an observation that the candidate is archived")
)

// Feedback is one structured "Not quite" entry. It always excludes the named
// candidate and, where the facts allow it, also refines the effective policy
// for the remaining candidates.
type Feedback struct {
	CandidateID string         `json:"candidate_id"`
	Reason      FeedbackReason `json:"reason"`
}

// AppliedFeedback is the inspectable record of what one entry actually did.
type AppliedFeedback struct {
	CandidateID string         `json:"candidate_id"`
	Reason      FeedbackReason `json:"reason"`
	// Refinement describes the effective policy change, empty when the entry
	// only excluded a candidate.
	Refinement string `json:"refinement,omitempty"`
	// Warning records a refinement that was impossible for a factual reason.
	Warning string `json:"warning,omitempty"`
}

// Exclusion is negative knowledge: a candidate the user removed.
type Exclusion struct {
	CandidateID string
	Reason      FeedbackReason
	Context     string
}

// RejectionReason renders the negative knowledge for model.Resolution.Rejected.
func (e Exclusion) RejectionReason() string {
	reason := "user_feedback:" + string(e.Reason)
	if e.Context != "" {
		reason += ": " + e.Context
	}
	return reason
}

// FeedbackOutcome is the refined policy plus what changed and who was removed.
type FeedbackOutcome struct {
	Policy   Policy
	Applied  []AppliedFeedback
	Excluded []Exclusion
}

// ApplyFeedback applies structured feedback in authored order.
//
// Two properties matter. First, feedback changes the effective policy rather
// than advancing to the next array element: a refinement such as max_direct is
// evaluated again for every remaining candidate. Second, every excluded
// candidate is preserved as negative knowledge so a later resolution can say
// why it is gone.
//
// known maps candidate ID to its extracted facts; a feedback entry naming a
// candidate outside that map is a structural error, never a silent no-op.
func ApplyFeedback(p Policy, known map[string]Facts, feedback []Feedback) (FeedbackOutcome, error) {
	outcome := FeedbackOutcome{Policy: p, Applied: []AppliedFeedback{}, Excluded: []Exclusion{}}
	applied := make(map[Feedback]struct{}, len(feedback))

	for _, entry := range feedback {
		if !SupportedFeedback(entry.Reason) {
			return FeedbackOutcome{}, fmt.Errorf("%w: %q", ErrUnsupportedFeedback, entry.Reason)
		}
		facts, ok := known[entry.CandidateID]
		if !ok {
			return FeedbackOutcome{}, fmt.Errorf("%w: %q", ErrUnknownFeedbackCandidate, entry.CandidateID)
		}
		if _, duplicate := applied[entry]; duplicate {
			continue
		}
		applied[entry] = struct{}{}

		record := AppliedFeedback{CandidateID: entry.CandidateID, Reason: entry.Reason}
		switch entry.Reason {
		case FeedbackNotQuite:
			// Exclusion only: no broader preference is inferred.
		case FeedbackTooManyDependencies:
			next, err := refineMaxDirect(outcome.Policy, facts)
			if err != nil {
				return FeedbackOutcome{}, fmt.Errorf("%w (candidate %q)", err, entry.CandidateID)
			}
			outcome.Policy.Dependencies.MaxDirect = next
			record.Refinement = fmt.Sprintf("dependencies.max_direct set to %d", *next)
		case FeedbackLicenceNotAllowed:
			if facts.Licence.Status != FactKnown || len(facts.Licence.Values) != 1 {
				record.Warning = fmt.Sprintf("licence identity for %q is not a single established expression, so no broader licence rule was derived; the candidate is excluded only", entry.CandidateID)
				break
			}
			value := facts.Licence.Values[0]
			if !containsString(outcome.Policy.Licence.Deny, value) {
				outcome.Policy.Licence.Deny = append(append([]string(nil), outcome.Policy.Licence.Deny...), value)
			}
			record.Refinement = fmt.Sprintf("licence %q added to the deny list", value)
		case FeedbackAvoidDependency:
			outcome.Policy.Reuse.Allowed = removeMode(outcome.Policy.Reuse.Allowed, model.ReuseDependency)
			record.Refinement = `reuse mode "dependency" removed from the allowed set`
		case FeedbackAvoidReference:
			outcome.Policy.Reuse.Allowed = removeMode(outcome.Policy.Reuse.Allowed, model.ReuseReference)
			record.Refinement = `reuse mode "reference" removed from the allowed set`
		case FeedbackArchivedProject:
			if !facts.Archived.Archived {
				return FeedbackOutcome{}, fmt.Errorf("%w: %q is not observed as archived", ErrNotArchived, entry.CandidateID)
			}
			outcome.Policy.Maintenance.Archived = ActionDeny
			record.Refinement = "maintenance.archived set to deny"
		}

		outcome.Applied = append(outcome.Applied, record)
		outcome.Excluded = append(outcome.Excluded, Exclusion{
			CandidateID: entry.CandidateID,
			Reason:      entry.Reason,
			Context:     record.Refinement,
		})
	}
	return outcome, nil
}

// refineMaxDirect returns the next effective maximum: the observed count minus
// one, unless an existing stricter maximum already applies. An unknown or zero
// count is a structured error rather than an invented threshold.
func refineMaxDirect(p Policy, facts Facts) (*int, error) {
	if !facts.Dependency.DirectCountKnown {
		return nil, fmt.Errorf("%w: direct dependency count is unknown", ErrDependencyCountUnknown)
	}
	if facts.Dependency.DirectCount == 0 {
		return nil, fmt.Errorf("%w: direct dependency count is zero", ErrDependencyCountZero)
	}
	next := facts.Dependency.DirectCount - 1
	if current := p.Dependencies.MaxDirect; current != nil && *current < next {
		next = *current
	}
	return &next, nil
}

// removeMode returns the allowed set without one mode. An empty (unrestricted)
// set is materialised first, so removing a mode from an unrestricted policy
// still means something.
func removeMode(configured []model.ReuseMode, remove model.ReuseMode) []model.ReuseMode {
	base := allowedModes(configured)
	out := make([]model.ReuseMode, 0, len(base))
	for _, mode := range base {
		if mode != remove {
			out = append(out, mode)
		}
	}
	return out
}
