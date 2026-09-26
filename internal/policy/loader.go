package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Structural loader errors, mirroring the catalogue loader's conventions.
var (
	ErrUnsupportedSchema = errors.New("policy: unsupported schema version")
	ErrPathEscape        = errors.New("policy: path escapes the repository root")
	ErrInvalidPolicy     = errors.New("policy: invalid policy profile")
	ErrInvalidFeedback   = errors.New("policy: invalid feedback")
)

// MaxShortlistCeiling is the hard upper bound on policy.selection.max_options.
// Even an authored profile cannot ask for an unbounded dump of every
// candidate.
const MaxShortlistCeiling = 5

// DTOs mirror the authored YAML. YAML is a source format, not a domain format:
// unknown fields are rejected so a typo cannot silently disable a rule.
type policyDTO struct {
	SchemaVersion int             `yaml:"schema_version"`
	ID            string          `yaml:"id"`
	Reuse         reuseDTO        `yaml:"reuse"`
	Licence       licenceDTO      `yaml:"license"`
	Security      securityDTO     `yaml:"security"`
	Dependencies  dependenciesDTO `yaml:"dependencies"`
	Maintenance   maintenanceDTO  `yaml:"maintenance"`
	Source        sourceDTO       `yaml:"source"`
	Selection     selectionDTO    `yaml:"selection"`
}

type reuseDTO struct {
	Allowed   []string `yaml:"allowed"`
	Preferred []string `yaml:"preferred"`
}

type licenceDTO struct {
	Allow    []string `yaml:"allow"`
	Deny     []string `yaml:"deny"`
	Unknown  string   `yaml:"unknown"`
	Multiple string   `yaml:"multiple"`
	Unlisted string   `yaml:"unlisted"`
}

type securityDTO struct {
	KnownAdvisory string `yaml:"known_advisory"`
	Unknown       string `yaml:"unknown"`
}

type dependenciesDTO struct {
	Unknown   string `yaml:"unknown"`
	MaxDirect *int   `yaml:"max_direct"`
}

type maintenanceDTO struct {
	Archived            string `yaml:"archived"`
	Deprecated          string `yaml:"deprecated"`
	Stale               string `yaml:"stale"`
	Unknown             string `yaml:"unknown"`
	MaxDaysSincePush    *int   `yaml:"max_days_since_push"`
	MaxDaysSinceRelease *int   `yaml:"max_days_since_release"`
}

type sourceDTO struct {
	RequireRevisionFor []string `yaml:"require_revision_for"`
	MissingRevision    string   `yaml:"missing_revision"`
}

type selectionDTO struct {
	MaxOptions int `yaml:"max_options"`
}

