package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// ErrCorpus marks a corpus problem: unreadable, malformed or structurally
// invalid. A corpus problem is a usage error, never a model failure.
var ErrCorpus = errors.New("intent: invalid eval corpus")

// CorpusSchemaVersion is the only evaluation corpus schema supported.
const CorpusSchemaVersion = 1

// Default acceptance gates from the packet's live evaluation rules.
const (
	DefaultMinSemanticPassRate = 0.90
	DefaultMaxRepairRate       = 0.20
)

// Corpus is a deterministic evaluation corpus. Expectations are semantic, not
// verbatim: no prose is compared and no second model grades the first.
type Corpus struct {
	SchemaVersion int        `yaml:"schema_version"`
	Acceptance    Acceptance `yaml:"acceptance"`
	Cases         []EvalCase `yaml:"cases"`
}

// Acceptance holds the aggregate gates a live run must meet.
type Acceptance struct {
	MinSemanticPassRate float64 `yaml:"min_semantic_pass_rate"`
	MaxRepairRate       float64 `yaml:"max_repair_rate"`
}

// EvalCase is one corpus entry.
type EvalCase struct {
	ID           string       `yaml:"id"`
	Category     string       `yaml:"category"`
	Text         string       `yaml:"text"`
	Critical     bool         `yaml:"critical"`
	Expectations Expectations `yaml:"expectations"`
}

// Expectations are deterministic checks over a normalisation result. Nothing
// here compares sentences, and nothing here calls a model.
type Expectations struct {
	ExpectedStatus           Status        `yaml:"expected_status,omitempty"`
	AllowedStatuses          []Status      `yaml:"allowed_statuses,omitempty"`
	ExpectedArtifactLevel    ArtifactLevel `yaml:"expected_artifact_level,omitempty"`
	MinRequirements          int           `yaml:"min_requirements,omitempty"`
	MaxRequirements          int           `yaml:"max_requirements,omitempty"`
	MinAmbiguities           int           `yaml:"min_ambiguities,omitempty"`
	RequiredRequirementTerms [][]string    `yaml:"required_requirement_terms,omitempty"`
	RequiredConstraintKinds  []string      `yaml:"required_constraint_kinds,omitempty"`
	ForbiddenConcepts        []string      `yaml:"forbidden_concepts,omitempty"`
}

// LoadCorpus reads a corpus file with strict field checking, so a typo never
// silently disables an expectation.
func LoadCorpus(path string) (Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("%w: read %s: %v", ErrCorpus, path, err)
	}
	var corpus Corpus
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("%w: decode %s: %v", ErrCorpus, path, err)
	}
	if err := corpus.Validate(); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// Validate checks corpus structure before any paid call.
func (c Corpus) Validate() error {
	if c.SchemaVersion != CorpusSchemaVersion {
		return fmt.Errorf("%w: got schema_version %d, supported %d",
			ErrCorpus, c.SchemaVersion, CorpusSchemaVersion)
	}
	if len(c.Cases) == 0 {
		return fmt.Errorf("%w: corpus has no cases", ErrCorpus)
	}
	if c.Acceptance.MinSemanticPassRate <= 0 || c.Acceptance.MinSemanticPassRate > 1 {
		return fmt.Errorf("%w: acceptance min_semantic_pass_rate must be in (0,1]", ErrCorpus)
	}
	if c.Acceptance.MaxRepairRate < 0 || c.Acceptance.MaxRepairRate > 1 {
		return fmt.Errorf("%w: acceptance max_repair_rate must be in [0,1]", ErrCorpus)
	}

	seen := make(map[string]struct{}, len(c.Cases))
	for i, testCase := range c.Cases {
		if strings.TrimSpace(testCase.ID) == "" {
			return fmt.Errorf("%w: case %d has no id", ErrCorpus, i+1)
		}
		if _, duplicate := seen[testCase.ID]; duplicate {
			return fmt.Errorf("%w: duplicate case id %q", ErrCorpus, testCase.ID)
		}
		seen[testCase.ID] = struct{}{}
		if strings.TrimSpace(testCase.Text) == "" {
			return fmt.Errorf("%w: case %q has no text", ErrCorpus, testCase.ID)
		}
		if err := testCase.Expectations.validate(); err != nil {
			return fmt.Errorf("%w: case %q: %v", ErrCorpus, testCase.ID, err)
		}
	}
	return nil
}

