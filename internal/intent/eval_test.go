package intent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// stubNormalizer is the harness double: corpus tests never make a paid call.
type stubNormalizer struct {
	calls   int
	handler func(input string) (Result, error)
}

func (s *stubNormalizer) Normalize(_ context.Context, input string) (Result, error) {
	s.calls++
	return s.handler(input)
}

func stubResult(status Status, requirements ...string) Result {
	result := Result{
		Input:                  "input",
		Status:                 status,
		RequestedArtifactLevel: ArtifactUnspecified,
		Capability:             "capability",
		Summary:                "summary",
		Constraints:            []Constraint{},
		Ambiguities:            []Ambiguity{},
		Assumptions:            []string{},
		Metadata: GenerationMetadata{
			Provider:      "fake",
			Model:         "fixture-model",
			PromptVersion: PromptVersion,
			SchemaVersion: SchemaVersion,
			Calls:         1,
		},
	}
	if len(requirements) == 0 {
		return result
	}
	primitiveID := IDPrefix + strings.Repeat("a", 64)
	contractID := ContractID(strings.Repeat("a", 64))
	contractRequirements := make([]model.Requirement, 0, len(requirements))
	for i, description := range requirements {
		contractRequirements = append(contractRequirements, model.Requirement{
			ID:          RequirementID(i),
			Description: description,
			Kind:        "behavior",
			Required:    true,
		})
	}
	primitive := model.Primitive{
		ID:          primitiveID,
		Name:        result.Capability,
		Description: result.Summary,
		ContractID:  contractID,
	}
	contract := model.Contract{
		ID:           contractID,
		PrimitiveID:  primitiveID,
		Version:      ContractVersion,
		Summary:      result.Summary,
		Requirements: contractRequirements,
	}
	result.Primitive = &primitive
	result.Contract = &contract
	return result
}

func writeCorpus(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return path
}

func TestLoadCorpusReadsTheShippedCorpus(t *testing.T) {
	corpus, err := LoadCorpus(filepath.Join("..", "..", "evals", "intent", "v1.yaml"))
	if err != nil {
		t.Fatalf("shipped corpus is not loadable: %v", err)
	}
	if len(corpus.Cases) != 24 {
		t.Errorf("shipped corpus has %d cases, want 24", len(corpus.Cases))
	}
	if corpus.Acceptance.MinSemanticPassRate != DefaultMinSemanticPassRate {
		t.Errorf("min pass rate = %v, want %v", corpus.Acceptance.MinSemanticPassRate, DefaultMinSemanticPassRate)
	}
	if corpus.Acceptance.MaxRepairRate != DefaultMaxRepairRate {
		t.Errorf("max repair rate = %v, want %v", corpus.Acceptance.MaxRepairRate, DefaultMaxRepairRate)
	}

	seen := map[string]bool{}
	categories := map[string]int{}
	for _, testCase := range corpus.Cases {
		if seen[testCase.ID] {
			t.Errorf("duplicate case id %q", testCase.ID)
		}
		seen[testCase.ID] = true
		categories[testCase.Category]++
	}
	if len(seen) != 24 {
		t.Errorf("unique case ids = %d, want 24", len(seen))
	}
	// The packet's required composition, checked so a corpus edit cannot
	// quietly drop a materially different intent shape.
	wantCategories := map[string]int{
		"bounded-subprocess": 6,
		"library":            3,
		"framework":          3,
		"code":               3,
		"ambiguous":          3,
		"license":            2,
		"platform":           1,
		"runtime":            1,
		"adversarial":        2,
	}
	for name, want := range wantCategories {
		if categories[name] != want {
			t.Errorf("category %q has %d cases, want %d", name, categories[name], want)
		}
	}
	for name, count := range categories {
		if _, expected := wantCategories[name]; !expected {
			t.Errorf("category %q (%d cases) is not part of the required composition", name, count)
		}
	}

	critical := 0
	for _, testCase := range corpus.Cases {
		if testCase.Critical {
			critical++
		}
	}
	if critical != 3 {
		t.Errorf("critical cases = %d, want the 3 materially ambiguous requests", critical)
	}
}

