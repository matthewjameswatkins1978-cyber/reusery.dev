package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	manifestYAML = `schema_version: 1
primitive_file: catalogue/primitive.yaml
contract_file: catalogue/contract.yaml
specimens_file: catalogue/specimens.yaml
evidence_file: catalogue/evidence.yaml
`

	primitiveYAML = `id: test/primitive
name: Test primitive
description: A test primitive
tags:
  - one
contract_id: test/primitive/v1
`

	contractYAML = `id: test/primitive/v1
primitive_id: test/primitive
version: "1"
summary: Test contract
requirements:
  - id: req-one
    description: first
    kind: behavior
    required: true
  - id: req-two
    description: second
    kind: behavior
    required: false
`

	specimensYAML = `specimens:
  - id: specimen-a
    primitive_id: test/primitive
    name: Specimen A
    source:
      path: catalogue/specimens.yaml
    reuse_modes:
      - adapt
  - id: specimen-b
    primitive_id: test/primitive
    name: Specimen B
    source:
      path: catalogue/specimens.yaml
    reuse_modes:
      - dependency
`

	evidenceYAML = `evidence:
  - id: ev-a
    subject_id: specimen-a
    kind: fixture
    claim: claims req-one
    result: pass
    source:
      path: catalogue/evidence.yaml
    observed_at: "2026-01-01T00:00:00Z"
    applies_to: req-one
    methodology: fixture
`
)

// tempBundle writes a valid bundle into a temporary directory, then applies
// mutate so a single test can break exactly one thing.
func tempBundle(t *testing.T, mutate func(map[string]string)) (root, manifest string) {
	t.Helper()

	files := map[string]string{
		"catalogue/manifest.yaml":  manifestYAML,
		"catalogue/primitive.yaml": primitiveYAML,
		"catalogue/contract.yaml":  contractYAML,
		"catalogue/specimens.yaml": specimensYAML,
		"catalogue/evidence.yaml":  evidenceYAML,
	}
	if mutate != nil {
		mutate(files)
	}

	root = t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root, "catalogue/manifest.yaml"
}

func mustFail(t *testing.T, root, manifest, what string) error {
	t.Helper()

	_, err := Load(root, manifest)
	if err == nil {
		t.Fatalf("%s: expected an error, got none", what)
	}
	return err
}

func TestLoadValidBundle(t *testing.T) {
	root, manifest := tempBundle(t, nil)

	bundle, err := Load(root, manifest)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if bundle.Primitive.ID != "test/primitive" || bundle.Primitive.ContractID != "test/primitive/v1" {
		t.Errorf("primitive = %#v", bundle.Primitive)
	}
	if len(bundle.Primitive.Tags) != 1 || bundle.Primitive.Tags[0] != "one" {
		t.Errorf("tags = %v", bundle.Primitive.Tags)
	}
	if bundle.Contract.ID != "test/primitive/v1" {
		t.Errorf("contract = %#v", bundle.Contract)
	}
	if len(bundle.Contract.Requirements) != 2 {
		t.Fatalf("requirements = %d, want 2", len(bundle.Contract.Requirements))
	}
	if !bundle.Contract.Requirements[0].Required || bundle.Contract.Requirements[1].Required {
		t.Errorf("requirement order/flags not preserved: %#v", bundle.Contract.Requirements)
	}
	if len(bundle.Specimens) != 2 {
		t.Fatalf("specimens = %d, want 2", len(bundle.Specimens))
	}
	if len(bundle.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1", len(bundle.Evidence))
	}
	if bundle.Evidence[0].Result != "pass" {
		t.Errorf("evidence result = %q", bundle.Evidence[0].Result)
	}
	if bundle.Evidence[0].Source.Path == "" {
		t.Error("evidence source provenance was dropped")
	}
}

// The repository-authored bundle is the real end-to-end input.
func TestLoadRepositoryBundle(t *testing.T) {
	root, manifest := filepath.Join("..", ".."), filepath.FromSlash("catalogue/dev/bounded-subprocess/manifest.yaml")

	bundle, err := Load(root, manifest)
	if err != nil {
		t.Fatalf("Load repository bundle: %v", err)
	}

	if bundle.Primitive.ID != "process/bounded-subprocess" {
		t.Errorf("primitive ID = %q", bundle.Primitive.ID)
	}
	if len(bundle.Contract.Requirements) != 11 {
		t.Fatalf("requirements = %d, want 11", len(bundle.Contract.Requirements))
	}
	// Requirement order must match the canonical contract file.
	wantFirst := "starts-requested-program"
	if bundle.Contract.Requirements[0].ID != wantFirst {
		t.Errorf("first requirement = %q, want %q", bundle.Contract.Requirements[0].ID, wantFirst)
	}
	if len(bundle.Specimens) != 2 {
		t.Fatalf("specimens = %d, want 2", len(bundle.Specimens))
	}
	if len(bundle.Evidence) != 22 {
		t.Fatalf("evidence = %d, want 22", len(bundle.Evidence))
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/specimens.yaml"] = strings.Replace(specimensYAML, "name: Specimen A", "name: Specimen A\n    colour: red", 1)
	})

	err := mustFail(t, root, manifest, "unknown YAML field")
	if !strings.Contains(err.Error(), "field colour not found") {
		t.Errorf("error = %v, want an unknown-field failure", err)
	}
}

func TestLoadRejectsUnsupportedSchemaVersion(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/manifest.yaml"] = strings.Replace(manifestYAML, "schema_version: 1", "schema_version: 99", 1)
	})

	if err := mustFail(t, root, manifest, "unsupported schema"); !errors.Is(err, ErrUnsupportedSchema) {
		t.Errorf("error = %v, want %v", err, ErrUnsupportedSchema)
	}
}

