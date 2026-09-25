package intent

import (
	"fmt"
	"strings"
)

// Maximum structural bounds, mirrored from Bounds for readability at call
// sites. They are a fixed Packet 6 constant, not configuration.
var (
	requirementKinds = map[string]struct{}{
		"behavior": {}, "invariant": {}, "resource": {}, "lifecycle": {},
		"platform": {}, "error-model": {}, "compatibility": {},
		"integration": {}, "policy": {}, "other": {},
	}
	constraintKinds = map[string]struct{}{
		"language": {}, "runtime": {}, "platform": {}, "license": {},
		"dependency": {}, "framework": {}, "performance": {},
		"deployment": {}, "compatibility": {}, "integration": {},
		"security": {}, "resource": {}, "other": {},
	}
)

// RequirementKindValues returns the allowed requirement kinds in schema order.
func RequirementKindValues() []string {
	return []string{
		"behavior", "invariant", "resource", "lifecycle", "platform",
		"error-model", "compatibility", "integration", "policy", "other",
	}
}

// ConstraintKindValues returns the allowed constraint kinds in schema order.
func ConstraintKindValues() []string {
	return []string{
		"language", "runtime", "platform", "license", "dependency",
		"framework", "performance", "deployment", "compatibility",
		"integration", "security", "resource", "other",
	}
}

// ValidRequirementKind reports whether kind is allowed.
func ValidRequirementKind(kind string) bool {
	_, ok := requirementKinds[kind]
	return ok
}

// ValidConstraintKind reports whether kind is allowed.
func ValidConstraintKind(kind string) bool {
	_, ok := constraintKinds[kind]
	return ok
}

// Validate returns the deterministic semantic validation failures for a draft.
// An empty result means the draft is valid.
//
// Strict JSON Schema conformance is deliberately not trusted on its own: this
// is the layer that decides whether a draft may become a provisional contract.
// Messages are safe and deterministic — no Go error strings, no provider text
// and no implementation internals — so they can be shown to a repair call.
func Validate(draft Draft) []string {
	failures := make([]string, 0, 8)

	if !draft.Status.Valid() {
		return append(failures, "status must be ready, needs_clarification or unsupported")
	}
	if !draft.RequestedArtifactLevel.Valid() {
		failures = append(failures,
			"requested_artifact_level must be unspecified, code, package, library, framework or cross_level")
	}

	bounds := DefaultBounds()
	if len(draft.Capability) > bounds.MaxCapabilityBytes {
		failures = append(failures, fmt.Sprintf(
			"capability is %d bytes, maximum is %d", len(draft.Capability), bounds.MaxCapabilityBytes))
	}
	if len(draft.Summary) > bounds.MaxSummaryBytes {
		failures = append(failures, fmt.Sprintf(
			"summary is %d bytes, maximum is %d", len(draft.Summary), bounds.MaxSummaryBytes))
	}
	if len(draft.UnsupportedReason) > bounds.MaxUnsupportedReasonBytes {
		failures = append(failures, fmt.Sprintf(
			"unsupported_reason is %d bytes, maximum is %d",
			len(draft.UnsupportedReason), bounds.MaxUnsupportedReasonBytes))
	}

	switch draft.Status {
	case StatusReady:
		if strings.TrimSpace(draft.Capability) == "" {
			failures = append(failures, "capability is empty")
		}
		if strings.TrimSpace(draft.Summary) == "" {
			failures = append(failures, "summary is empty")
		}
		if len(draft.Requirements) == 0 {
			failures = append(failures, "ready requires at least one requirement")
		}
		if len(draft.Ambiguities) > 0 {
			failures = append(failures, "ready must not contain ambiguities")
		}
		if strings.TrimSpace(draft.UnsupportedReason) != "" {
			failures = append(failures, "unsupported_reason must be empty when status is ready")
		}
	case StatusNeedsClarification:
		if strings.TrimSpace(draft.Summary) == "" {
			failures = append(failures, "summary is empty")
		}
		if len(draft.Ambiguities) == 0 {
			failures = append(failures, "needs_clarification requires at least one ambiguity")
		}
		if strings.TrimSpace(draft.UnsupportedReason) != "" {
			failures = append(failures, "unsupported_reason must be empty when status is needs_clarification")
		}
	case StatusUnsupported:
		if strings.TrimSpace(draft.UnsupportedReason) == "" {
			failures = append(failures, "unsupported requires unsupported_reason")
		}
		if len(draft.Requirements) > 0 {
			failures = append(failures, "unsupported must not contain requirements")
		}
		if len(draft.Ambiguities) > 0 {
			failures = append(failures, "unsupported must not contain ambiguities")
		}
	}

	failures = append(failures, validateRequirements(draft.Requirements)...)
	failures = append(failures, validateConstraints(draft.Constraints)...)
	failures = append(failures, validateAmbiguities(draft.Ambiguities)...)
	failures = append(failures, validateAssumptions(draft.Assumptions)...)
	return failures
}