func TestLoadCorpusRejectsStructuralProblems(t *testing.T) {
	cases := map[string]string{
		"missing file": "",
		"not yaml":     "this is not a corpus",
		"unknown field": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
    surprise: true
`,
		"bad schema version": `schema_version: 2
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
`,
		"no cases": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases: []
`,
		"duplicate id": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
  - id: a
    text: world
`,
		"empty text": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: "   "
`,
		"bad status": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
    expectations:
      expected_status: probably
`,
		"min exceeds max": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
    expectations:
      min_requirements: 5
      max_requirements: 2
`,
		"unknown constraint kind": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
    expectations:
      required_constraint_kinds: [flavour]
`,
		"bad acceptance": `schema_version: 1
acceptance: {min_semantic_pass_rate: 1.5, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
`,
		"empty alternatives group": `schema_version: 1
acceptance: {min_semantic_pass_rate: 0.9, max_repair_rate: 0.2}
cases:
  - id: a
    text: hello
    expectations:
      required_requirement_terms:
        - []
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := ""
			if name != "missing file" {
				path = writeCorpus(t, body)
			} else {
				path = filepath.Join(t.TempDir(), "absent.yaml")
			}
			_, err := LoadCorpus(path)
			if !errors.Is(err, ErrCorpus) {
				t.Fatalf("err = %v, want ErrCorpus", err)
			}
		})
	}
}

func TestExpectationsAreSemanticNotVerbatim(t *testing.T) {
	result := stubResult(StatusReady,
		"A configured deadline terminates the running process.",
		"Caller cancellation initiates process termination.")
	result.Constraints = []Constraint{{Kind: "language", Description: "Go", Required: true}}

	expectations := Expectations{
		ExpectedStatus:           StatusReady,
		MinRequirements:          2,
		RequiredRequirementTerms: [][]string{{"timeout", "deadline"}, {"cancel", "cancellation"}},
		RequiredConstraintKinds:  []string{"language"},
	}
	if failures := expectations.evaluate(result); len(failures) > 0 {
		t.Errorf("semantic expectations failed: %q", failures)
	}

	// The same intent expressed with different words must still satisfy the
	// same alternative groups: no sentence is compared.
	other := stubResult(StatusReady,
		"Captured output is bounded.",
		"A configured deadline ends the child.",
		"Caller cancellation tears down the child.")
	other.Constraints = []Constraint{{Kind: "language", Description: "Go", Required: true}}
	if failures := expectations.evaluate(other); len(failures) > 0 {
		t.Errorf("alternate wording failed: %q", failures)
	}
}

func TestExpectationsReportEveryUnmetCheck(t *testing.T) {
	result := stubResult(StatusReady)
	result.RequestedArtifactLevel = ArtifactLibrary
	expectations := Expectations{
		ExpectedStatus:           StatusNeedsClarification,
		ExpectedArtifactLevel:    ArtifactFramework,
		MinRequirements:          3,
		MaxRequirements:          0,
		MinAmbiguities:           1,
		RequiredRequirementTerms: [][]string{{"timeout"}},
		RequiredConstraintKinds:  []string{"language"},
	}
	failures := expectations.evaluate(result)
	want := []string{
		"status is ready, expected needs_clarification",
		"requested_artifact_level is library, expected framework",
		"0 requirements, expected at least 3",
		"0 ambiguities, expected at least 1",
		"no requirement mentions any of timeout",
		`no constraint of kind "language"`,
	}
	for _, expected := range want {
		found := false
		for _, failure := range failures {
			if failure == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("missing failure %q in %q", expected, failures)
		}
	}
	if len(failures) != len(want) {
		t.Errorf("failures = %q, want exactly %d", failures, len(want))
	}
}