func TestLoadRejectsPathEscape(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/manifest.yaml"] = strings.Replace(manifestYAML, "primitive_file: catalogue/primitive.yaml", "primitive_file: ../escape.yaml", 1)
	})

	if err := mustFail(t, root, manifest, "path escape"); !errors.Is(err, ErrPathEscape) {
		t.Errorf("error = %v, want %v", err, ErrPathEscape)
	}
}

func TestLoadRejectsAbsolutePath(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/manifest.yaml"] = strings.Replace(manifestYAML, "primitive_file: catalogue/primitive.yaml", "primitive_file: /etc/passwd", 1)
	})

	if err := mustFail(t, root, manifest, "absolute path"); !errors.Is(err, ErrPathEscape) {
		t.Errorf("error = %v, want %v", err, ErrPathEscape)
	}
}

func TestLoadRejectsPrimitiveContractMismatch(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/primitive.yaml"] = strings.Replace(primitiveYAML, "contract_id: test/primitive/v1", "contract_id: test/other/v1", 1)
	})

	err := mustFail(t, root, manifest, "primitive/contract mismatch")
	if !strings.Contains(err.Error(), "declares contract") {
		t.Errorf("error = %v, want a contract mismatch", err)
	}
}

func TestLoadRejectsContractPrimitiveMismatch(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/contract.yaml"] = strings.Replace(contractYAML, "primitive_id: test/primitive", "primitive_id: test/other", 1)
	})

	err := mustFail(t, root, manifest, "contract/primitive mismatch")
	if !strings.Contains(err.Error(), "declares primitive") {
		t.Errorf("error = %v, want a primitive mismatch", err)
	}
}

func TestLoadRejectsDuplicateSpecimenID(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/specimens.yaml"] = strings.Replace(specimensYAML, "id: specimen-b", "id: specimen-a", 1)
	})

	err := mustFail(t, root, manifest, "duplicate specimen ID")
	if !strings.Contains(err.Error(), "duplicate specimen ID") {
		t.Errorf("error = %v, want a duplicate specimen ID", err)
	}
}

func TestLoadRejectsInvalidReuseMode(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/specimens.yaml"] = strings.Replace(specimensYAML, "- adapt", "- borrow", 1)
	})

	err := mustFail(t, root, manifest, "invalid reuse mode")
	if !strings.Contains(err.Error(), "unsupported reuse mode") {
		t.Errorf("error = %v, want an unsupported reuse mode", err)
	}
}

func TestLoadRejectsDuplicateEvidenceID(t *testing.T) {
	entry := `  - id: ev-a
    subject_id: specimen-b
    kind: fixture
    claim: duplicate observation
    result: pass
    source:
      path: catalogue/evidence.yaml
    observed_at: "2026-01-01T00:00:00Z"
    applies_to: req-two
    methodology: fixture
`
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = evidenceYAML + entry
	})

	err := mustFail(t, root, manifest, "duplicate evidence ID")
	if !strings.Contains(err.Error(), "duplicate evidence ID") {
		t.Errorf("error = %v, want a duplicate evidence ID", err)
	}
}

func TestLoadRejectsInvalidEvidenceResult(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = strings.Replace(evidenceYAML, "result: pass", "result: probably", 1)
	})

	err := mustFail(t, root, manifest, "invalid evidence result")
	if !strings.Contains(err.Error(), "unsupported result") {
		t.Errorf("error = %v, want an unsupported result", err)
	}
}

func TestLoadRejectsUnknownEvidenceSubject(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = strings.Replace(evidenceYAML, "subject_id: specimen-a", "subject_id: specimen-ghost", 1)
	})

	err := mustFail(t, root, manifest, "unknown evidence subject")
	if !strings.Contains(err.Error(), "not a bundle specimen") {
		t.Errorf("error = %v, want an unknown subject", err)
	}
}

func TestLoadRejectsUnknownRequirement(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = strings.Replace(evidenceYAML, "applies_to: req-one", "applies_to: req-ghost", 1)
	})

	err := mustFail(t, root, manifest, "unknown requirement")
	if !strings.Contains(err.Error(), "unknown requirement") {
		t.Errorf("error = %v, want an unknown requirement", err)
	}
}

func TestLoadRejectsMalformedTimestamp(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = strings.Replace(evidenceYAML, `"2026-01-01T00:00:00Z"`, `"yesterday"`, 1)
	})

	err := mustFail(t, root, manifest, "malformed timestamp")
	if !strings.Contains(err.Error(), "invalid observed_at") {
		t.Errorf("error = %v, want a timestamp failure", err)
	}
}

func TestLoadRejectsMissingProvenance(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/evidence.yaml"] = strings.Replace(evidenceYAML, "    source:\n      path: catalogue/evidence.yaml\n", "", 1)
	})

	err := mustFail(t, root, manifest, "missing provenance")
	if !strings.Contains(err.Error(), "no source provenance") {
		t.Errorf("error = %v, want missing provenance", err)
	}
}

func TestLoadRejectsEmptyManifestPath(t *testing.T) {
	root, manifest := tempBundle(t, func(files map[string]string) {
		files["catalogue/manifest.yaml"] = strings.Replace(manifestYAML, "primitive_file: catalogue/primitive.yaml", "primitive_file: \"\"", 1)
	})

	err := mustFail(t, root, manifest, "empty manifest path")
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("error = %v, want an empty-path failure", err)
	}
}
