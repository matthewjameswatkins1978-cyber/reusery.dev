// Package policy applies explicit, deterministic project rules to typed facts
// extracted from stored evidence.
//
// Three boundaries matter here. A policy decision is not Evidence: it never
// satisfies, fails or refutes a behavioural contract requirement. A policy
// decision is not a score: outcomes are allow, review or deny with a reason,
// never a number. And a policy decision is not legal advice: licence wording is
// always "allowed by policy <id>" or "denied by policy <id>", never "licence
// compatible".
//
// The package reads facts, never English: every machine-consumable value
// arrives through the versioned fact artifact convention, never by parsing a
// human-readable Evidence.Claim.
package policy

import (
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// SchemaVersion is the only policy profile schema supported for now.
const SchemaVersion = 1

// Action is the deterministic outcome of one policy rule. There is no
// numerical weighting and no ordering beyond allow > review > deny for
// inspection purposes.
type Action string

const (
	ActionAllow  Action = "allow"
	ActionReview Action = "review"
	ActionDeny   Action = "deny"
)

// ValidAction reports whether the string is one of the three supported
// actions.
func ValidAction(action Action) bool {
	switch action {
	case ActionAllow, ActionReview, ActionDeny:
		return true
	default:
		return false
	}
}

// CountActions tallies deny and review decisions. Review deliberately does not
// count as a deny: review is not deny, and it is not automatic approval
// either — it only blocks automatic direct selection.
func CountActions(decisions []PolicyDecision) (denies, reviews int) {
	for _, decision := range decisions {
		switch decision.Action {
		case ActionDeny:
			denies++
		case ActionReview:
			reviews++
		}
	}
	return denies, reviews
}

// Policy is one authored, validated rule profile.
type Policy struct {
	SchemaVersion int
	ID            string

	Reuse        ReusePolicy
	Licence      LicencePolicy
	Security     SecurityPolicy
	Dependencies DependencyPolicy
	Maintenance  MaintenancePolicy
	Source       SourcePolicy
	Selection    SelectionPolicy
}

// ReusePolicy states which reuse modes are permitted and, optionally, which
// order the author prefers. Preferred order only breaks ties between otherwise
// equivalent implementation-eligible candidates; it is not a ranking score.
type ReusePolicy struct {
	Allowed   []model.ReuseMode
	Preferred []model.ReuseMode
}

// LicencePolicy applies exact SPDX strings. Reusery does not parse SPDX and
// does not evaluate legal compatibility: it compares the exact expression a
// provider returned against exact authored lists. Conflicting evidence applies
// the Multiple action, because this packet defines no separate conflict rule.
type LicencePolicy struct {
	Allow    []string
	Deny     []string
	Unknown  Action
	Multiple Action
	Unlisted Action
}

// SecurityPolicy covers observed advisory identifiers. A reported zero is an
// explicit allow for one narrow statement only: "no known direct advisory IDs
// were reported by this source at this time". It is never a pass and never a
// security claim.
type SecurityPolicy struct {
	KnownAdvisory Action
	Unknown       Action
}

// DependencyPolicy applies only when MaxDirect is configured. When it is nil
// the dependency count is reported as a trade-off and no quantity decision is
// made, because no universal "good dependency count" exists.
type DependencyPolicy struct {
	Unknown   Action
	MaxDirect *int
}

// MaintenancePolicy evaluates archived and deprecated facts always, and stale
// thresholds only when a threshold is configured. Absent thresholds mean no
// threshold: no default day count is invented.
type MaintenancePolicy struct {
	Archived            Action
	Deprecated          Action
	Stale               Action
	Unknown             Action
	MaxDaysSincePush    *int
	MaxDaysSinceRelease *int
}

// SourcePolicy governs provenance. A pinned or versioned revision is stronger
// provenance than an unpinned reference, but it is never evidence that the code
// itself is better.
type SourcePolicy struct {
	RequireRevisionFor []model.ReuseMode
	MissingRevision    Action
}

// SelectionPolicy bounds the shortlist. The loader enforces 1..5 so a profile
// cannot ask for an unbounded dump of every candidate.
type SelectionPolicy struct {
	MaxOptions int
}

// PolicyDecision is one deterministic rule outcome. Ordering is fixed and
// inspectable; there is no score, weight or confidence attached.
type PolicyDecision struct {
	Dimension   string   `json:"dimension"`
	Action      Action   `json:"action"`
	Reason      string   `json:"reason"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// Decision dimensions, in the fixed order Evaluate emits them.
const (
	DimensionReuse                 = "reuse"
	DimensionLicence               = "licence"
	DimensionSecurity              = "security"
	DimensionDependencies          = "dependencies"
	DimensionMaintenanceArchived   = "maintenance_archived"
	DimensionMaintenanceDeprecated = "maintenance_deprecated"
	DimensionMaintenanceStale      = "maintenance_stale"
	DimensionSourceRevision        = "source_revision"
)

// EffectiveSummary is the inspectable projection of a policy that a run
// actually applied. It exists so a caller can show what feedback changed
// without dumping every authored rule.
type EffectiveSummary struct {
	ID                  string            `json:"id"`
	SchemaVersion       int               `json:"schema_version"`
	AllowedReuseModes   []model.ReuseMode `json:"allowed_reuse_modes"`
	PreferredReuseModes []model.ReuseMode `json:"preferred_reuse_modes,omitempty"`
	LicenceDeny         []string          `json:"licence_deny,omitempty"`
	LicenceAllow        []string          `json:"licence_allow,omitempty"`
	MaxDirect           *int              `json:"max_direct,omitempty"`
	ArchivedAction      Action            `json:"archived_action"`
	MaxDaysSincePush    *int              `json:"max_days_since_push,omitempty"`
	MaxDaysSinceRelease *int              `json:"max_days_since_release,omitempty"`
	MaxOptions          int               `json:"max_options"`
}

// Summarize renders the effective policy for inspection.
func Summarize(p Policy) EffectiveSummary {
	return EffectiveSummary{
		ID:                  p.ID,
		SchemaVersion:       p.SchemaVersion,
		AllowedReuseModes:   append([]model.ReuseMode(nil), allowedModes(p.Reuse.Allowed)...),
		PreferredReuseModes: append([]model.ReuseMode(nil), p.Reuse.Preferred...),
		LicenceDeny:         append([]string(nil), p.Licence.Deny...),
		LicenceAllow:        append([]string(nil), p.Licence.Allow...),
		MaxDirect:           p.Dependencies.MaxDirect,
		ArchivedAction:      p.Maintenance.Archived,
		MaxDaysSincePush:    p.Maintenance.MaxDaysSincePush,
		MaxDaysSinceRelease: p.Maintenance.MaxDaysSinceRelease,
		MaxOptions:          p.Selection.MaxOptions,
	}
}

// DecisionDimensions returns the dimension order Evaluate uses, so callers and
// tests never have to hard-code it.
func DecisionDimensions() []string {
	return []string{
		DimensionReuse,
		DimensionLicence,
		DimensionSecurity,
		DimensionDependencies,
		DimensionMaintenanceArchived,
		DimensionMaintenanceDeprecated,
		DimensionMaintenanceStale,
		DimensionSourceRevision,
	}
}

// ReuseModes returns the canonical reuse-mode vocabulary, used by validation.
func ReuseModes() []model.ReuseMode {
	return []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference}
}