func TestExpectationsRejectInventedLicenceAndSecurityFacts(t *testing.T) {
	result := stubResult(StatusReady)
	result.Constraints = []Constraint{{Kind: "license", Description: "MIT", Required: true}}
	failures := Expectations{}.evaluate(result)
	if !containsSubstring(failures, "licence constraint was invented") {
		t.Errorf("failures = %q, want the licence traceability rule", failures)
	}

	result = stubResult(StatusReady)
	result.Constraints = []Constraint{{Kind: "security", Description: "TLS 1.3 only", Required: true}}
	failures = Expectations{}.evaluate(result)
	if !containsSubstring(failures, "security constraint was invented") {
		t.Errorf("failures = %q, want the security traceability rule", failures)
	}

	// Stating the licence in the request makes the constraint legitimate.
	result.Input = "I need a Go JSON library but GPL code is not acceptable."
	result.Constraints = []Constraint{{Kind: "license", Description: "No GPL", Required: true}}
	allowed := Expectations{}.evaluate(result)
	if len(allowed) > 0 {
		t.Errorf("stated licence was rejected: %q", allowed)
	}
}

func TestExpectationsFlagForbiddenConcepts(t *testing.T) {
	result := stubResult(StatusReady)
	result.Capability = "confidence-aware runner"
	failures := Expectations{ForbiddenConcepts: []string{"confidence"}}.evaluate(result)
	if !containsSubstring(failures, `forbidden concept "confidence"`) {
		t.Errorf("failures = %q", failures)
	}
}