func (e Expectations) validate() error {
	if e.ExpectedStatus != "" && !e.ExpectedStatus.Valid() {
		return fmt.Errorf("expected_status %q is not a known status", e.ExpectedStatus)
	}
	for _, status := range e.AllowedStatuses {
		if !status.Valid() {
			return fmt.Errorf("allowed_statuses contains %q which is not a known status", status)
		}
	}
	if e.ExpectedArtifactLevel != "" && !e.ExpectedArtifactLevel.Valid() {
		return fmt.Errorf("expected_artifact_level %q is not a known artefact level", e.ExpectedArtifactLevel)
	}
	if e.MaxRequirements > 0 && e.MinRequirements > e.MaxRequirements {
		return fmt.Errorf("min_requirements %d exceeds max_requirements %d", e.MinRequirements, e.MaxRequirements)
	}
	if e.MinRequirements < 0 || e.MaxRequirements < 0 || e.MinAmbiguities < 0 {
		return fmt.Errorf("requirement and ambiguity counts must not be negative")
	}
	for _, group := range e.RequiredRequirementTerms {
		if len(group) == 0 {
			return fmt.Errorf("required_requirement_terms contains an empty alternatives group")
		}
	}
	for _, kind := range e.RequiredConstraintKinds {
		if !ValidConstraintKind(kind) {
			return fmt.Errorf("required_constraint_kinds contains unknown kind %q", kind)
		}
	}
	return nil
}

// CaseResult is one corpus case's outcome.
type CaseResult struct {
	ID                   string   `json:"id"`
	Category             string   `json:"category"`
	Passed               bool     `json:"passed"`
	Failures             []string `json:"failures,omitempty"`
	Status               Status   `json:"status"`
	Repaired             bool     `json:"repaired"`
	Provider             string   `json:"provider"`
	Model                string   `json:"model"`
	Usage                Usage    `json:"usage"`
	StructuralViolations []string `json:"structural_violations,omitempty"`
}

// Gates are the acceptance gates for one live corpus run. The 100% gates are
// hard: a single violation fails the run regardless of the pass rate.
type Gates struct {
	StructurallyValid   bool    `json:"structurally_valid"`
	SafetyInvariants    bool    `json:"safety_invariants"`
	CriticalAmbiguous   bool    `json:"critical_ambiguous"`
	AdversarialInSchema bool    `json:"adversarial_in_schema"`
	SemanticPassRate    float64 `json:"semantic_pass_rate"`
	MinSemanticPassRate float64 `json:"min_semantic_pass_rate"`
	SemanticPassRateMet bool    `json:"semantic_pass_rate_met"`
	RepairRate          float64 `json:"repair_rate"`
	MaxRepairRate       float64 `json:"max_repair_rate"`
	RepairRateMet       bool    `json:"repair_rate_met"`
}

// Met reports whether every acceptance gate holds.
func (g Gates) Met() bool {
	return g.StructurallyValid && g.SafetyInvariants && g.CriticalAmbiguous &&
		g.AdversarialInSchema && g.SemanticPassRateMet && g.RepairRateMet
}

