package discovery

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// SchemaVersion is the only discovery profile schema supported.
const SchemaVersion = 1

// MaxQueryTextLength bounds authored query text.
const MaxQueryTextLength = 200

// Structural profile errors. Every loader or validation problem wraps
// ErrProfile so the CLI can distinguish a profile problem (usage) from an
// execution failure.
var (
	// ErrProfile marks a discovery profile problem: unreadable, malformed,
	// structurally invalid, or inconsistent with the requested domain.
	ErrProfile = errors.New("discovery: invalid profile")
	// ErrUnsupportedSchema reports an unsupported profile schema version.
	ErrUnsupportedSchema = fmt.Errorf("%w: unsupported schema version", ErrProfile)
	// ErrPathEscape reports a profile path that would leave the repository root.
	ErrPathEscape = fmt.Errorf("%w: path escapes the repository root", ErrProfile)
)

// LoadProfile reads a discovery profile relative to root.
//
// Decoding is strict (unknown fields are rejected, so a typo never silently
// disables part of a plan) and every path must resolve beneath root.
func LoadProfile(root, profilePath string) (Profile, error) {
	path, err := resolveProfilePath(root, profilePath)
	if err != nil {
		return Profile{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: read %s: %v", ErrProfile, profilePath, err)
	}

	var profile Profile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("%w: decode %s: %v", ErrProfile, profilePath, err)
	}

	if profile.SchemaVersion != SchemaVersion {
		return Profile{}, fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchema, profile.SchemaVersion, SchemaVersion)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

// Validate checks a profile against the bounded structural rules. It is called
// by the loader and again by the service so a hand-built profile cannot skip
// validation.
func (p Profile) Validate() error {
	if p.PrimitiveID == "" {
		return fmt.Errorf("%w: primitive_id is empty", ErrProfile)
	}
	if p.ContractID == "" {
		return fmt.Errorf("%w: contract_id is empty", ErrProfile)
	}

	budget := DefaultBudget()
	if len(p.Providers) == 0 {
		return fmt.Errorf("%w: no providers are configured", ErrProfile)
	}
	if len(p.Providers) > budget.MaxProviders {
		return fmt.Errorf("%w: %d providers exceed the maximum of %d", ErrProfile, len(p.Providers), budget.MaxProviders)
	}

	known := make(map[string]struct{}, len(KnownProviderIDs()))
	for _, id := range KnownProviderIDs() {
		known[id] = struct{}{}
	}

	seenProviders := make(map[string]struct{}, len(p.Providers))
	for _, plan := range p.Providers {
		if plan.ID == "" {
			return fmt.Errorf("%w: provider id is empty", ErrProfile)
		}
		if _, duplicate := seenProviders[plan.ID]; duplicate {
			return fmt.Errorf("%w: duplicate provider %q", ErrProfile, plan.ID)
		}
		seenProviders[plan.ID] = struct{}{}
		if _, ok := known[plan.ID]; !ok {
			return fmt.Errorf("%w: unknown provider %q", ErrProfile, plan.ID)
		}
		if err := validateQueries(plan); err != nil {
			return err
		}
	}
	return nil
}

func validateQueries(plan ProviderPlan) error {
	budget := DefaultBudget()
	if len(plan.Queries) == 0 {
		return fmt.Errorf("%w: provider %q has no queries", ErrProfile, plan.ID)
	}
	if len(plan.Queries) > budget.MaxQueries {
		return fmt.Errorf("%w: provider %q declares %d queries, maximum is %d",
			ErrProfile, plan.ID, len(plan.Queries), budget.MaxQueries)
	}

	seen := make(map[string]struct{}, len(plan.Queries))
	for _, query := range plan.Queries {
		text := strings.TrimSpace(query.Text)
		if text == "" {
			return fmt.Errorf("%w: provider %q has an empty query", ErrProfile, plan.ID)
		}
		if len(text) > MaxQueryTextLength {
			return fmt.Errorf("%w: provider %q query is %d bytes, maximum is %d",
				ErrProfile, plan.ID, len(text), MaxQueryTextLength)
		}
		if _, duplicate := seen[text]; duplicate {
			return fmt.Errorf("%w: provider %q repeats query %q", ErrProfile, plan.ID, text)
		}
		seen[text] = struct{}{}

		if query.Limit < 1 {
			return fmt.Errorf("%w: provider %q query %q has limit %d, minimum is 1",
				ErrProfile, plan.ID, text, query.Limit)
		}
		if query.Limit > budget.MaxResults {
			return fmt.Errorf("%w: provider %q query %q has limit %d, maximum is %d",
				ErrProfile, plan.ID, text, query.Limit, budget.MaxResults)
		}
	}
	return nil
}

// ValidateProfileDomain checks that the profile describes the primitive and
// contract it is about to be run against. Structural mistakes surface here
// instead of silently discovering for the wrong primitive.
func ValidateProfileDomain(profile Profile, primitive model.Primitive, contract model.Contract) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if profile.PrimitiveID != primitive.ID {
		return fmt.Errorf("%w: profile targets primitive %q but the registry has %q",
			ErrProfile, profile.PrimitiveID, primitive.ID)
	}
	if profile.ContractID != contract.ID {
		return fmt.Errorf("%w: profile targets contract %q but the registry has %q",
			ErrProfile, profile.ContractID, contract.ID)
	}
	if primitive.ID == "" {
		return fmt.Errorf("%w: primitive ID is empty", ErrProfile)
	}
	if contract.ID == "" {
		return fmt.Errorf("%w: contract ID is empty", ErrProfile)
	}
	if primitive.ContractID != contract.ID {
		return fmt.Errorf("%w: primitive %q declares contract %q but the registry has %q",
			ErrProfile, primitive.ID, primitive.ContractID, contract.ID)
	}
	if contract.PrimitiveID != primitive.ID {
		return fmt.Errorf("%w: contract %q declares primitive %q but the registry has %q",
			ErrProfile, contract.ID, contract.PrimitiveID, primitive.ID)
	}
	return nil
}

// resolveProfilePath joins a profile path to root and refuses anything that
// would leave the repository root.
func resolveProfilePath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("%w: empty profile path", ErrProfile)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathEscape, rel)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve repository root: %v", ErrProfile, err)
	}
	rootAbs = filepath.Clean(rootAbs)

	joined := filepath.Clean(filepath.Join(rootAbs, rel))
	if joined != rootAbs && !strings.HasPrefix(joined, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	return joined, nil
}