func TestStructuralWalkFindsProhibitedAuthority(t *testing.T) {
	tree := map[string]any{
		"status": "ready",
		"contract": map[string]any{
			"requirements": []any{
				map[string]any{"id": "req-001", "applies_to": "req-001"},
			},
		},
		"evidence":   []any{},
		"candidates": []any{map[string]any{"score": 1}},
	}
	violations := walkForProhibitedKeys("", tree)
	for _, want := range []string{"evidence", "candidates", "score", "applies_to"} {
		found := false
		for _, violation := range violations {
			if strings.Contains(violation, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no violation mentions %q: %q", want, violations)
		}
	}
	if len(violations) != 4 {
		t.Errorf("violations = %q, want 4", violations)
	}
}

func TestRunCorpusAggregatesResultsAndGates(t *testing.T) {
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Acceptance: Acceptance{
			MinSemanticPassRate: 1.0,
			MaxRepairRate:       0.5,
		},
		Cases: []EvalCase{
			{
				ID: "ok", Category: "bounded-subprocess", Text: "run child processes",
				Expectations: Expectations{ExpectedStatus: StatusReady, MinRequirements: 1},
			},
			{
				ID: "ambiguous", Category: "ambiguous", Critical: true, Text: "I need authentication.",
				Expectations: Expectations{ExpectedStatus: StatusNeedsClarification, MinAmbiguities: 1},
			},
		},
	}

	stub := &stubNormalizer{handler: func(input string) (Result, error) {
		if strings.Contains(input, "authentication") {
			return Result{
				Input: input, Status: StatusNeedsClarification,
				RequestedArtifactLevel: ArtifactUnspecified,
				Capability:             "authentication", Summary: "summary",
				Constraints: []Constraint{},
				Ambiguities: []Ambiguity{{Question: "Which kind?", WhyItMatters: "It differs."}},
				Assumptions: []string{},
				Metadata: GenerationMetadata{Provider: "fake", Model: "fixture-model", Calls: 1,
					Repaired: true, PromptVersion: PromptVersion, SchemaVersion: SchemaVersion},
			}, nil
		}
		return stubResult(StatusReady, "Captured output is bounded."), nil
	}}

	report, err := RunCorpus(context.Background(), stub, corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if report.Cases != 2 || report.Passed != 2 || report.Failed != 0 {
		t.Errorf("cases=%d passed=%d failed=%d", report.Cases, report.Passed, report.Failed)
	}
	if report.Repairs != 1 {
		t.Errorf("repairs = %d, want 1", report.Repairs)
	}
	if report.Gates.RepairRate != 0.5 {
		t.Errorf("repair rate = %v, want 0.5", report.Gates.RepairRate)
	}
	if !report.Gates.Met() {
		t.Errorf("gates not met: %q", report.Gates.Failures())
	}
	if report.Usage.TotalTokens != 0 {
		t.Errorf("usage = %#v, want zero from a stub", report.Usage)
	}
	if len(report.Results) != 2 {
		t.Errorf("results = %d, want 2", len(report.Results))
	}
	if report.Provider != "fake" || report.Model != "fixture-model" {
		t.Errorf("provider/model = %q/%q", report.Provider, report.Model)
	}
}

func TestRunCorpusFailsTheCriticalGateWhenAmbiguityIsGuessed(t *testing.T) {
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Acceptance:    Acceptance{MinSemanticPassRate: 0.0, MaxRepairRate: 1.0},
		Cases: []EvalCase{{
			ID: "ambiguous", Category: "ambiguous", Critical: true, Text: "I need authentication.",
			Expectations: Expectations{ExpectedStatus: StatusNeedsClarification, MinAmbiguities: 1},
		}},
	}
	stub := &stubNormalizer{handler: func(string) (Result, error) {
		return stubResult(StatusReady, "Anything."), nil
	}}

	report, err := RunCorpus(context.Background(), stub, corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if report.Gates.CriticalAmbiguous {
		t.Error("a critical ambiguous case was guessed at and the gate still passed")
	}
	if report.Gates.Met() {
		t.Error("gates must not be met when a critical case is misclassified")
	}
	if !containsSubstring(report.Gates.Failures(), "critical ambiguous case") {
		t.Errorf("failures = %q", report.Gates.Failures())
	}
}

func TestRunCorpusFailsTheSemanticRateGate(t *testing.T) {
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Acceptance:    Acceptance{MinSemanticPassRate: 1.0, MaxRepairRate: 1.0},
		Cases: []EvalCase{
			{ID: "a", Text: "one", Expectations: Expectations{ExpectedStatus: StatusReady}},
			{ID: "b", Text: "two", Expectations: Expectations{ExpectedStatus: StatusReady}},
		},
	}
	stub := &stubNormalizer{handler: func(string) (Result, error) {
		return stubResult(StatusUnsupported), nil
	}}

	report, err := RunCorpus(context.Background(), stub, corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if report.Gates.SemanticPassRateMet {
		t.Error("a 0% pass rate met a 100% gate")
	}
	if report.Failed != 2 {
		t.Errorf("failed = %d, want 2", report.Failed)
	}
}

func TestRunCorpusStopsAfterAProviderFailure(t *testing.T) {
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Acceptance:    Acceptance{MinSemanticPassRate: 1.0, MaxRepairRate: 1.0},
		Cases: []EvalCase{
			{ID: "a", Text: "one"},
			{ID: "b", Text: "two"},
			{ID: "c", Text: "three"},
		},
	}
	stub := &stubNormalizer{handler: func(string) (Result, error) {
		return Result{}, NewProviderError(ErrorRateLimited, 429, "")
	}}

	report, err := RunCorpus(context.Background(), stub, corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("normalizer called %d times, want 1 (stop rather than repeat a failed call)", stub.calls)
	}
	if report.Gates.StructurallyValid {
		t.Error("a provider failure must fail the structural gate")
	}
	if report.Gates.Met() {
		t.Error("gates must not be met after a provider failure")
	}
	if report.Cases != 1 || report.Failed != 1 {
		t.Errorf("cases=%d failed=%d, want 1/1", report.Cases, report.Failed)
	}
}

func TestRunCorpusKeepsRunningAfterASemanticFailure(t *testing.T) {
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Acceptance:    Acceptance{MinSemanticPassRate: 1.0, MaxRepairRate: 1.0},
		Cases: []EvalCase{
			{ID: "a", Text: "one"},
			{ID: "b", Text: "two"},
		},
	}
	stub := &stubNormalizer{handler: func(string) (Result, error) {
		return Result{}, errors.Join(ErrValidation, errors.New("ready requires at least one requirement"))
	}}

	report, err := RunCorpus(context.Background(), stub, corpus)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if stub.calls != 2 {
		t.Errorf("normalizer called %d times, want 2", stub.calls)
	}
	if report.Gates.StructurallyValid {
		t.Error("a validation failure must fail the structural gate")
	}
}

func TestAcceptanceDefaultsMatchThePacket(t *testing.T) {
	if DefaultMinSemanticPassRate != 0.90 {
		t.Errorf("min semantic pass rate = %v, want 0.90", DefaultMinSemanticPassRate)
	}
	if DefaultMaxRepairRate != 0.20 {
		t.Errorf("max repair rate = %v, want 0.20", DefaultMaxRepairRate)
	}
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