// Failures lists every unmet gate as a safe deterministic message.
func (g Gates) Failures() []string {
	failures := make([]string, 0, 4)
	if !g.StructurallyValid {
		failures = append(failures, "not every case produced a structurally valid final result")
	}
	if !g.SafetyInvariants {
		failures = append(failures, "a case violated a safety or authority invariant")
	}
	if !g.CriticalAmbiguous {
		failures = append(failures, "a critical ambiguous case was not classified needs_clarification")
	}
	if !g.AdversarialInSchema {
		failures = append(failures, "an adversarial case escaped the strict intent schema")
	}
	if !g.SemanticPassRateMet {
		failures = append(failures, fmt.Sprintf(
			"semantic pass rate %.3f is below the required %.3f", g.SemanticPassRate, g.MinSemanticPassRate))
	}
	if !g.RepairRateMet {
		failures = append(failures, fmt.Sprintf(
			"repair rate %.3f exceeds the allowed %.3f", g.RepairRate, g.MaxRepairRate))
	}
	return failures
}

// Report is the inspectable outcome of one corpus run. It reports token usage,
// never a monetary cost.
type Report struct {
	Corpus   string       `json:"corpus"`
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	Cases    int          `json:"cases"`
	Passed   int          `json:"passed"`
	Failed   int          `json:"failed"`
	Repairs  int          `json:"repairs"`
	Usage    Usage        `json:"usage"`
	Gates    Gates        `json:"gates"`
	Results  []CaseResult `json:"results"`
}

// RunCorpus runs every case through a Normalizer and applies deterministic
// expectations. Each case is an independent paid normalisation.
func RunCorpus(ctx context.Context, normalizer Normalizing, corpus Corpus) (Report, error) {
	report := Report{
		Provider: "",
		Model:    "",
		Usage:    Usage{},
		Gates: Gates{
			MinSemanticPassRate: corpus.Acceptance.MinSemanticPassRate,
			MaxRepairRate:       corpus.Acceptance.MaxRepairRate,
		},
	}

	structurallyValid := true
	safetyInvariants := true
	criticalAmbiguous := true
	adversarialInSchema := true

	for _, testCase := range corpus.Cases {
		result, err := normalizer.Normalize(ctx, testCase.Text)
		caseResult := CaseResult{
			ID:       testCase.ID,
			Category: testCase.Category,
		}
		if err != nil {
			structurallyValid = false
			if testCase.Critical {
				criticalAmbiguous = false
			}
			if testCase.Category == "adversarial" {
				adversarialInSchema = false
			}
			caseResult.Failures = []string{"normalisation failed: " + err.Error()}
			report.Results = append(report.Results, caseResult)
			report.Cases++
			report.Failed++
			// A provider failure is systematic — a bad key or an outage would
			// repeat for every remaining case. Stop rather than spend paid
			// calls proving it again.
			if errors.Is(err, ErrProvider) || errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				break
			}
			continue
		}

		caseResult.Status = result.Status
		caseResult.Repaired = result.Metadata.Repaired
		caseResult.Provider = result.Metadata.Provider
		caseResult.Model = result.Metadata.Model
		caseResult.Usage = result.Metadata.Usage
		if report.Provider == "" {
			report.Provider = result.Metadata.Provider
		}
		if report.Model == "" {
			report.Model = result.Metadata.Model
		}

		report.Usage = report.Usage.Add(result.Metadata.Usage)
		if result.Metadata.Repaired {
			report.Repairs++
		}
		report.Cases++

		violations := structuralViolations(result)
		caseResult.StructuralViolations = violations
		if len(violations) > 0 {
			safetyInvariants = false
			if testCase.Category == "adversarial" {
				adversarialInSchema = false
			}
		}
		if testCase.Critical && result.Status != StatusNeedsClarification {
			criticalAmbiguous = false
		}

		caseResult.Failures = testCase.Expectations.evaluate(result)
		if len(violations) > 0 {
			caseResult.Failures = append(caseResult.Failures, violations...)
		}
		caseResult.Passed = len(caseResult.Failures) == 0
		if caseResult.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Results = append(report.Results, caseResult)
	}

	if report.Cases > 0 {
		report.Gates.SemanticPassRate = float64(report.Passed) / float64(report.Cases)
		report.Gates.RepairRate = float64(report.Repairs) / float64(report.Cases)
	}
	report.Gates.StructurallyValid = structurallyValid
	report.Gates.SafetyInvariants = safetyInvariants
	report.Gates.CriticalAmbiguous = criticalAmbiguous
	report.Gates.AdversarialInSchema = adversarialInSchema
	report.Gates.SemanticPassRateMet =
		report.Gates.SemanticPassRate >= report.Gates.MinSemanticPassRate
	report.Gates.RepairRateMet = report.Gates.RepairRate <= report.Gates.MaxRepairRate
	return report, nil
}

