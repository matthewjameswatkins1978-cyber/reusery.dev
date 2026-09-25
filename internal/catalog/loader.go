// Package catalog loads repository-authored catalogue bundles into
// internal/model.
//
// YAML is a source format, not a domain format: the loader decodes into its
// own DTOs with strict field checking and maps them into internal/model. The
// domain model never learns what YAML is.
package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// SchemaVersion is the only manifest schema supported for now.
const SchemaVersion = 1

// Structural loader errors. Anything else is reported with a description of
// the offending file and value.
var (
	ErrUnsupportedSchema = errors.New("catalog: unsupported schema version")
	ErrPathEscape        = errors.New("catalog: path escapes the repository root")
)

// Bundle is a validated catalogue bundle ready to seed or reason about.
type Bundle struct {
	Primitive model.Primitive
	Contract  model.Contract
	Specimens []model.Specimen
	Evidence  []model.Evidence
}

// manifestDTO mirrors catalogue manifest YAML.
type manifestDTO struct {
	SchemaVersion int    `yaml:"schema_version"`
	PrimitiveFile string `yaml:"primitive_file"`
	ContractFile  string `yaml:"contract_file"`
	SpecimensFile string `yaml:"specimens_file"`
	EvidenceFile  string `yaml:"evidence_file"`
}

type primitiveDTO struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	ContractID  string   `yaml:"contract_id"`
}

type contractDTO struct {
	ID           string           `yaml:"id"`
	PrimitiveID  string           `yaml:"primitive_id"`
	Version      string           `yaml:"version"`
	Summary      string           `yaml:"summary"`
	Requirements []requirementDTO `yaml:"requirements"`
	// Notes are part of the canonical contract file but model.Contract has no
	// field for them yet. They are decoded so strict loading accepts the
	// authored file, and are intentionally not mapped: the canonical YAML
	// remains their home until a packet models them.
	Notes []string `yaml:"notes"`
}

type requirementDTO struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Kind        string `yaml:"kind"`
	Required    bool   `yaml:"required"`
}

type sourceDTO struct {
	URL      string `yaml:"url"`
	Revision string `yaml:"revision"`
	Path     string `yaml:"path"`
	License  string `yaml:"license"`
}

type specimenDTO struct {
	ID          string    `yaml:"id"`
	PrimitiveID string    `yaml:"primitive_id"`
	Name        string    `yaml:"name"`
	Source      sourceDTO `yaml:"source"`
	ReuseModes  []string  `yaml:"reuse_modes"`
}

type specimensDTO struct {
	Specimens []specimenDTO `yaml:"specimens"`
}

type evidenceDTO struct {
	ID          string    `yaml:"id"`
	SubjectID   string    `yaml:"subject_id"`
	Kind        string    `yaml:"kind"`
	Claim       string    `yaml:"claim"`
	Result      string    `yaml:"result"`
	Source      sourceDTO `yaml:"source"`
	ObservedAt  string    `yaml:"observed_at"`
	AppliesTo   string    `yaml:"applies_to"`
	Methodology string    `yaml:"methodology"`
	Artifact    string    `yaml:"artifact"`
}

type evidenceListDTO struct {
	Evidence []evidenceDTO `yaml:"evidence"`
}

