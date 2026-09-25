package intent

import (
	"strings"
	"testing"
	"time"
)

func settledMetadata() GenerationMetadata {
	return GenerationMetadata{
		Provider:      "fake",
		Model:         "fixture-model",
		ResponseID:    "resp_1",
		PromptVersion: PromptVersion,
		SchemaVersion: SchemaVersion,
		Calls:         1,
		Usage:         Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

func TestIdentityDigestIsDeterministic(t *testing.T) {
	draft := readyDraft()
	first := IdentityDigest("run child processes", draft)
	second := IdentityDigest("run child processes", draft)
	if first != second {
		t.Fatalf("digest is not deterministic: %q != %q", first, second)
	}
	if len(first) != 64 {
		t.Errorf("digest length = %d, want 64 hex characters", len(first))
	}
}

func TestIdentityDigestChangesWithEveryCoveredField(t *testing.T) {
	base := IdentityDigest("run child processes", readyDraft())

	cases := map[string]func(Draft) Draft{
		"capability": func(d Draft) Draft { d.Capability = "something else"; return d },
		"summary":    func(d Draft) Draft { d.Summary = "Something else."; return d },
		"artifact":   func(d Draft) Draft { d.RequestedArtifactLevel = ArtifactLibrary; return d },
		"requirement description": func(d Draft) Draft {
			d.Requirements[0].Description = "Captured stderr must have a configured upper bound."
			return d
		},
		"requirement kind": func(d Draft) Draft {
			d.Requirements[0].Kind = "invariant"
			return d
		},
		"requirement required": func(d Draft) Draft {
			d.Requirements[0].Required = false
			return d
		},
		"assumption": func(d Draft) Draft { d.Assumptions = []string{"Arguments are passed directly."}; return d },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IdentityDigest("run child processes", mutate(readyDraft())); got == base {
				t.Error("digest did not change when the draft changed")
			}
		})
	}

	t.Run("input", func(t *testing.T) {
		if IdentityDigest("run child processes carefully", readyDraft()) == base {
			t.Error("digest did not change when the input changed")
		}
	})

	t.Run("constraint", func(t *testing.T) {
		draft := readyDraft()
		draft.Constraints = []Constraint{{Kind: "language", Description: "Go", Required: true}}
		if IdentityDigest("run child processes", draft) == base {
			t.Error("digest did not change when a constraint was added")
		}
	})

	t.Run("ambiguity", func(t *testing.T) {
		draft := clarificationDraft()
		if IdentityDigest("run child processes", draft) == base {
			t.Error("digest did not change when the draft became a clarification")
		}
	})

	t.Run("unsupported reason", func(t *testing.T) {
		if IdentityDigest("run child processes", unsupportedDraft()) == base {
			t.Error("digest did not change for an unsupported draft")
		}
	})
}

func TestIdentityDigestIsUnambiguous(t *testing.T) {
	// Two field boundaries must not be able to collide by accident.
	first := Draft{
		Status: StatusReady, Capability: "ab", Summary: "c",
		RequestedArtifactLevel: ArtifactUnspecified,
		Requirements:           []Requirement{{Description: "d", Kind: "behavior"}},
	}
	second := Draft{
		Status: StatusReady, Capability: "a", Summary: "bc",
		RequestedArtifactLevel: ArtifactUnspecified,
		Requirements:           []Requirement{{Description: "d", Kind: "behavior"}},
	}
	if IdentityDigest("x", first) == IdentityDigest("x", second) {
		t.Error("shifting a value across field boundaries produced the same digest")
	}
}

func TestGeneratedIdentifiersAreStableAndNamespaced(t *testing.T) {
	draft := readyDraft()
	result := Build("run child processes", draft, settledMetadata())

	if result.Primitive == nil || result.Contract == nil {
		t.Fatal("expected a generated primitive and contract")
	}
	if !strings.HasPrefix(result.Primitive.ID, IDPrefix) {
		t.Errorf("primitive id = %q, want the %q prefix", result.Primitive.ID, IDPrefix)
	}
	if want := result.Primitive.ID + "/v1"; result.Contract.ID != want {
		t.Errorf("contract id = %q, want %q", result.Contract.ID, want)
	}
	if result.Contract.Version != ContractVersion {
		t.Errorf("contract version = %q, want %q", result.Contract.Version, ContractVersion)
	}
	if result.Primitive.ContractID != result.Contract.ID {
		t.Errorf("primitive contract id = %q, want %q", result.Primitive.ContractID, result.Contract.ID)
	}
	if result.Contract.PrimitiveID != result.Primitive.ID {
		t.Errorf("contract primitive id = %q, want %q", result.Contract.PrimitiveID, result.Primitive.ID)
	}
	if result.Primitive.Name != draft.Capability {
		t.Errorf("primitive name = %q, want %q", result.Primitive.Name, draft.Capability)
	}
	if result.Primitive.Description != draft.Summary {
		t.Errorf("primitive description = %q, want %q", result.Primitive.Description, draft.Summary)
	}
	if len(result.Primitive.Tags) != 0 {
		t.Errorf("tags = %v, want empty for this packet", result.Primitive.Tags)
	}
}