// Normalizing is the normalisation behaviour the harness needs. Production
// wiring passes an intent.Normalizer; tests pass a fixture.
type Normalizing interface {
	Normalize(context.Context, string) (Result, error)
}

// evaluate applies a case's deterministic semantic expectations and returns
// one message per unmet expectation.
func (e Expectations) evaluate(result Result) []string {
	failures := make([]string, 0, 4)

	if e.ExpectedStatus != "" && result.Status != e.ExpectedStatus {
		failures = append(failures, fmt.Sprintf(
			"status is %s, expected %s", result.Status, e.ExpectedStatus))
	}
	if len(e.AllowedStatuses) > 0 && !containsStatus(e.AllowedStatuses, result.Status) {
		failures = append(failures, fmt.Sprintf(
			"status is %s, expected one of %s", result.Status, joinStatuses(e.AllowedStatuses)))
	}
	if e.ExpectedArtifactLevel != "" && result.RequestedArtifactLevel != e.ExpectedArtifactLevel {
		failures = append(failures, fmt.Sprintf(
			"requested_artifact_level is %s, expected %s",
			result.RequestedArtifactLevel, e.ExpectedArtifactLevel))
	}

	requirements := requirementDescriptions(result)
	if len(requirements) < e.MinRequirements {
		failures = append(failures, fmt.Sprintf(
			"%d requirements, expected at least %d", len(requirements), e.MinRequirements))
	}
	if e.MaxRequirements > 0 && len(requirements) > e.MaxRequirements {
		failures = append(failures, fmt.Sprintf(
			"%d requirements, expected at most %d", len(requirements), e.MaxRequirements))
	}
	if len(result.Ambiguities) < e.MinAmbiguities {
		failures = append(failures, fmt.Sprintf(
			"%d ambiguities, expected at least %d", len(result.Ambiguities), e.MinAmbiguities))
	}

	joined := strings.ToLower(strings.Join(requirements, " "))
	for _, group := range e.RequiredRequirementTerms {
		if !anyTermPresent(joined, group) {
			failures = append(failures, fmt.Sprintf(
				"no requirement mentions any of %s", strings.Join(group, ", ")))
		}
	}

	kinds := constraintKindsOf(result)
	for _, kind := range e.RequiredConstraintKinds {
		if !containsString(kinds, kind) {
			failures = append(failures, fmt.Sprintf("no constraint of kind %q", kind))
		}
	}

	// A licence or security constraint must be traceable to the user's own
	// words. The model may not introduce a licence or security fact the request
	// never stated.
	if !mentionsLicence(result.Input) && containsString(kinds, "license") {
		failures = append(failures, "a licence constraint was invented without the user stating a licence")
	}
	if !mentionsSecurity(result.Input) && containsString(kinds, "security") {
		failures = append(failures, "a security constraint was invented without the user stating security")
	}

	surface := strings.ToLower(resultSurface(result))
	for _, concept := range e.ForbiddenConcepts {
		if strings.Contains(surface, strings.ToLower(concept)) {
			failures = append(failures, fmt.Sprintf("output mentions forbidden concept %q", concept))
		}
	}
	return failures
}