// Load reads a manifest relative to root and returns a validated bundle.
// Every referenced path must resolve beneath root.
func Load(root, manifestPath string) (Bundle, error) {
	manifestFile, err := resolvePath(root, manifestPath)
	if err != nil {
		return Bundle{}, err
	}

	var manifest manifestDTO
	if err := decodeStrict(manifestFile, &manifest); err != nil {
		return Bundle{}, err
	}
	if manifest.SchemaVersion != SchemaVersion {
		return Bundle{}, fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchema, manifest.SchemaVersion, SchemaVersion)
	}

	primitive, err := loadPrimitive(root, manifest.PrimitiveFile)
	if err != nil {
		return Bundle{}, err
	}
	contract, err := loadContract(root, manifest.ContractFile)
	if err != nil {
		return Bundle{}, err
	}
	specimens, err := loadSpecimens(root, manifest.SpecimensFile)
	if err != nil {
		return Bundle{}, err
	}
	evidence, err := loadEvidence(root, manifest.EvidenceFile)
	if err != nil {
		return Bundle{}, err
	}

	bundle := Bundle{
		Primitive: primitive,
		Contract:  contract,
		Specimens: specimens,
		Evidence:  evidence,
	}
	if err := validateBundle(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func loadPrimitive(root, rel string) (model.Primitive, error) {
	path, err := resolvePath(root, rel)
	if err != nil {
		return model.Primitive{}, err
	}
	var dto primitiveDTO
	if err := decodeStrict(path, &dto); err != nil {
		return model.Primitive{}, err
	}
	return model.Primitive{
		ID:          dto.ID,
		Name:        dto.Name,
		Description: dto.Description,
		Tags:        dto.Tags,
		ContractID:  dto.ContractID,
	}, nil
}

func loadContract(root, rel string) (model.Contract, error) {
	path, err := resolvePath(root, rel)
	if err != nil {
		return model.Contract{}, err
	}
	var dto contractDTO
	if err := decodeStrict(path, &dto); err != nil {
		return model.Contract{}, err
	}

	requirements := make([]model.Requirement, 0, len(dto.Requirements))
	for _, req := range dto.Requirements {
		requirements = append(requirements, model.Requirement{
			ID:          req.ID,
			Description: req.Description,
			Kind:        req.Kind,
			Required:    req.Required,
		})
	}
	return model.Contract{
		ID:           dto.ID,
		PrimitiveID:  dto.PrimitiveID,
		Version:      dto.Version,
		Summary:      dto.Summary,
		Requirements: requirements,
	}, nil
}

func loadSpecimens(root, rel string) ([]model.Specimen, error) {
	path, err := resolvePath(root, rel)
	if err != nil {
		return nil, err
	}
	var dto specimensDTO
	if err := decodeStrict(path, &dto); err != nil {
		return nil, err
	}

	specimens := make([]model.Specimen, 0, len(dto.Specimens))
	for _, s := range dto.Specimens {
		modes := make([]model.ReuseMode, 0, len(s.ReuseModes))
		for _, mode := range s.ReuseModes {
			modes = append(modes, model.ReuseMode(mode))
		}
		specimens = append(specimens, model.Specimen{
			ID:          s.ID,
			PrimitiveID: s.PrimitiveID,
			Name:        s.Name,
			Source:      model.SourceRef{URL: s.Source.URL, Revision: s.Source.Revision, Path: s.Source.Path, License: s.Source.License},
			ReuseMode:   modes,
		})
	}
	return specimens, nil
}

func loadEvidence(root, rel string) ([]model.Evidence, error) {
	path, err := resolvePath(root, rel)
	if err != nil {
		return nil, err
	}
	var dto evidenceListDTO
	if err := decodeStrict(path, &dto); err != nil {
		return nil, err
	}

	items := make([]model.Evidence, 0, len(dto.Evidence))
	for _, e := range dto.Evidence {
		observedAt, err := time.Parse(time.RFC3339, e.ObservedAt)
		if err != nil {
			return nil, fmt.Errorf("catalog: evidence %q has invalid observed_at %q: %w", e.ID, e.ObservedAt, err)
		}
		items = append(items, model.Evidence{
			ID:          e.ID,
			SubjectID:   e.SubjectID,
			Kind:        e.Kind,
			Claim:       e.Claim,
			Result:      model.EvidenceResult(e.Result),
			Source:      model.SourceRef{URL: e.Source.URL, Revision: e.Source.Revision, Path: e.Source.Path, License: e.Source.License},
			ObservedAt:  observedAt,
			AppliesTo:   e.AppliesTo,
			Methodology: e.Methodology,
			Artifact:    e.Artifact,
		})
	}
	return items, nil
}

// validateBundle checks relationships after decoding.
func validateBundle(bundle Bundle) error {
	if bundle.Primitive.ID == "" {
		return errors.New("catalog: primitive ID is empty")
	}
	if bundle.Contract.ID == "" {
		return errors.New("catalog: contract ID is empty")
	}
	if bundle.Primitive.ContractID != bundle.Contract.ID {
		return fmt.Errorf("catalog: primitive %q declares contract %q but bundle has %q",
			bundle.Primitive.ID, bundle.Primitive.ContractID, bundle.Contract.ID)
	}
	if bundle.Contract.PrimitiveID != bundle.Primitive.ID {
		return fmt.Errorf("catalog: contract %q declares primitive %q but bundle has %q",
			bundle.Contract.ID, bundle.Contract.PrimitiveID, bundle.Primitive.ID)
	}

	requirementIDs := make(map[string]struct{}, len(bundle.Contract.Requirements))
	for _, req := range bundle.Contract.Requirements {
		if req.ID == "" {
			return errors.New("catalog: contract requirement ID is empty")
		}
		if _, duplicate := requirementIDs[req.ID]; duplicate {
			return fmt.Errorf("catalog: duplicate contract requirement ID %q", req.ID)
		}
		requirementIDs[req.ID] = struct{}{}
	}

	specimenIDs := make(map[string]struct{}, len(bundle.Specimens))
	for _, specimen := range bundle.Specimens {
		if specimen.ID == "" {
			return errors.New("catalog: specimen ID is empty")
		}
		if _, duplicate := specimenIDs[specimen.ID]; duplicate {
			return fmt.Errorf("catalog: duplicate specimen ID %q", specimen.ID)
		}
		specimenIDs[specimen.ID] = struct{}{}

		if specimen.PrimitiveID != bundle.Primitive.ID {
			return fmt.Errorf("catalog: specimen %q declares primitive %q, want %q",
				specimen.ID, specimen.PrimitiveID, bundle.Primitive.ID)
		}
		if specimen.Source.URL == "" && specimen.Source.Path == "" {
			return fmt.Errorf("catalog: specimen %q has no source provenance", specimen.ID)
		}
		for _, mode := range specimen.ReuseMode {
			if !validReuseMode(mode) {
				return fmt.Errorf("catalog: specimen %q declares unsupported reuse mode %q", specimen.ID, mode)
			}
		}
	}

	evidenceIDs := make(map[string]struct{}, len(bundle.Evidence))
	for _, evidence := range bundle.Evidence {
		if evidence.ID == "" {
			return errors.New("catalog: evidence ID is empty")
		}
		if _, duplicate := evidenceIDs[evidence.ID]; duplicate {
			return fmt.Errorf("catalog: duplicate evidence ID %q", evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}

		if !validEvidenceResult(evidence.Result) {
			return fmt.Errorf("catalog: evidence %q has unsupported result %q", evidence.ID, evidence.Result)
		}
		if _, known := specimenIDs[evidence.SubjectID]; !known {
			return fmt.Errorf("catalog: evidence %q has subject %q which is not a bundle specimen", evidence.ID, evidence.SubjectID)
		}
		if evidence.AppliesTo != "" {
			if _, known := requirementIDs[evidence.AppliesTo]; !known {
				return fmt.Errorf("catalog: evidence %q applies to unknown requirement %q", evidence.ID, evidence.AppliesTo)
			}
		}
		if evidence.Source.URL == "" && evidence.Source.Path == "" {
			return fmt.Errorf("catalog: evidence %q has no source provenance", evidence.ID)
		}
		if evidence.ObservedAt.IsZero() {
			return fmt.Errorf("catalog: evidence %q has no observed_at", evidence.ID)
		}
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

func validEvidenceResult(result model.EvidenceResult) bool {
	switch result {
	case model.EvidencePass, model.EvidenceFail, model.EvidenceUnknown, model.EvidenceInfo:
		return true
	default:
		return false
	}
}

// resolvePath joins a manifest-relative path to root and refuses anything that
// would leave the repository root.
func resolvePath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("catalog: manifest references an empty path")
	}
	// A path that is absolute on this platform, or merely rooted in POSIX
	// style, is never relative to the repository root.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
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

// decodeStrict decodes one YAML file, rejecting unknown fields.
func decodeStrict(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("catalog: read %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("catalog: decode %s: %w", path, err)
	}
	return nil
}
