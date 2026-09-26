package policy

import (
	"os"
	"reflect"
	"testing"
)

// repoRoot and baselineRel locate the authored profile from this package's
// test working directory. The separator is a slash because the policy loader
// deliberately rejects Windows path separators in relative inputs.
const (
	repoRoot    = "../.."
	baselineRel = "policies/public-go-baseline-v1.yaml"
)

// TestPublicGoBaselineMatchesAuthoredYAML is the drift gate between the
// code-owned built-in baseline and the authored human-readable profile.
//
// The YAML stays the source of record for humans; the built-in keeps an
// installed binary working outside a repository checkout. Either changing
// without the other fails here.
func TestPublicGoBaselineMatchesAuthoredYAML(t *testing.T) {
	loaded, err := Load(repoRoot, baselineRel)
	if err != nil {
		t.Fatalf("load authored baseline: %v", err)
	}

	builtin := PublicGoBaseline()

	if err := builtin.Validate(); err != nil {
		t.Fatalf("built-in baseline does not validate: %v", err)
	}
	if !reflect.DeepEqual(builtin, loaded) {
		t.Errorf("built-in baseline drifted from %s\nbuilt-in: %+v\nauthored: %+v",
			baselineRel, builtin, loaded)
	}
}

// TestPublicGoBaselineIsStable pins the identifiers an agent-visible contract
// depends on: renaming the profile id would silently change every Resolution
// written with it.
func TestPublicGoBaselineIsStable(t *testing.T) {
	builtin := PublicGoBaseline()
	if builtin.ID != "public-go-baseline/v1" {
		t.Errorf("id = %q, want public-go-baseline/v1", builtin.ID)
	}
	if builtin.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", builtin.SchemaVersion, SchemaVersion)
	}
	if builtin.Selection.MaxOptions != 3 {
		t.Errorf("max_options = %d, want 3", builtin.Selection.MaxOptions)
	}
	// Omitted numeric thresholds mean NO threshold: no default day count and
	// no default dependency ceiling may ever appear here.
	if builtin.Dependencies.MaxDirect != nil {
		t.Errorf("max_direct = %v, want nil (no quantity rule)", *builtin.Dependencies.MaxDirect)
	}
	if builtin.Maintenance.MaxDaysSincePush != nil || builtin.Maintenance.MaxDaysSinceRelease != nil {
		t.Errorf("maintenance thresholds = %v/%v, want nil",
			builtin.Maintenance.MaxDaysSincePush, builtin.Maintenance.MaxDaysSinceRelease)
	}
}

// TestPublicGoBaselineFileIsPresent keeps the authored profile in the tree so
// the parity test above can never be skipped by a missing file.
func TestPublicGoBaselineFileIsPresent(t *testing.T) {
	if _, err := os.Stat(repoRoot + "/" + baselineRel); err != nil {
		t.Fatalf("authored baseline missing: %v", err)
	}
}