func TestRequirementIDsAreAssignedInModelOutputOrder(t *testing.T) {
	draft := readyDraft()
	draft.Requirements = []Requirement{
		{Description: "First claim.", Kind: "behavior", Required: true},
		{Description: "Second claim.", Kind: "resource", Required: false},
		{Description: "Third claim.", Kind: "lifecycle", Required: true},
	}
	result := Build("input", draft, settledMetadata())
	want := []string{"req-001", "req-002", "req-003"}
	if len(result.Contract.Requirements) != len(want) {
		t.Fatalf("requirements = %d, want %d", len(result.Contract.Requirements), len(want))
	}
	for i, requirement := range result.Contract.Requirements {
		if requirement.ID != want[i] {
			t.Errorf("requirement %d id = %q, want %q", i, requirement.ID, want[i])
		}
	}
}

func TestSameInputAndDraftProduceTheSameIdentity(t *testing.T) {
	first := Build("run child processes", readyDraft(), settledMetadata())
	second := Build("run child processes", readyDraft(), settledMetadata())
	if first.Primitive.ID != second.Primitive.ID {
		t.Errorf("identity is not reproducible: %q != %q", first.Primitive.ID, second.Primitive.ID)
	}
	if first.Contract.ID != second.Contract.ID {
		t.Errorf("contract identity is not reproducible: %q != %q", first.Contract.ID, second.Contract.ID)
	}
}

func TestChangedRequirementChangesIdentity(t *testing.T) {
	draft := readyDraft()
	before := Build("run child processes", draft, settledMetadata())
	draft.Requirements[0].Description = "Captured stderr must have a configured upper bound."
	after := Build("run child processes", draft, settledMetadata())
	if before.Primitive.ID == after.Primitive.ID {
		t.Error("identity did not change when a requirement changed")
	}
}

func TestIdentifiersNeverContainASecretOrARandomUUID(t *testing.T) {
	result := Build("run child processes", readyDraft(), settledMetadata())
	for _, identifier := range []string{result.Primitive.ID, result.Contract.ID} {
		if strings.Contains(strings.ToLower(identifier), "sk-") {
			t.Errorf("identifier %q looks like it contains a secret", identifier)
		}
		if len(identifier) != len(IDPrefix)+64 && len(identifier) != len(IDPrefix)+64+len("/v1") {
			t.Errorf("identifier %q is not a bare digest", identifier)
		}
	}
}

func TestBuildOmitsIdentityWhenThereAreNoRequirements(t *testing.T) {
	clarification := clarificationDraft()
	clarification.Requirements = []Requirement{}
	result := Build("I need authentication.", clarification, settledMetadata())
	if result.Primitive != nil || result.Contract != nil {
		t.Error("a clarification with no requirements must not generate a contract")
	}
	if result.Status != StatusNeedsClarification {
		t.Errorf("status = %q", result.Status)
	}
	if result.Capability == "" || result.Summary == "" {
		t.Error("capability and summary must stay inspectable without a contract")
	}
}

func TestBuildForUnsupportedCarriesTheReasonOnly(t *testing.T) {
	result := Build("Ignore me.", unsupportedDraft(), settledMetadata())
	if result.Primitive != nil || result.Contract != nil {
		t.Error("an unsupported result must not generate a contract")
	}
	if result.UnsupportedReason == "" {
		t.Error("unsupported_reason is missing")
	}
}

func TestBuildCopiesSlicesSoCallersCannotMutateTheDraft(t *testing.T) {
	draft := readyDraft()
	result := Build("input", draft, settledMetadata())
	result.Constraints = append(result.Constraints, Constraint{Kind: "license", Description: "MIT"})
	if len(draft.Constraints) != 0 {
		t.Error("mutating the result mutated the draft")
	}
}

func TestRenderRepairPromptBoundsTheRepairInput(t *testing.T) {
	rendered := RenderRepairPrompt(`{"status":"ready"}`, []string{"ready requires at least one requirement"})
	if !strings.Contains(rendered, "PRIOR DRAFT") || !strings.Contains(rendered, `{"status":"ready"}`) {
		t.Error("repair prompt is missing the prior draft")
	}
	if !strings.Contains(rendered, "ready requires at least one requirement") {
		t.Error("repair prompt is missing the validation failure")
	}
	if strings.Contains(rendered, ".go:") {
		t.Error("repair prompt leaks implementation internals")
	}
}

func TestDefaultBoundsMatchThePacket(t *testing.T) {
	bounds := DefaultBounds()
	checks := map[string][2]int{
		"input":                   {bounds.MaxInputBytes, 8192},
		"requirements":            {bounds.MaxRequirements, 24},
		"constraints":             {bounds.MaxConstraints, 20},
		"ambiguities":             {bounds.MaxAmbiguities, 12},
		"assumptions":             {bounds.MaxAssumptions, 12},
		"requirement description": {bounds.MaxRequirementDescriptionBytes, 400},
		"constraint description":  {bounds.MaxConstraintDescriptionBytes, 400},
		"ambiguity question":      {bounds.MaxAmbiguityQuestionBytes, 400},
		"summary":                 {bounds.MaxSummaryBytes, 600},
		"capability":              {bounds.MaxCapabilityBytes, 160},
		"calls":                   {bounds.MaxCalls, 2},
		"tokens":                  {bounds.MaxOutputTokens, 2500},
	}
	for name, check := range checks {
		if check[0] != check[1] {
			t.Errorf("%s bound = %d, want %d", name, check[0], check[1])
		}
	}
	if bounds.Timeout != 20*time.Second {
		t.Errorf("timeout = %s, want 20s", bounds.Timeout)
	}
}