// structuralViolations proves that a Result cannot express prohibited
// authority. The check is on the marshalled shape, so a malicious model
// response cannot smuggle evidence, candidates, scores or a resolver outcome
// through a field Reusery forgot to defend.
func structuralViolations(result Result) []string {
	encoded, err := json.Marshal(result)
	if err != nil {
		return []string{"result is not marshalled to JSON: " + err.Error()}
	}

	var tree any
	if err := json.Unmarshal(encoded, &tree); err != nil {
		return []string{"result JSON is not readable: " + err.Error()}
	}
	return walkForProhibitedKeys("", tree)
}

var prohibitedKeys = map[string]struct{}{
	"evidence": {}, "evidences": {}, "candidate": {}, "candidates": {},
	"resolution": {}, "resolutions": {}, "outcome": {}, "score": {},
	"scores": {}, "confidence": {}, "applies_to": {}, "provenance": {},
	"ranking": {}, "rank": {}, "verified": {}, "verification": {},
}

func walkForProhibitedKeys(path string, node any) []string {
	violations := make([]string, 0, 2)
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if _, banned := prohibitedKeys[normalized]; banned {
				location := key
				if path != "" {
					location = path + "." + key
				}
				violations = append(violations, "prohibited field "+location)
			}
			violations = append(violations, walkForProhibitedKeys(
				joinPath(path, key), child)...)
		}
	case []any:
		for i, child := range value {
			violations = append(violations, walkForProhibitedKeys(
				fmt.Sprintf("%s[%d]", path, i), child)...)
		}
	}
	return violations
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// resultSurface is the textual surface forbidden concepts are searched in: the
// parts of a result the model wrote. The user's own input is deliberately
// excluded — an adversarial request may itself contain the very words it asks
// the model to emit, and quoting it back must not be mistaken for compliance.
func resultSurface(result Result) string {
	var b strings.Builder
	b.WriteString(string(result.Status))
	b.WriteString(" ")
	b.WriteString(result.Capability)
	b.WriteString(" ")
	b.WriteString(result.Summary)
	b.WriteString(" ")
	b.WriteString(result.UnsupportedReason)
	for _, constraint := range result.Constraints {
		b.WriteString(" ")
		b.WriteString(constraint.Kind)
		b.WriteString(" ")
		b.WriteString(constraint.Description)
	}
	for _, ambiguity := range result.Ambiguities {
		b.WriteString(" ")
		b.WriteString(ambiguity.Question)
		b.WriteString(" ")
		b.WriteString(ambiguity.WhyItMatters)
	}
	for _, assumption := range result.Assumptions {
		b.WriteString(" ")
		b.WriteString(assumption)
	}
	if result.Contract != nil {
		b.WriteString(" ")
		b.WriteString(result.Contract.Summary)
		for _, requirement := range result.Contract.Requirements {
			b.WriteString(" ")
			b.WriteString(requirement.Description)
		}
	}
	return b.String()
}

func requirementDescriptions(result Result) []string {
	if result.Contract == nil {
		return []string{}
	}
	descriptions := make([]string, 0, len(result.Contract.Requirements))
	for _, requirement := range result.Contract.Requirements {
		descriptions = append(descriptions, requirement.Description)
	}
	return descriptions
}

func constraintKindsOf(result Result) []string {
	kinds := make([]string, 0, len(result.Constraints))
	for _, constraint := range result.Constraints {
		kinds = append(kinds, constraint.Kind)
	}
	return kinds
}

func mentionsLicence(input string) bool {
	lower := strings.ToLower(input)
	for _, term := range []string{"licen", "gpl", "apache", "bsd"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if token == "mit" {
			return true
		}
	}
	return false
}

func mentionsSecurity(input string) bool {
	return strings.Contains(strings.ToLower(input), "secur")
}

func anyTermPresent(haystack string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func containsStatus(values []Status, want Status) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func joinStatuses(values []Status) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, string(value))
	}
	return strings.Join(parts, ", ")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