func validateRequirements(requirements []Requirement) []string {
	bounds := DefaultBounds()
	failures := make([]string, 0, 2)
	if len(requirements) > bounds.MaxRequirements {
		failures = append(failures, fmt.Sprintf(
			"%d requirements exceed the maximum of %d", len(requirements), bounds.MaxRequirements))
	}

	seen := make(map[string]int, len(requirements))
	for i, requirement := range requirements {
		number := i + 1
		description := strings.TrimSpace(requirement.Description)
		if description == "" {
			failures = append(failures, fmt.Sprintf("requirement %d has an empty description", number))
			continue
		}
		if len(requirement.Description) > bounds.MaxRequirementDescriptionBytes {
			failures = append(failures, fmt.Sprintf(
				"requirement %d description is %d bytes, maximum is %d",
				number, len(requirement.Description), bounds.MaxRequirementDescriptionBytes))
		}
		if !ValidRequirementKind(requirement.Kind) {
			failures = append(failures, fmt.Sprintf(
				"requirement %d has unsupported kind %q", number, requirement.Kind))
		}
		key := normalizeKey(requirement.Description)
		if previous, duplicate := seen[key]; duplicate {
			failures = append(failures, fmt.Sprintf(
				"requirement %d duplicates requirement %d", number, previous+1))
			continue
		}
		seen[key] = i
	}
	return failures
}

func validateConstraints(constraints []Constraint) []string {
	bounds := DefaultBounds()
	failures := make([]string, 0, 2)
	if len(constraints) > bounds.MaxConstraints {
		failures = append(failures, fmt.Sprintf(
			"%d constraints exceed the maximum of %d", len(constraints), bounds.MaxConstraints))
	}

	seen := make(map[string]int, len(constraints))
	for i, constraint := range constraints {
		number := i + 1
		description := strings.TrimSpace(constraint.Description)
		if description == "" {
			failures = append(failures, fmt.Sprintf("constraint %d has an empty description", number))
			continue
		}
		if len(constraint.Description) > bounds.MaxConstraintDescriptionBytes {
			failures = append(failures, fmt.Sprintf(
				"constraint %d description is %d bytes, maximum is %d",
				number, len(constraint.Description), bounds.MaxConstraintDescriptionBytes))
		}
		if !ValidConstraintKind(constraint.Kind) {
			failures = append(failures, fmt.Sprintf(
				"constraint %d has unsupported kind %q", number, constraint.Kind))
		}
		key := normalizeKey(constraint.Kind) + "\x00" + normalizeKey(constraint.Description)
		if previous, duplicate := seen[key]; duplicate {
			failures = append(failures, fmt.Sprintf(
				"constraint %d duplicates constraint %d", number, previous+1))
			continue
		}
		seen[key] = i
	}
	return failures
}

func validateAmbiguities(ambiguities []Ambiguity) []string {
	bounds := DefaultBounds()
	failures := make([]string, 0, 2)
	if len(ambiguities) > bounds.MaxAmbiguities {
		failures = append(failures, fmt.Sprintf(
			"%d ambiguities exceed the maximum of %d", len(ambiguities), bounds.MaxAmbiguities))
	}

	seen := make(map[string]int, len(ambiguities))
	for i, ambiguity := range ambiguities {
		number := i + 1
		question := strings.TrimSpace(ambiguity.Question)
		if question == "" {
			failures = append(failures, fmt.Sprintf("ambiguity %d has an empty question", number))
			continue
		}
		if len(ambiguity.Question) > bounds.MaxAmbiguityQuestionBytes {
			failures = append(failures, fmt.Sprintf(
				"ambiguity %d question is %d bytes, maximum is %d",
				number, len(ambiguity.Question), bounds.MaxAmbiguityQuestionBytes))
		}
		if strings.TrimSpace(ambiguity.WhyItMatters) == "" {
			failures = append(failures, fmt.Sprintf("ambiguity %d has an empty why_it_matters", number))
		} else if len(ambiguity.WhyItMatters) > bounds.MaxAmbiguityWhyBytes {
			failures = append(failures, fmt.Sprintf(
				"ambiguity %d why_it_matters is %d bytes, maximum is %d",
				number, len(ambiguity.WhyItMatters), bounds.MaxAmbiguityWhyBytes))
		}
		key := normalizeKey(ambiguity.Question)
		if previous, duplicate := seen[key]; duplicate {
			failures = append(failures, fmt.Sprintf(
				"ambiguity %d duplicates ambiguity %d", number, previous+1))
			continue
		}
		seen[key] = i
	}
	return failures
}

func validateAssumptions(assumptions []string) []string {
	bounds := DefaultBounds()
	failures := make([]string, 0, 2)
	if len(assumptions) > bounds.MaxAssumptions {
		failures = append(failures, fmt.Sprintf(
			"%d assumptions exceed the maximum of %d", len(assumptions), bounds.MaxAssumptions))
	}
	for i, assumption := range assumptions {
		number := i + 1
		if strings.TrimSpace(assumption) == "" {
			failures = append(failures, fmt.Sprintf("assumption %d is empty", number))
			continue
		}
		if len(assumption) > bounds.MaxAssumptionBytes {
			failures = append(failures, fmt.Sprintf(
				"assumption %d is %d bytes, maximum is %d",
				number, len(assumption), bounds.MaxAssumptionBytes))
		}
	}
	return failures
}

// normalizeKey is the deduplication key: trim, collapse internal whitespace
// and case fold. Duplicates are reported as failures rather than silently
// deleted, so model quality stays visible and the one repair attempt is used.
func normalizeKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
