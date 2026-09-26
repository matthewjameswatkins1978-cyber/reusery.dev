package project

import (
	"fmt"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// DerivePreference turns an explicit Packet 7 feedback reason plus the stored
// facts for that candidate into one project memory.
//
// The caller never supplies a licence value, a dependency count or an archived
// boolean: Reusery already has those facts and a caller must not be able to
// invent them. When the facts cannot support the requested memory the call
// fails rather than storing something the project never established.
//
// This is only ever reached through an explicit remember operation. A refine
// call or an outcome event never creates a preference on its own.
func DerivePreference(
	projectID string,
	primitiveID string,
	candidateID string,
	reason policy.FeedbackReason,
	facts policy.Facts,
	sourceResolutionID int64,
	now time.Time,
) (Preference, error) {
	if !policy.SupportedFeedback(reason) {
		return Preference{}, fmt.Errorf("%w %q", ErrUnknownFeedbackReason, string(reason))
	}
	base := Preference{
		ProjectID:          projectID,
		PrimitiveID:        primitiveID,
		CandidateID:        candidateID,
		SourceReason:       reason,
		SourceResolutionID: sourceResolutionID,
		RecordedAt:         now,
	}

	switch reason {
	case policy.FeedbackNotQuite:
		// A poor fit for this decision. Scoped to the candidate so it can
		// never become a broader architectural preference.
		base.Kind = PrefExcludeCandidate
		return base, nil

	case policy.FeedbackTooManyDependencies:
		if !facts.Dependency.DirectCountKnown || facts.Dependency.DirectCount <= 0 {
			return Preference{}, fmt.Errorf("%w: the direct dependency count is unknown", ErrPreferenceUnsupported)
		}
		maximum := facts.Dependency.DirectCount - 1
		base.Kind = PrefMaxDirectDeps
		base.IntValue = &maximum
		return base, nil

	case policy.FeedbackLicenceNotAllowed:
		if len(facts.Licence.Values) == 1 && facts.Licence.Status == policy.FactKnown {
			base.Kind = PrefDenyLicence
			base.TextValue = facts.Licence.Values[0]
			return base, nil
		}
		// Unknown, multiple or conflicting licence: no general licence rule
		// can be invented, so only this candidate is excluded.
		base.Kind = PrefExcludeCandidate
		return base, nil

	case policy.FeedbackAvoidDependency:
		base.Kind = PrefAvoidDependency
		return base, nil

	case policy.FeedbackAvoidReference:
		base.Kind = PrefAvoidReference
		return base, nil

	case policy.FeedbackArchivedProject:
		if !facts.Archived.Known || !facts.Archived.Archived {
			return Preference{}, fmt.Errorf("%w: the candidate is not observed as archived", ErrPreferenceUnsupported)
		}
		base.Kind = PrefDenyArchived
		return base, nil
	}
	return Preference{}, fmt.Errorf("%w %q", ErrUnknownFeedbackReason, string(reason))
}

// ApplyPreferences returns a copy of base with every preference applied in a
// fixed order. The stored base policy is never mutated.
//
// exclude_candidate is not applied here: it is expressed as structured
// feedback to the quality kernel so the exclusion is visible in the decision's
// rejection reasons.
func ApplyPreferences(base policy.Policy, preferences []Preference) policy.Policy {
	effective := clonePolicy(base)

	// A fixed order keeps the result independent of how rows were returned.
	for _, kind := range PreferenceKinds() {
		for _, preference := range preferences {
			if preference.Kind != kind {
				continue
			}
			switch preference.Kind {
			case PrefAvoidDependency:
				effective.Reuse.Allowed = removeModeOrAll(effective.Reuse.Allowed, model.ReuseDependency)
				effective.Reuse.Preferred = removeModeOrAll(effective.Reuse.Preferred, model.ReuseDependency)
			case PrefAvoidReference:
				effective.Reuse.Allowed = removeModeOrAll(effective.Reuse.Allowed, model.ReuseReference)
				effective.Reuse.Preferred = removeModeOrAll(effective.Reuse.Preferred, model.ReuseReference)
			case PrefDenyLicence:
				if preference.TextValue != "" && !contains(effective.Licence.Deny, preference.TextValue) {
					effective.Licence.Deny = append(append([]string(nil), effective.Licence.Deny...), preference.TextValue)
				}
			case PrefMaxDirectDeps:
				if preference.IntValue == nil {
					continue
				}
				if effective.Dependencies.MaxDirect == nil {
					value := *preference.IntValue
					effective.Dependencies.MaxDirect = &value
				} else if *preference.IntValue < *effective.Dependencies.MaxDirect {
					value := *preference.IntValue
					effective.Dependencies.MaxDirect = &value
				}
			case PrefDenyArchived:
				effective.Maintenance.Archived = policy.ActionDeny
			}
		}
	}
	return effective
}

// ExclusionFeedback converts active exclude_candidate memories into the
// structured feedback vocabulary the quality kernel already understands.
//
// Only candidates present in the request are converted: a remembered
// exclusion for a candidate nobody is considering is irrelevant, and feeding
// it through would be rejected as an unknown candidate.
func ExclusionFeedback(preferences []Preference, present map[string]bool) []policy.Feedback {
	out := make([]policy.Feedback, 0, len(preferences))
	for _, preference := range preferences {
		if preference.Kind != PrefExcludeCandidate || preference.CandidateID == "" {
			continue
		}
		if !present[preference.CandidateID] {
			continue
		}
		reason := preference.SourceReason
		if reason == "" {
			reason = policy.FeedbackNotQuite
		}
		if !policy.SupportedFeedback(reason) {
			reason = policy.FeedbackNotQuite
		}
		out = append(out, policy.Feedback{CandidateID: preference.CandidateID, Reason: reason})
	}
	return out
}

// DependencyFits derives the bounded project dependency-fit context for a
// candidate set.
//
// Only dependency-mode candidates can match the manifest; everything else is
// not_applicable. The match is a longest module-path prefix on path-segment
// boundaries, so github.com/foo never matches github.com/foobar. Versions are
// compared exactly: Packet 10 does no semantic-version reasoning, so a
// different version is simply different.
func DependencyFits(fingerprint Fingerprint, candidates []resolver.CandidateRef, specimens map[string]model.Specimen) map[string]resolver.CandidateContext {
	out := make(map[string]resolver.CandidateContext, len(candidates))
	for _, candidate := range candidates {
		if candidate.ReuseMode != model.ReuseDependency {
			out[candidate.SpecimenID] = resolver.CandidateContext{DependencyFit: resolver.FitNotApplicable}
			continue
		}
		specimen, ok := specimens[candidate.SpecimenID]
		if !ok {
			out[candidate.SpecimenID] = resolver.CandidateContext{DependencyFit: resolver.FitNewDependency}
			continue
		}
		out[candidate.SpecimenID] = fitFor(fingerprint, specimen)
	}
	return out
}

func fitFor(fingerprint Fingerprint, specimen model.Specimen) resolver.CandidateContext {
	candidatePath := specimen.Source.Path
	if candidatePath == "" {
		return resolver.CandidateContext{DependencyFit: resolver.FitNewDependency}
	}

	modulePath, module, matched := longestModuleMatch(fingerprint, candidatePath)
	if !matched {
		return resolver.CandidateContext{DependencyFit: resolver.FitNewDependency}
	}
	context := resolver.CandidateContext{
		DependencyFit: resolver.FitExistingVersionChange,
		ModulePath:    modulePath,
		ModuleVersion: module.requiredVersion,
	}
	if module.replaced {
		context.DependencyFit = resolver.FitExistingReplaced
		return context
	}
	if module.requiredVersion != "" && module.requiredVersion == specimen.Source.Revision {
		context.DependencyFit = resolver.FitExistingExact
	}
	return context
}

type matchedModule struct {
	requiredVersion string
	replaced        bool
}

// longestModuleMatch finds the required module that best explains a candidate
// package path, comparing on path-segment boundaries.
func longestModuleMatch(fingerprint Fingerprint, candidatePath string) (string, matchedModule, bool) {
	bestPath := ""
	var best matchedModule
	for _, module := range fingerprint.Modules {
		for _, require := range module.Requirements {
			if !providesPackage(require.ModulePath, candidatePath) {
				continue
			}
			if len(require.ModulePath) > len(bestPath) {
				bestPath = require.ModulePath
				best = matchedModule{requiredVersion: require.Version, replaced: replacedModule(module, require.ModulePath)}
			}
		}
	}
	if bestPath == "" {
		return "", matchedModule{}, false
	}
	return bestPath, best, true
}

// providesPackage reports whether a required module path provides the
// candidate package, on path-segment boundaries only.
func providesPackage(modulePath, candidatePath string) bool {
	if modulePath == "" || candidatePath == "" {
		return false
	}
	if modulePath == candidatePath {
		return true
	}
	return len(candidatePath) > len(modulePath) && candidatePath[len(modulePath)] == '/' &&
		candidatePath[:len(modulePath)] == modulePath
}

// replacedModule reports whether any replace directive targets the module,
// with or without a version qualifier.
func replacedModule(module GoModule, modulePath string) bool {
	for _, replace := range module.Replacements {
		if replace.OldModulePath == modulePath {
			return true
		}
	}
	return false
}

func removeMode(values []model.ReuseMode, mode model.ReuseMode) []model.ReuseMode {
	out := make([]model.ReuseMode, 0, len(values))
	for _, value := range values {
		if value != mode {
			out = append(out, value)
		}
	}
	return out
}

// removeModeOrAll removes one mode, or — when the list was unrestricted
// (empty means "everything is permitted") — returns every mode except it, so
// an avoid memory still restricts an otherwise open policy.
func removeModeOrAll(values []model.ReuseMode, mode model.ReuseMode) []model.ReuseMode {
	if len(values) == 0 {
		out := make([]model.ReuseMode, 0, len(policy.ReuseModes()))
		for _, candidate := range policy.ReuseModes() {
			if candidate != mode {
				out = append(out, candidate)
			}
		}
		return out
	}
	return removeMode(values, mode)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// clonePolicy deep-copies a policy so an overlay can never mutate the caller's
// stored base policy through a shared slice.
func clonePolicy(base policy.Policy) policy.Policy {
	clone := base
	clone.Reuse.Allowed = append([]model.ReuseMode(nil), base.Reuse.Allowed...)
	clone.Reuse.Preferred = append([]model.ReuseMode(nil), base.Reuse.Preferred...)
	clone.Licence.Allow = append([]string(nil), base.Licence.Allow...)
	clone.Licence.Deny = append([]string(nil), base.Licence.Deny...)
	clone.Source.RequireRevisionFor = append([]model.ReuseMode(nil), base.Source.RequireRevisionFor...)
	if base.Dependencies.MaxDirect != nil {
		value := *base.Dependencies.MaxDirect
		clone.Dependencies.MaxDirect = &value
	}
	if base.Maintenance.MaxDaysSincePush != nil {
		value := *base.Maintenance.MaxDaysSincePush
		clone.Maintenance.MaxDaysSincePush = &value
	}
	if base.Maintenance.MaxDaysSinceRelease != nil {
		value := *base.Maintenance.MaxDaysSinceRelease
		clone.Maintenance.MaxDaysSinceRelease = &value
	}
	return clone
}
