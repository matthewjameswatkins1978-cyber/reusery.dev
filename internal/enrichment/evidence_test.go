package enrichment

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

var observedAt = time.Date(2026, time.September, 1, 8, 30, 0, 0, time.UTC)

func sampleObservation() model.Evidence {
	return NewObservation(observationSpec())
}

func observationSpec() ObservationSpec {
	artifact, err := policy.EncodeFactArtifact("MIT")
	if err != nil {
		panic(err)
	}
	return ObservationSpec{
		ProviderID:  ProviderDepsDev,
		SubjectID:   "public/pkg.go.dev/example.test/pkg@v1.0.0",
		Kind:        KindSourceLicense,
		Claim:       `deps.dev returned licence expression "MIT" for Go module "example.test/pkg" version "v1.0.0"`,
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://api.deps.dev/v3/x", Revision: "v1.0.0", Path: "example.test/pkg"},
		ObservedAt:  observedAt,
		Methodology: MethodologyDepsDev,
		Artifact:    artifact,
	}
}

func TestEvidenceIDIsDeterministicAndObservationTimeSensitive(t *testing.T) {
	first := NewObservation(observationSpec())
	second := NewObservation(observationSpec())
	if first.ID != second.ID {
		t.Fatalf("ids differ for identical observations: %q vs %q", first.ID, second.ID)
	}
	if !strings.HasPrefix(first.ID, "enrichment/"+ProviderDepsDev+"/") {
		t.Errorf("id = %q, want enrichment/<provider>/<hex>", first.ID)
	}
	if len(first.ID) != len("enrichment/"+ProviderDepsDev+"/")+64 {
		t.Errorf("id = %q, want a full sha256 digest", first.ID)
	}

	laterSpec := observationSpec()
	laterSpec.ObservedAt = observedAt.Add(time.Minute)
	later := NewObservation(laterSpec)
	if later.ID == first.ID {
		t.Error("a new observation time must produce a new evidence record")
	}

	mutatedSpec := observationSpec()
	mutatedSpec.Claim = "something else"
	mutated := NewObservation(mutatedSpec)
	if mutated.ID == first.ID {
		t.Error("different content must not collide on the same identity")
	}
}

func TestValidateObservationEnforcesTheTrustRules(t *testing.T) {
	base := sampleObservation()

	cases := map[string]func(*model.Evidence){
		"pass result":    func(e *model.Evidence) { e.Result = model.EvidencePass },
		"fail result":    func(e *model.Evidence) { e.Result = model.EvidenceFail },
		"applies_to set": func(e *model.Evidence) { e.AppliesTo = "bounds-stdout" },
		"no subject":     func(e *model.Evidence) { e.SubjectID = "other" },
		"no source":      func(e *model.Evidence) { e.Source = model.SourceRef{} },
		"zero observed":  func(e *model.Evidence) { e.ObservedAt = time.Time{} },
		"no id":          func(e *model.Evidence) { e.ID = "" },
		"wrong id":       func(e *model.Evidence) { e.ID = "enrichment/" + ProviderDepsDev + "/" + strings.Repeat("0", 64) },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			evidence := base
			mutate(&evidence)
			if err := ValidateObservation(ProviderDepsDev, base.SubjectID, evidence); !errors.Is(err, ErrUnsafeOutput) {
				t.Errorf("err = %v, want ErrUnsafeOutput", err)
			}
		})
	}

	if err := ValidateObservation(ProviderDepsDev, base.SubjectID, base); err != nil {
		t.Errorf("a valid observation was rejected: %v", err)
	}
	if err := ValidateObservation("", base.SubjectID, base); !errors.Is(err, ErrUnsafeOutput) {
		t.Errorf("err = %v, want ErrUnsafeOutput for a missing provider", err)
	}
}

func TestObservationAppliesToIsAlwaysEmpty(t *testing.T) {
	// The ObservationSpec type deliberately has no AppliesTo field, so a
	// provider cannot even express one. Assert the produced value.
	evidence := sampleObservation()
	if evidence.AppliesTo != "" {
		t.Errorf("applies_to = %q, want empty", evidence.AppliesTo)
	}
	if strings.Contains(string(evidence.Methodology), "pass") {
		t.Errorf("methodology = %q", evidence.Methodology)
	}
}

func TestObservationCarriesAVersionedFactArtifact(t *testing.T) {
	evidence := sampleObservation()
	value, ok := factArtifactValue(evidence.Artifact)
	if !ok || value != "MIT" {
		t.Errorf("artifact = %q, want a readable MIT value", evidence.Artifact)
	}
	if !strings.HasPrefix(evidence.Artifact, `{"schema_version":1,`) {
		t.Errorf("artifact = %q, want the versioned envelope", evidence.Artifact)
	}
}

// factArtifactValue reads back the value an enrichment observation recorded,
// using the same envelope policy decodes.
func factArtifactValue(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	var artifact struct {
		SchemaVersion int    `json:"schema_version"`
		Value         string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &artifact); err != nil {
		return "", false
	}
	if artifact.SchemaVersion != policy.FactArtifactSchemaVersion {
		return "", false
	}
	return artifact.Value, true
}

func TestKindsAreSharedWithThePolicyVocabulary(t *testing.T) {
	// The producer aliases the consumer's constants, so they cannot drift.
	pairs := map[string]string{
		"KindSourceLicense":           KindSourceLicense,
		"KindKnownAdvisory":           KindKnownAdvisory,
		"KindKnownAdvisoryCount":      KindKnownAdvisoryCount,
		"KindDirectDependencyCount":   KindDirectDependencyCount,
		"KindIndirectDependencyCount": KindIndirectDependencyCount,
		"KindRepositoryArchived":      KindRepositoryArchived,
		"KindRepositoryPushedAt":      KindRepositoryPushedAt,
		"KindPackagePublishedAt":      KindPackagePublishedAt,
		"KindPackageDeprecated":       KindPackageDeprecated,
		"KindPackageVersion":          KindPackageVersion,
	}
	want := map[string]string{
		"KindSourceLicense":           policy.KindSourceLicense,
		"KindKnownAdvisory":           policy.KindKnownAdvisory,
		"KindKnownAdvisoryCount":      policy.KindKnownAdvisoryCount,
		"KindDirectDependencyCount":   policy.KindDirectDependencyCount,
		"KindIndirectDependencyCount": policy.KindIndirectDependencyCount,
		"KindRepositoryArchived":      policy.KindRepositoryArchived,
		"KindRepositoryPushedAt":      policy.KindRepositoryPushedAt,
		"KindPackagePublishedAt":      policy.KindPackagePublishedAt,
		"KindPackageDeprecated":       policy.KindPackageDeprecated,
		"KindPackageVersion":          policy.KindPackageVersion,
	}
	for name, got := range pairs {
		if got != want[name] {
			t.Errorf("%s = %q, policy has %q", name, got, want[name])
		}
	}
}