// Load reads a policy profile relative to root. Every referenced path must
// resolve beneath root, so a profile path can never reach outside the
// repository.
func Load(root, path string) (Policy, error) {
	file, err := resolvePath(root, path)
	if err != nil {
		return Policy{}, err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return Policy{}, fmt.Errorf("policy: read %s: %w", file, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var dto policyDTO
	if err := decoder.Decode(&dto); err != nil {
		return Policy{}, fmt.Errorf("policy: decode %s: %w", file, err)
	}

	policy := mapPolicy(dto)
	if err := policy.Validate(); err != nil {
		return Policy{}, fmt.Errorf("policy: %s: %w", file, err)
	}
	return policy, nil
}

func mapPolicy(dto policyDTO) Policy {
	return Policy{
		SchemaVersion: dto.SchemaVersion,
		ID:            dto.ID,
		Reuse: ReusePolicy{
			Allowed:   reuseModes(dto.Reuse.Allowed),
			Preferred: reuseModes(dto.Reuse.Preferred),
		},
		Licence: LicencePolicy{
			Allow:    append([]string(nil), dto.Licence.Allow...),
			Deny:     append([]string(nil), dto.Licence.Deny...),
			Unknown:  defaultedAction(dto.Licence.Unknown),
			Multiple: defaultedAction(dto.Licence.Multiple),
			Unlisted: defaultedAction(dto.Licence.Unlisted),
		},
		Security: SecurityPolicy{
			KnownAdvisory: defaultedAction(dto.Security.KnownAdvisory),
			Unknown:       defaultedAction(dto.Security.Unknown),
		},
		Dependencies: DependencyPolicy{
			Unknown:   defaultedAction(dto.Dependencies.Unknown),
			MaxDirect: dto.Dependencies.MaxDirect,
		},
		Maintenance: MaintenancePolicy{
			Archived:            defaultedAction(dto.Maintenance.Archived),
			Deprecated:          defaultedAction(dto.Maintenance.Deprecated),
			Stale:               defaultedAction(dto.Maintenance.Stale),
			Unknown:             defaultedAction(dto.Maintenance.Unknown),
			MaxDaysSincePush:    dto.Maintenance.MaxDaysSincePush,
			MaxDaysSinceRelease: dto.Maintenance.MaxDaysSinceRelease,
		},
		Source: SourcePolicy{
			RequireRevisionFor: reuseModes(dto.Source.RequireRevisionFor),
			MissingRevision:    defaultedAction(dto.Source.MissingRevision),
		},
		Selection: SelectionPolicy{MaxOptions: dto.Selection.MaxOptions},
	}
}

// defaultedAction maps an omitted action onto review.
//
// An incomplete profile must never silently allow something, so "review" is the
// conservative reading of an absent rule. Explicit unknown values still fail
// validation, which is what catches a typo.
func defaultedAction(action string) Action {
	if strings.TrimSpace(action) == "" {
		return ActionReview
	}
	return Action(action)
}

func reuseModes(values []string) []model.ReuseMode {
	if len(values) == 0 {
		return nil
	}
	out := make([]model.ReuseMode, 0, len(values))
	for _, value := range values {
		out = append(out, model.ReuseMode(value))
	}
	return out
}

// Validate rejects a malformed profile before any rule is evaluated.
func (p Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchema, p.SchemaVersion, SchemaVersion)
	}
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: profile id is empty", ErrInvalidPolicy)
	}

	if err := validateReuse(p.Reuse.Allowed, "reuse.allowed"); err != nil {
		return err
	}
	if err := validateReuse(p.Reuse.Preferred, "reuse.preferred"); err != nil {
		return err
	}
	allowed := allowedModes(p.Reuse.Allowed)
	for _, mode := range p.Reuse.Preferred {
		if !containsMode(allowed, mode) {
			return fmt.Errorf("%w: reuse.preferred names %q which reuse.allowed does not permit", ErrInvalidPolicy, mode)
		}
	}

	if err := validateLicenceList(p.Licence.Allow, "license.allow"); err != nil {
		return err
	}
	if err := validateLicenceList(p.Licence.Deny, "license.deny"); err != nil {
		return err
	}
	for _, value := range p.Licence.Allow {
		if containsString(p.Licence.Deny, value) {
			return fmt.Errorf("%w: licence %q appears in both license.allow and license.deny", ErrInvalidPolicy, value)
		}
	}

	actions := []struct {
		name   string
		action Action
	}{
		{"license.unknown", p.Licence.Unknown},
		{"license.multiple", p.Licence.Multiple},
		{"license.unlisted", p.Licence.Unlisted},
		{"security.known_advisory", p.Security.KnownAdvisory},
		{"security.unknown", p.Security.Unknown},
		{"dependencies.unknown", p.Dependencies.Unknown},
		{"maintenance.archived", p.Maintenance.Archived},
		{"maintenance.deprecated", p.Maintenance.Deprecated},
		{"maintenance.stale", p.Maintenance.Stale},
		{"maintenance.unknown", p.Maintenance.Unknown},
		{"source.missing_revision", p.Source.MissingRevision},
	}
	for _, entry := range actions {
		if !ValidAction(entry.action) {
			return fmt.Errorf("%w: %s has invalid action %q", ErrInvalidPolicy, entry.name, entry.action)
		}
	}

	if p.Dependencies.MaxDirect != nil && *p.Dependencies.MaxDirect < 0 {
		return fmt.Errorf("%w: dependencies.max_direct is negative (%d)", ErrInvalidPolicy, *p.Dependencies.MaxDirect)
	}
	if p.Maintenance.MaxDaysSincePush != nil && *p.Maintenance.MaxDaysSincePush < 0 {
		return fmt.Errorf("%w: maintenance.max_days_since_push is negative (%d)", ErrInvalidPolicy, *p.Maintenance.MaxDaysSincePush)
	}
	if p.Maintenance.MaxDaysSinceRelease != nil && *p.Maintenance.MaxDaysSinceRelease < 0 {
		return fmt.Errorf("%w: maintenance.max_days_since_release is negative (%d)", ErrInvalidPolicy, *p.Maintenance.MaxDaysSinceRelease)
	}

	if err := validateReuse(p.Source.RequireRevisionFor, "source.require_revision_for"); err != nil {
		return err
	}

	if p.Selection.MaxOptions < 1 || p.Selection.MaxOptions > MaxShortlistCeiling {
		return fmt.Errorf("%w: selection.max_options is %d, must be between 1 and %d",
			ErrInvalidPolicy, p.Selection.MaxOptions, MaxShortlistCeiling)
	}
	return nil
}

