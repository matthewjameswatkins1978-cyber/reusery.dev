package intent

import (
	"fmt"
	"strings"
	"testing"
)

// readyDraft is the smallest draft that satisfies every READY rule.
func readyDraft() Draft {
	return Draft{
		Status:                 StatusReady,
		Capability:             "bounded subprocess execution",
		Summary:                "Run child processes with bounded output and an explicit lifetime.",
		RequestedArtifactLevel: ArtifactUnspecified,
		Requirements: []Requirement{
			{Description: "Captured stdout must have a configured upper bound.", Kind: "resource", Required: true},
			{Description: "Caller cancellation must initiate process termination.", Kind: "behavior", Required: true},
		},
		Constraints: []Constraint{},
		Ambiguities: []Ambiguity{},
		Assumptions: []string{},
	}
}

func clarificationDraft() Draft {
	draft := readyDraft()
	draft.Status = StatusNeedsClarification
	draft.Ambiguities = []Ambiguity{{
		Question:     "Is this service-to-service or end-user authentication?",
		WhyItMatters: "These require materially different behaviours and solution families.",
	}}
	return draft
}

func unsupportedDraft() Draft {
	return Draft{
		Status:                 StatusUnsupported,
		Capability:             "",
		Summary:                "",
		RequestedArtifactLevel: ArtifactUnspecified,
		Requirements:           []Requirement{},
		Constraints:            []Constraint{},
		Ambiguities:            []Ambiguity{},
		Assumptions:            []string{},
		UnsupportedReason:      "The request is not an engineering selection, reuse or resolution request.",
	}
}

func requireFailure(t *testing.T, failures []string, contains string) {
	t.Helper()
	for _, failure := range failures {
		if strings.Contains(failure, contains) {
			return
		}
	}
	t.Errorf("no failure mentions %q; failures = %q", contains, failures)
}

func requireNoFailures(t *testing.T, failures []string) {
	t.Helper()
	if len(failures) > 0 {
		t.Errorf("unexpected validation failures: %q", failures)
	}
}

func TestValidateAcceptsEveryStatus(t *testing.T) {
	for name, draft := range map[string]Draft{
		"ready":               readyDraft(),
		"needs_clarification": clarificationDraft(),
		"unsupported":         unsupportedDraft(),
	} {
		t.Run(name, func(t *testing.T) {
			requireNoFailures(t, Validate(draft))
		})
	}
}

func TestValidateRejectsUnknownStatusAndArtifactLevel(t *testing.T) {
	draft := readyDraft()
	draft.Status = "probably-fine"
	requireFailure(t, Validate(draft), "status must be ready")

	draft = readyDraft()
	draft.RequestedArtifactLevel = "primitive"
	requireFailure(t, Validate(draft), "requested_artifact_level must be")
}

func TestValidateStatusRules(t *testing.T) {
	t.Run("ready with ambiguity", func(t *testing.T) {
		draft := readyDraft()
		draft.Ambiguities = []Ambiguity{{Question: "Which platform?", WhyItMatters: "Platform semantics differ."}}
		requireFailure(t, Validate(draft), "ready must not contain ambiguities")
	})

	t.Run("ready without requirements", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements = []Requirement{}
		requireFailure(t, Validate(draft), "ready requires at least one requirement")
	})

	t.Run("ready without capability", func(t *testing.T) {
		draft := readyDraft()
		draft.Capability = "   "
		requireFailure(t, Validate(draft), "capability is empty")
	})

	t.Run("ready without summary", func(t *testing.T) {
		draft := readyDraft()
		draft.Summary = ""
		requireFailure(t, Validate(draft), "summary is empty")
	})

	t.Run("ready with unsupported reason", func(t *testing.T) {
		draft := readyDraft()
		draft.UnsupportedReason = "not a request"
		requireFailure(t, Validate(draft), "unsupported_reason must be empty when status is ready")
	})

	t.Run("clarification without ambiguity", func(t *testing.T) {
		draft := readyDraft()
		draft.Status = StatusNeedsClarification
		draft.Ambiguities = []Ambiguity{}
		requireFailure(t, Validate(draft), "needs_clarification requires at least one ambiguity")
	})

	t.Run("clarification without summary", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Summary = ""
		requireFailure(t, Validate(draft), "summary is empty")
	})

	t.Run("unsupported without reason", func(t *testing.T) {
		draft := unsupportedDraft()
		draft.UnsupportedReason = " "
		requireFailure(t, Validate(draft), "unsupported requires unsupported_reason")
	})

	t.Run("unsupported with requirements", func(t *testing.T) {
		draft := unsupportedDraft()
		draft.Requirements = []Requirement{{Description: "Anything.", Kind: "behavior", Required: true}}
		requireFailure(t, Validate(draft), "unsupported must not contain requirements")
	})

	t.Run("unsupported with ambiguities", func(t *testing.T) {
		draft := unsupportedDraft()
		draft.Ambiguities = []Ambiguity{{Question: "What?", WhyItMatters: "Because."}}
		requireFailure(t, Validate(draft), "unsupported must not contain ambiguities")
	})

	t.Run("unsupported may keep explicit constraints", func(t *testing.T) {
		draft := unsupportedDraft()
		draft.Constraints = []Constraint{{Kind: "language", Description: "Go", Required: true}}
		requireNoFailures(t, Validate(draft))
	})
}

