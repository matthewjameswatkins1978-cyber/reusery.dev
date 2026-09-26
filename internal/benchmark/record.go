// Package benchmark records and compares engineering measurements.
//
// The single most important rule here is the separation between what was
// MEASURED and what was MODELLED. A record never labels an estimate as a
// measurement, a comparison never reports a saving when a required baseline
// field is absent, and no single run is ever presented as proof that Reusery
// saved money. Packet 16 owns release-level validation claims; this package
// only owns inspectable recording and aggregation.
package benchmark

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only benchmark record schema supported for now.
const SchemaVersion = 1

// Variant identifies which side of a paired experiment produced a record.
type Variant string

const (
	VariantBaseline Variant = "baseline"
	VariantReusery  Variant = "reusery"
)

// ValidVariant reports whether a variant name is recognised.
func ValidVariant(variant Variant) bool {
	return variant == VariantBaseline || variant == VariantReusery
}

// Measured holds values that were actually observed. Every field is optional
// because an unobserved metric stays unknown rather than being guessed, and an
// unknown is preserved as unknown through aggregation.
type Measured struct {
	Success         *bool  `yaml:"success" json:"success"`
	ElapsedMs       *int64 `yaml:"elapsed_ms" json:"elapsed_ms"`
	InputTokens     *int   `yaml:"input_tokens" json:"input_tokens"`
	OutputTokens    *int   `yaml:"output_tokens" json:"output_tokens"`
	ReasoningTokens *int   `yaml:"reasoning_tokens" json:"reasoning_tokens"`
	ToolCalls       *int   `yaml:"tool_calls" json:"tool_calls"`
	TestRuns        *int   `yaml:"test_runs" json:"test_runs"`
	GeneratedLines  *int   `yaml:"generated_lines" json:"generated_lines"`

	ReusedSpecimenIDs []string `yaml:"reused_specimen_ids" json:"reused_specimen_ids"`
	ResolutionID      *int64   `yaml:"resolution_id" json:"resolution_id"`
}

// Estimated holds values that were modelled rather than observed. It is
// deliberately a separate section so an estimate can never be summed into a
// measured total.
type Estimated struct {
	EngineeringMinutes *int   `yaml:"engineering_minutes" json:"engineering_minutes"`
	Basis              string `yaml:"basis" json:"basis"`
}

// Record is one durable benchmark observation.
type Record struct {
	SchemaVersion int        `yaml:"schema_version" json:"schema_version"`
	TaskID        string     `yaml:"task_id" json:"task_id"`
	Variant       Variant    `yaml:"variant" json:"variant"`
	Model         string     `yaml:"model" json:"model"`
	Exploratory   bool       `yaml:"exploratory" json:"exploratory"`
	Measured      Measured   `yaml:"measured" json:"measured"`
	Estimated     *Estimated `yaml:"estimated,omitempty" json:"estimated,omitempty"`
	Notes         string     `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// Structural record errors.
var (
	ErrUnsupportedSchema = errors.New("benchmark: unsupported schema version")
	ErrInvalidRecord     = errors.New("benchmark: invalid record")
)

// Validate rejects a malformed record before it is stored or compared.
func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchema, r.SchemaVersion, SchemaVersion)
	}
	if r.TaskID == "" {
		return fmt.Errorf("%w: task_id is empty", ErrInvalidRecord)
	}
	if !ValidVariant(r.Variant) {
		return fmt.Errorf("%w: variant %q is not baseline or reusery", ErrInvalidRecord, r.Variant)
	}
	if r.Model == "" {
		return fmt.Errorf("%w: model is empty", ErrInvalidRecord)
	}
	if r.Estimated != nil && r.Estimated.EngineeringMinutes != nil && *r.Estimated.EngineeringMinutes < 0 {
		return fmt.Errorf("%w: estimated engineering_minutes is negative", ErrInvalidRecord)
	}
	return nil
}

// LoadRecord reads one YAML record, rejecting unknown fields so a typo fails
// loudly instead of silently recording nothing.
func LoadRecord(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("benchmark: read %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("benchmark: decode %s: %w", path, err)
	}
	if err := record.Validate(); err != nil {
		return Record{}, fmt.Errorf("benchmark: %s: %w", path, err)
	}
	return record, nil
}

// LoadRecords reads every YAML file directly inside one directory, in filename
// order so a corpus is reproducible.
func LoadRecords(dir string) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("benchmark: read dir %s: %w", dir, err)
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := filepath.Ext(entry.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}
		record, err := LoadRecord(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
