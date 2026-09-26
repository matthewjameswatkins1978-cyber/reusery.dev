package policy

import (
	"fmt"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// ModeAllowed reports whether the policy permits one reuse mode. An empty
// allowed list means no restriction.
func (p Policy) ModeAllowed(mode model.ReuseMode) bool {
	return containsMode(allowedModes(p.Reuse.Allowed), mode)
}

// PreferredIndex returns the authored preference position for a reuse mode,
// or the length of the preferred list when the author expressed no preference
// for that mode. Lower sorts first; an empty preferred list ranks every mode
// equally.
func (p Policy) PreferredIndex(mode model.ReuseMode) int {
	for index, candidate := range p.Reuse.Preferred {
		if candidate == mode {
			return index
		}
	}
	return len(p.Reuse.Preferred)
}

// RequiresRevision reports whether the policy demands a pinned or versioned
// revision for one reuse mode.
func (p Policy) RequiresRevision(mode model.ReuseMode) bool {
	return containsMode(p.Source.RequireRevisionFor, mode)
}

// Evaluate applies one policy to one candidate's typed facts under one reuse
// mode.
//
// Dimensions are emitted in a fixed order and there is no score, weight or
// confidence anywhere in the output. now is the single resolution clock: no
// rule reads the wall clock itself, so a run is reproducible.
func Evaluate(p Policy, specimen model.Specimen, mode model.ReuseMode, facts Facts, now time.Time) []PolicyDecision {
	decisions := make([]PolicyDecision, 0, 8)
	decisions = append(decisions, evaluateReuse(p, mode))
	decisions = append(decisions, evaluateLicence(p, facts.Licence))
	decisions = append(decisions, evaluateSecurity(p, facts.Advisory))
	if dependency, applicable := evaluateDependencies(p, facts.Dependency); applicable {
		decisions = append(decisions, dependency)
	}
	decisions = append(decisions, evaluateArchived(p, facts.Archived))
	decisions = append(decisions, evaluateDeprecated(p, facts.Deprecated))
	if stale, applicable := evaluateStale(p, facts, now); applicable {
		decisions = append(decisions, stale)
	}
	decisions = append(decisions, evaluateSourceRevision(p, specimen, mode, facts.Revision))
	return decisions
}

func evaluateReuse(p Policy, mode model.ReuseMode) PolicyDecision {
	if p.ModeAllowed(mode) {
		return PolicyDecision{
			Dimension: DimensionReuse,
			Action:    ActionAllow,
			Reason:    fmt.Sprintf("reuse mode %q is allowed by policy %s", mode, p.ID),
		}
	}
	return PolicyDecision{
		Dimension: DimensionReuse,
		Action:    ActionDeny,
		Reason:    fmt.Sprintf("reuse mode %q is denied by policy %s", mode, p.ID),
	}
}

// evaluateLicence applies exact-string licence rules. Reusery never says a
// licence is "compatible": the strongest permitted wording is "allowed by
// policy <id>".
func evaluateLicence(p Policy, fact LicenceFact) PolicyDecision {
	decision := PolicyDecision{Dimension: DimensionLicence, EvidenceIDs: fact.EvidenceIDs}

	switch {
	case fact.Status == FactMultiple, fact.Status == FactConflicting:
		decision.Action = p.Licence.Multiple
		if fact.Status == FactConflicting {
			decision.Reason = fmt.Sprintf("stored observations record conflicting licence values %s; policy %s applies %s",
				strings.Join(quoteAll(fact.Values), ", "), p.ID, decision.Action)
		} else {
			decision.Reason = fmt.Sprintf("stored observations record %d licence expressions whose relationship is not established; policy %s applies %s",
				len(fact.Values), p.ID, decision.Action)
		}
	case fact.Status == FactKnown && len(fact.Values) == 1:
		licence := fact.Values[0]
		switch {
		case containsString(p.Licence.Deny, licence):
			decision.Action = ActionDeny
			decision.Reason = fmt.Sprintf("licence %q is denied by policy %s", licence, p.ID)
		case len(p.Licence.Allow) > 0 && containsString(p.Licence.Allow, licence):
			decision.Action = ActionAllow
			decision.Reason = fmt.Sprintf("licence %q is allowed by policy %s", licence, p.ID)
		case len(p.Licence.Allow) > 0:
			decision.Action = p.Licence.Unlisted
			decision.Reason = fmt.Sprintf("licence %q is not in the allow list of policy %s, which applies %s",
				licence, p.ID, decision.Action)
		default:
			decision.Action = ActionAllow
			decision.Reason = fmt.Sprintf("licence %q is allowed by policy %s", licence, p.ID)
		}
	default:
		decision.Action = p.Licence.Unknown
		decision.Reason = fmt.Sprintf("source licence could not be established; policy %s applies %s", p.ID, decision.Action)
	}
	return decision
}

// evaluateSecurity treats observed advisory identifiers as a review trigger and
// a reported zero as an allow for exactly one narrow statement. Neither is a
// pass, and neither is ever worded as a security claim.
func evaluateSecurity(p Policy, fact AdvisoryFact) PolicyDecision {
	decision := PolicyDecision{Dimension: DimensionSecurity, EvidenceIDs: fact.EvidenceIDs}

	observed := len(fact.KnownIDs) > 0 || (fact.CountKnown && fact.Count > 0)
	switch {
	case observed:
		decision.Action = p.Security.KnownAdvisory
		if len(fact.KnownIDs) > 0 {
			decision.Reason = fmt.Sprintf("%d known direct advisory ID(s) reported for this version (%s); policy %s applies %s",
				len(fact.KnownIDs), strings.Join(fact.KnownIDs, ", "), p.ID, decision.Action)
		} else {
			decision.Reason = fmt.Sprintf("an advisory source reported %d known direct advisory identifiers for this version; policy %s applies %s",
				fact.Count, p.ID, decision.Action)
		}
	case fact.CountKnown:
		decision.Action = ActionAllow
		decision.Reason = "no known direct advisory IDs were reported by this source at this time; " +
			"policy " + p.ID + " allows this candidate for that narrow statement only"
	default:
		decision.Action = p.Security.Unknown
		decision.Reason = fmt.Sprintf("advisory state is not established by stored evidence; policy %s applies %s", p.ID, decision.Action)
	}
	return decision
}

func evaluateDependencies(p Policy, fact DependencyFact) (PolicyDecision, bool) {
	decision := PolicyDecision{Dimension: DimensionDependencies, EvidenceIDs: fact.EvidenceIDs}
	if p.Dependencies.MaxDirect == nil {
		// No threshold means no quantity decision: the count is a trade-off,
		// not a rule. No universal "good dependency count" is invented.
		return decision, false
	}
	maximum := *p.Dependencies.MaxDirect

	switch {
	case !fact.DirectCountKnown:
		decision.Action = p.Dependencies.Unknown
		decision.Reason = fmt.Sprintf("direct dependency count is not established by stored evidence while policy %s configures maximum %d; policy applies %s",
			p.ID, maximum, decision.Action)
	case fact.DirectCount > maximum:
		decision.Action = ActionDeny
		decision.Reason = fmt.Sprintf("direct dependency count %d exceeds configured maximum %d of policy %s",
			fact.DirectCount, maximum, p.ID)
	default:
		decision.Action = ActionAllow
		decision.Reason = fmt.Sprintf("direct dependency count %d is within configured maximum %d of policy %s",
			fact.DirectCount, maximum, p.ID)
	}
	return decision, true
}

func evaluateArchived(p Policy, fact ArchivedFact) PolicyDecision {
	decision := PolicyDecision{Dimension: DimensionMaintenanceArchived, EvidenceIDs: fact.EvidenceIDs}
	switch fact.Status {
	case FactConflicting:
		decision.Action = p.Maintenance.Archived
		decision.Reason = fmt.Sprintf("stored observations disagree about whether the repository is archived; policy %s applies %s", p.ID, decision.Action)
	case FactKnown:
		if fact.Archived {
			decision.Action = p.Maintenance.Archived
			decision.Reason = fmt.Sprintf("repository is archived; policy %s applies %s", p.ID, decision.Action)
			return decision
		}
		decision.Action = ActionAllow
		decision.Reason = fmt.Sprintf("repository is not reported archived; policy %s allows this candidate", p.ID)
	default:
		decision.Action = p.Maintenance.Unknown
		decision.Reason = fmt.Sprintf("repository archived state is not established by stored evidence; policy %s applies %s", p.ID, decision.Action)
	}
	return decision
}

func evaluateDeprecated(p Policy, fact DeprecatedFact) PolicyDecision {
	decision := PolicyDecision{Dimension: DimensionMaintenanceDeprecated, EvidenceIDs: fact.EvidenceIDs}
	switch fact.Status {
	case FactConflicting:
		decision.Action = p.Maintenance.Deprecated
		decision.Reason = fmt.Sprintf("stored observations disagree about whether the package is deprecated; policy %s applies %s", p.ID, decision.Action)
	case FactKnown:
		if fact.Deprecated {
			reason := "package is reported deprecated"
			if fact.Reason != "" {
				reason += fmt.Sprintf(" (%s)", fact.Reason)
			}
			decision.Action = p.Maintenance.Deprecated
			decision.Reason = reason + fmt.Sprintf("; policy %s applies %s", p.ID, decision.Action)
			return decision
		}
		decision.Action = ActionAllow
		decision.Reason = fmt.Sprintf("package is not reported deprecated; policy %s allows this candidate", p.ID)
	default:
		decision.Action = p.Maintenance.Unknown
		decision.Reason = fmt.Sprintf("package deprecation state is not established by stored evidence; policy %s applies %s", p.ID, decision.Action)
	}
	return decision
}

// evaluateStale returns the maintenance staleness decision, and reports whether
// the policy configured any threshold at all. Absent thresholds mean no
// threshold: no default day count exists anywhere in this package.
func evaluateStale(p Policy, facts Facts, now time.Time) (PolicyDecision, bool) {
	decision := PolicyDecision{Dimension: DimensionMaintenanceStale}
	if p.Maintenance.MaxDaysSincePush == nil && p.Maintenance.MaxDaysSinceRelease == nil {
		return decision, false
	}
	decision.EvidenceIDs = append(decision.EvidenceIDs, facts.LastPush.EvidenceIDs...)
	decision.EvidenceIDs = append(decision.EvidenceIDs, facts.PublishedAt.EvidenceIDs...)

	type check struct {
		label    string
		known    bool
		age      int
		observed time.Time
		limit    int
	}
	checks := make([]check, 0, 2)
	if limit := p.Maintenance.MaxDaysSincePush; limit != nil {
		age := -1
		if facts.LastPush.Known {
			age = daysBetween(now, facts.LastPush.PushedAt)
		}
		checks = append(checks, check{label: "last push", known: facts.LastPush.Known, age: age, observed: facts.LastPush.PushedAt, limit: *limit})
	}
	if limit := p.Maintenance.MaxDaysSinceRelease; limit != nil {
		age := -1
		if facts.PublishedAt.Known {
			age = daysBetween(now, facts.PublishedAt.PublishedAt)
		}
		checks = append(checks, check{label: "latest package publication", known: facts.PublishedAt.Known, age: age, observed: facts.PublishedAt.PublishedAt, limit: *limit})
	}

	for _, entry := range checks {
		if !entry.known {
			decision.Action = p.Maintenance.Unknown
			decision.Reason = fmt.Sprintf("%s is not established by stored evidence while policy %s configures a %d day limit; policy applies %s",
				entry.label, p.ID, entry.limit, decision.Action)
			return decision, true
		}
	}
	for _, entry := range checks {
		if entry.age > entry.limit {
			decision.Action = p.Maintenance.Stale
			decision.Reason = fmt.Sprintf("%s is %d days old and policy limit is %d days", entry.label, entry.age, entry.limit)
			return decision, true
		}
	}

	parts := make([]string, 0, len(checks))
	for _, entry := range checks {
		parts = append(parts, fmt.Sprintf("%s is %d days old within policy limit %d days", entry.label, entry.age, entry.limit))
	}
	decision.Action = ActionAllow
	decision.Reason = strings.Join(parts, "; ") + fmt.Sprintf("; policy %s allows this candidate", p.ID)
	return decision, true
}

// evaluateSourceRevision surfaces provenance. A pinned or versioned revision is
// stronger provenance than an unpinned reference, but never evidence that the
// code itself is better.
func evaluateSourceRevision(p Policy, specimen model.Specimen, mode model.ReuseMode, fact RevisionFact) PolicyDecision {
	decision := PolicyDecision{Dimension: DimensionSourceRevision, EvidenceIDs: fact.EvidenceIDs}
	if fact.Status == FactConflicting {
		decision.Action = p.Source.MissingRevision
		decision.Reason = fmt.Sprintf("stored observations record conflicting source revisions; policy %s applies %s", p.ID, decision.Action)
		return decision
	}
	if fact.Known {
		decision.Action = ActionAllow
		decision.Reason = fmt.Sprintf("source is pinned to revision %s", fact.Revision)
		return decision
	}

	if p.RequiresRevision(mode) {
		decision.Action = p.Source.MissingRevision
		decision.Reason = fmt.Sprintf("source revision is missing and policy %s requires a pinned or versioned revision for reuse mode %q; policy applies %s",
			p.ID, mode, decision.Action)
		return decision
	}
	decision.Action = ActionReview
	decision.Reason = fmt.Sprintf("source revision is not pinned; recorded as a provenance weakness for reuse mode %q", mode)
	return decision
}

func quoteAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%q", value))
	}
	return out
}

// daysBetween counts whole UTC days elapsed from observed to now. A future
// observation yields a negative age rather than wrapping.
func daysBetween(now, observed time.Time) int {
	if observed.IsZero() {
		return -1
	}
	return int(now.UTC().Sub(observed.UTC()).Hours() / 24)
}