func TestValidateRequirementRules(t *testing.T) {
	t.Run("invalid kind", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements[0].Kind = "security"
		requireFailure(t, Validate(draft), `requirement 1 has unsupported kind "security"`)
	})

	t.Run("empty description", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements[0].Description = "   "
		requireFailure(t, Validate(draft), "requirement 1 has an empty description")
	})

	t.Run("duplicate after whitespace normalization", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements[1].Description = "  Captured   stdout\n must have a configured upper bound.  "
		requireFailure(t, Validate(draft), "requirement 2 duplicates requirement 1")
	})

	t.Run("duplicate after case normalization", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements[1].Description = strings.ToUpper(draft.Requirements[0].Description)
		requireFailure(t, Validate(draft), "requirement 2 duplicates requirement 1")
	})

	t.Run("too many requirements", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements = make([]Requirement, DefaultBounds().MaxRequirements+1)
		for i := range draft.Requirements {
			draft.Requirements[i] = Requirement{
				Description: fmt.Sprintf("Claim number %d.", i),
				Kind:        "behavior",
				Required:    true,
			}
		}
		requireFailure(t, Validate(draft), "requirements exceed the maximum of 24")
	})

	t.Run("oversized description", func(t *testing.T) {
		draft := readyDraft()
		draft.Requirements[0].Description = strings.Repeat("x", 401)
		requireFailure(t, Validate(draft), "description is 401 bytes, maximum is 400")
	})
}

func TestValidateConstraintRules(t *testing.T) {
	t.Run("invalid kind", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = []Constraint{{Kind: "flavour", Description: "Go", Required: true}}
		requireFailure(t, Validate(draft), `constraint 1 has unsupported kind "flavour"`)
	})

	t.Run("empty description", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = []Constraint{{Kind: "language", Description: "", Required: true}}
		requireFailure(t, Validate(draft), "constraint 1 has an empty description")
	})

	t.Run("duplicate constraint", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = []Constraint{
			{Kind: "language", Description: "Go", Required: true},
			{Kind: "language", Description: "  go ", Required: false},
		}
		requireFailure(t, Validate(draft), "constraint 2 duplicates constraint 1")
	})

	t.Run("same description under a different kind is not a duplicate", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = []Constraint{
			{Kind: "language", Description: "Go", Required: true},
			{Kind: "runtime", Description: "Go", Required: true},
		}
		requireNoFailures(t, Validate(draft))
	})

	t.Run("too many constraints", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = make([]Constraint, DefaultBounds().MaxConstraints+1)
		for i := range draft.Constraints {
			draft.Constraints[i] = Constraint{
				Kind:        "other",
				Description: fmt.Sprintf("Limit number %d.", i),
				Required:    false,
			}
		}
		requireFailure(t, Validate(draft), "constraints exceed the maximum of 20")
	})
}