func validateReuse(modes []model.ReuseMode, field string) error {
	seen := make(map[model.ReuseMode]struct{}, len(modes))
	for _, mode := range modes {
		if !validReuseMode(mode) {
			return fmt.Errorf("%w: %s names unsupported reuse mode %q", ErrInvalidPolicy, field, mode)
		}
		if _, duplicate := seen[mode]; duplicate {
			return fmt.Errorf("%w: %s repeats reuse mode %q", ErrInvalidPolicy, field, mode)
		}
		seen[mode] = struct{}{}
	}
	return nil
}

func validateLicenceList(values []string, field string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s contains an empty entry", ErrInvalidPolicy, field)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: %s repeats licence %q", ErrInvalidPolicy, field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validReuseMode(mode model.ReuseMode) bool {
	switch mode {
	case model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference:
		return true
	default:
		return false
	}
}

// allowedModes returns the effective allowed set: an empty authored list means
// no restriction, which is the same as permitting every supported mode.
func allowedModes(configured []model.ReuseMode) []model.ReuseMode {
	if len(configured) == 0 {
		return ReuseModes()
	}
	return configured
}

func containsMode(modes []model.ReuseMode, want model.ReuseMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// feedbackFile is the authored JSON shape for structured "Not quite" input.
type feedbackFile struct {
	Feedback []feedbackDTO `json:"feedback"`
}

type feedbackDTO struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

// LoadFeedback reads a structured feedback file relative to root, rejecting
// unknown fields and unsupported reasons so a typo fails loudly instead of
// silently doing nothing.
func LoadFeedback(root, path string) ([]Feedback, error) {
	file, err := resolvePath(root, path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", file, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload feedbackFile
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %v", ErrInvalidFeedback, file, err)
	}
	if len(payload.Feedback) == 0 {
		return nil, fmt.Errorf("%w: %s contains no feedback entries", ErrInvalidFeedback, file)
	}

	feedback := make([]Feedback, 0, len(payload.Feedback))
	for _, entry := range payload.Feedback {
		reason := FeedbackReason(entry.Reason)
		if !SupportedFeedback(reason) {
			return nil, fmt.Errorf("%w: unsupported feedback reason %q", ErrInvalidFeedback, entry.Reason)
		}
		if strings.TrimSpace(entry.CandidateID) == "" {
			return nil, fmt.Errorf("%w: feedback reason %q has no candidate id", ErrInvalidFeedback, entry.Reason)
		}
		feedback = append(feedback, Feedback{CandidateID: entry.CandidateID, Reason: reason})
	}
	return feedback, nil
}

// resolvePath joins a relative path to root and refuses anything that would
// leave it. It mirrors the catalogue loader so both file inputs behave the same
// way, and additionally rejects a backslash outright: a Windows-style separator
// in an authored path must fail identically on every platform rather than being
// treated as a plain character on POSIX.
func resolvePath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("policy: an empty path was supplied")
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("%w: %q contains a Windows path separator", ErrPathEscape, rel)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathEscape, rel)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	joined := filepath.Clean(filepath.Join(rootAbs, rel))
	if joined != rootAbs && !strings.HasPrefix(joined, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	return joined, nil
}