func TestValidateAmbiguityRules(t *testing.T) {
	t.Run("empty question", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Ambiguities[0].Question = ""
		requireFailure(t, Validate(draft), "ambiguity 1 has an empty question")
	})

	t.Run("empty why it matters", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Ambiguities[0].WhyItMatters = "  "
		requireFailure(t, Validate(draft), "ambiguity 1 has an empty why_it_matters")
	})

	t.Run("duplicate question", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Ambiguities = append(draft.Ambiguities, Ambiguity{
			Question:     " is this service-to-service or end-user AUTHENTICATION? ",
			WhyItMatters: "Different solution families.",
		})
		requireFailure(t, Validate(draft), "ambiguity 2 duplicates ambiguity 1")
	})

	t.Run("too many ambiguities", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Ambiguities = make([]Ambiguity, DefaultBounds().MaxAmbiguities+1)
		for i := range draft.Ambiguities {
			draft.Ambiguities[i] = Ambiguity{
				Question:     fmt.Sprintf("Open question %d?", i),
				WhyItMatters: "It would change the contract.",
			}
		}
		requireFailure(t, Validate(draft), "ambiguities exceed the maximum of 12")
	})

	t.Run("oversized question", func(t *testing.T) {
		draft := clarificationDraft()
		draft.Ambiguities[0].Question = strings.Repeat("q", 401)
		requireFailure(t, Validate(draft), "question is 401 bytes, maximum is 400")
	})
}

func TestValidateAssumptionRules(t *testing.T) {
	t.Run("empty assumption", func(t *testing.T) {
		draft := readyDraft()
		draft.Assumptions = []string{" "}
		requireFailure(t, Validate(draft), "assumption 1 is empty")
	})

	t.Run("too many assumptions", func(t *testing.T) {
		draft := readyDraft()
		draft.Assumptions = make([]string, DefaultBounds().MaxAssumptions+1)
		for i := range draft.Assumptions {
			draft.Assumptions[i] = fmt.Sprintf("Interpretation %d.", i)
		}
		requireFailure(t, Validate(draft), "assumptions exceed the maximum of 12")
	})

	t.Run("oversized assumption", func(t *testing.T) {
		draft := readyDraft()
		draft.Assumptions = []string{strings.Repeat("a", 401)}
		requireFailure(t, Validate(draft), "assumption 1 is 401 bytes, maximum is 400")
	})
}

func TestValidateSummaryAndCapabilityBounds(t *testing.T) {
	draft := readyDraft()
	draft.Summary = strings.Repeat("s", 601)
	requireFailure(t, Validate(draft), "summary is 601 bytes, maximum is 600")

	draft = readyDraft()
	draft.Capability = strings.Repeat("c", 161)
	requireFailure(t, Validate(draft), "capability is 161 bytes, maximum is 160")

	draft = unsupportedDraft()
	draft.UnsupportedReason = strings.Repeat("u", 601)
	requireFailure(t, Validate(draft), "unsupported_reason is 601 bytes, maximum is 600")
}

func TestValidateMessagesCarryNoImplementationInternals(t *testing.T) {
	draft := readyDraft()
	draft.Requirements[0].Kind = "nonsense"
	failures := Validate(draft)
	if len(failures) == 0 {
		t.Fatal("expected failures")
	}
	for _, failure := range failures {
		if strings.Contains(failure, ".go:") || strings.Contains(failure, "0x") ||
			strings.Contains(failure, "goroutine") {
			t.Errorf("failure %q leaks implementation internals", failure)
		}
	}
}

func TestKindValueListsMatchTheValidators(t *testing.T) {
	for _, kind := range RequirementKindValues() {
		if !ValidRequirementKind(kind) {
			t.Errorf("requirement kind %q is listed but not valid", kind)
		}
	}
	for _, kind := range ConstraintKindValues() {
		if !ValidConstraintKind(kind) {
			t.Errorf("constraint kind %q is listed but not valid", kind)
		}
	}
	if len(RequirementKindValues()) != 10 {
		t.Errorf("requirement kinds = %d, want 10", len(RequirementKindValues()))
	}
	if len(ConstraintKindValues()) != 13 {
		t.Errorf("constraint kinds = %d, want 13", len(ConstraintKindValues()))
	}
}

func TestErrorKindsAreClassified(t *testing.T) {
	for _, kind := range []ErrorKind{
		ErrorAuthentication, ErrorRateLimited, ErrorTimeout, ErrorUnavailable,
		ErrorRefused, ErrorIncomplete, ErrorInvalidResponse,
	} {
		if !kind.Valid() {
			t.Errorf("kind %q should be valid", kind)
		}
	}
	if ErrorKind("leaked-key").Valid() {
		t.Error("an unknown kind must not be valid")
	}
}
