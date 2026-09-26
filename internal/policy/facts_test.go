package policy

import (
	"fmt"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var factsSubject = "public/pkg.go.dev/example.test/pkg@v1.0.0"

var factsObservedAt = time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

func factsSpecimen() model.Specimen {
	return model.Specimen{
		ID:          factsSubject,
		PrimitiveID: "process/bounded-subprocess",
		Name:        "example.test/pkg",
		Source:      model.SourceRef{URL: "https://pkg.go.dev/example.test/pkg", Revision: "v1.0.0", Path: "example.test/pkg"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
}

// fact builds one evidence item carrying a canonical fact artifact. A nil value
// produces an observation with no machine value, which is how UNKNOWN
// observations are recorded.
func fact(id, kind string, result model.EvidenceResult, value any) model.Evidence {
	artifact := ""
	if value != nil {
		encoded, err := EncodeFactArtifact(value)
		if err != nil {
			panic(err)
		}
		artifact = encoded
	}
	return model.Evidence{
		ID:         "ev/" + id,
		SubjectID:  factsSubject,
		Kind:       kind,
		Claim:      "human readable claim for " + kind,
		Result:     result,
		Source:     model.SourceRef{URL: "https://api.example.test/v3", Revision: "v1.0.0", Path: "example.test/pkg"},
		ObservedAt: factsObservedAt,
		Artifact:   artifact,
	}
}

func TestEncodeFactArtifactIsDeterministicAndVersioned(t *testing.T) {
	first, err := EncodeFactArtifact("MIT")
	if err != nil {
		t.Fatalf("EncodeFactArtifact: %v", err)
	}
	second, err := EncodeFactArtifact("MIT")
	if err != nil {
		t.Fatalf("EncodeFactArtifact: %v", err)
	}
	if first != second {
		t.Errorf("encoding is not deterministic: %q vs %q", first, second)
	}
	if first != `{"schema_version":1,"value":"MIT"}` {
		t.Errorf("encoding = %q", first)
	}

	count, err := EncodeFactArtifact(3)
	if err != nil {
		t.Fatalf("EncodeFactArtifact: %v", err)
	}
	if count != `{"schema_version":1,"value":3}` {
		t.Errorf("count encoding = %q", count)
	}
	flag, err := EncodeFactArtifact(false)
	if err != nil {
		t.Fatalf("EncodeFactArtifact: %v", err)
	}
	if flag != `{"schema_version":1,"value":false}` {
		t.Errorf("bool encoding = %q", flag)
	}
}

func TestSingleKnownLicence(t *testing.T) {
	evidence := []model.Evidence{fact("lic", KindSourceLicense, model.EvidenceInfo, "MIT")}
	got := ExtractFacts(factsSpecimen(), evidence).Licence

	if got.Status != FactKnown {
		t.Errorf("status = %q, want known", got.Status)
	}
	if len(got.Values) != 1 || got.Values[0] != "MIT" {
		t.Errorf("values = %v", got.Values)
	}
	if len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != "ev/lic" {
		t.Errorf("evidence ids = %v", got.EvidenceIDs)
	}
}

func TestUnknownLicence(t *testing.T) {
	got := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("lic", KindSourceLicense, model.EvidenceUnknown, nil),
	}).Licence
	if got.Status != FactUnknown {
		t.Errorf("status = %q, want unknown", got.Status)
	}
	if len(got.Values) != 0 {
		t.Errorf("values = %v, want none", got.Values)
	}
	if len(got.EvidenceIDs) != 1 {
		t.Errorf("evidence ids = %v, want the unknown observation preserved", got.EvidenceIDs)
	}
}

func TestNoLicenceEvidenceAtAllIsUnknown(t *testing.T) {
	if got := ExtractFacts(factsSpecimen(), nil).Licence; got.Status != FactUnknown {
		t.Errorf("status = %q, want unknown", got.Status)
	}
}

func TestMultipleLicencesFromOneProviderIsMultipleNotConflicting(t *testing.T) {
	evidence := []model.Evidence{
		fact("lic-a", KindSourceLicense, model.EvidenceInfo, "MIT"),
		fact("lic-b", KindSourceLicense, model.EvidenceInfo, "Apache-2.0"),
		fact("rel", KindLicenceRelationship, model.EvidenceUnknown, nil),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Licence

	if got.Status != FactMultiple {
		t.Errorf("status = %q, want multiple", got.Status)
	}
	if !got.RelationshipUnknown {
		t.Error("relationship_unknown = false, want true")
	}
	if len(got.Values) != 2 {
		t.Errorf("values = %v", got.Values)
	}
	if len(got.EvidenceIDs) != 3 {
		t.Errorf("evidence ids = %v, want 3", got.EvidenceIDs)
	}
}

func TestConflictingLicenceProviders(t *testing.T) {
	evidence := []model.Evidence{
		fact("depsdev-lic", KindSourceLicense, model.EvidenceInfo, "MIT"),
		fact("github-lic", KindSourceLicense, model.EvidenceInfo, "Apache-2.0"),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Licence

	if got.Status != FactConflicting {
		t.Errorf("status = %q, want conflicting", got.Status)
	}
	if got.RelationshipUnknown {
		t.Error("relationship_unknown must stay false without an explicit relationship observation")
	}
	if len(got.Values) != 2 {
		t.Errorf("values = %v", got.Values)
	}
}

func TestLicenceFallsBackToTheSpecimenSourceForDiscoveryEvidence(t *testing.T) {
	specimen := factsSpecimen()
	specimen.Source.License = "BSD-3-Clause"
	evidence := []model.Evidence{{
		ID:         "ev/discovery-lic",
		SubjectID:  specimen.ID,
		Kind:       KindSourceLicense,
		Claim:      "pkg.go.dev reports licence \"BSD-3-Clause\"",
		Result:     model.EvidenceInfo,
		Source:     specimen.Source,
		ObservedAt: factsObservedAt,
		Artifact:   "query=subprocess",
	}}
	got := ExtractFacts(specimen, evidence).Licence
	if got.Status != FactKnown || len(got.Values) != 1 || got.Values[0] != "BSD-3-Clause" {
		t.Errorf("licence = %+v", got)
	}
}

func TestAdvisoryCountZeroIsKnownAndNotAReview(t *testing.T) {
	evidence := []model.Evidence{fact("count", KindKnownAdvisoryCount, model.EvidenceInfo, 0)}
	got := ExtractFacts(factsSpecimen(), evidence).Advisory

	if got.Status != FactKnown {
		t.Errorf("status = %q, want known: a reported zero is a fact", got.Status)
	}
	if !got.CountKnown || got.Count != 0 {
		t.Errorf("count=%d known=%t, want 0/true", got.Count, got.CountKnown)
	}
	if len(got.KnownIDs) != 0 {
		t.Errorf("known ids = %v, want none", got.KnownIDs)
	}
}

func TestAdvisoryIDsPresent(t *testing.T) {
	evidence := []model.Evidence{
		fact("adv-1", KindKnownAdvisory, model.EvidenceInfo, "GHSA-w73w-5m7g-f7qc"),
		fact("adv-2", KindKnownAdvisory, model.EvidenceInfo, "GO-2020-0017"),
		fact("count", KindKnownAdvisoryCount, model.EvidenceInfo, 2),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Advisory

	if got.Status != FactKnown {
		t.Errorf("status = %q", got.Status)
	}
	if got.Count != 2 || !got.CountKnown {
		t.Errorf("count=%d known=%t", got.Count, got.CountKnown)
	}
	if len(got.KnownIDs) != 2 || got.KnownIDs[0] != "GHSA-w73w-5m7g-f7qc" {
		t.Errorf("ids = %v", got.KnownIDs)
	}
}

func TestAdvisoryStaleZeroNeverErasesALaterObservation(t *testing.T) {
	evidence := []model.Evidence{
		fact("old-count", KindKnownAdvisoryCount, model.EvidenceInfo, 0),
		fact("adv", KindKnownAdvisory, model.EvidenceInfo, "GHSA-xxxx-yyyy-zzzz"),
		fact("new-count", KindKnownAdvisoryCount, model.EvidenceInfo, 1),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Advisory
	if got.Count != 1 || len(got.KnownIDs) != 1 {
		t.Errorf("count=%d ids=%v, want the union", got.Count, got.KnownIDs)
	}
}

func TestDependencyCounts(t *testing.T) {
	evidence := []model.Evidence{
		fact("direct", KindDirectDependency, model.EvidenceInfo, "github.com/pkg/errors@v0.9.1"),
		fact("direct-count", KindDirectDependencyCount, model.EvidenceInfo, 1),
		fact("indirect-count", KindIndirectDependencyCount, model.EvidenceInfo, 3),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Dependency

	if got.Status != FactKnown {
		t.Errorf("status = %q", got.Status)
	}
	if !got.DirectCountKnown || got.DirectCount != 1 {
		t.Errorf("direct=%d known=%t", got.DirectCount, got.DirectCountKnown)
	}
	if !got.IndirectCountKnown || got.IndirectCount != 3 {
		t.Errorf("indirect=%d known=%t", got.IndirectCount, got.IndirectCountKnown)
	}
	if len(got.Direct) != 1 {
		t.Errorf("direct = %v", got.Direct)
	}
}

func TestDependencyCountsDisagreeOnTheConservativeValue(t *testing.T) {
	evidence := []model.Evidence{
		fact("count-a", KindDirectDependencyCount, model.EvidenceInfo, 3),
		fact("count-b", KindDirectDependencyCount, model.EvidenceInfo, 12),
	}
	got := ExtractFacts(factsSpecimen(), evidence).Dependency
	if got.DirectCount != 12 {
		t.Errorf("direct count = %d, want the larger (12) so a maximum cannot be cleared", got.DirectCount)
	}
}

func TestArchivedTrueFalseAndConflict(t *testing.T) {
	archived := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("arch", KindRepositoryArchived, model.EvidenceInfo, true),
	}).Archived
	if archived.Status != FactKnown || !archived.Known || !archived.Archived {
		t.Errorf("archived = %+v", archived)
	}

	active := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("arch", KindRepositoryArchived, model.EvidenceInfo, false),
	}).Archived
	if active.Status != FactKnown || !active.Known || active.Archived {
		t.Errorf("active = %+v", active)
	}

	conflicting := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("arch-a", KindRepositoryArchived, model.EvidenceInfo, false),
		fact("arch-b", KindRepositoryArchived, model.EvidenceInfo, true),
	}).Archived
	if conflicting.Status != FactConflicting {
		t.Errorf("status = %q, want conflicting", conflicting.Status)
	}
	if conflicting.Known {
		t.Error("a conflict must not be reported as known")
	}
	if !conflicting.Archived {
		t.Error("a conflict must report the risk-bearing state")
	}
}

func TestPushedAtExtractionAndDisagreementUsesTheOldestObservation(t *testing.T) {
	single := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("push", KindRepositoryPushedAt, model.EvidenceInfo, "2026-01-02T03:04:05Z"),
	}).LastPush
	if single.Status != FactKnown || !single.Known {
		t.Fatalf("last push = %+v", single)
	}
	if !single.PushedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("pushed at = %v", single.PushedAt)
	}

	conflicting := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("push-old", KindRepositoryPushedAt, model.EvidenceInfo, "2020-01-01T00:00:00Z"),
		fact("push-new", KindRepositoryPushedAt, model.EvidenceInfo, "2026-06-01T00:00:00Z"),
	}).LastPush
	if conflicting.Status != FactConflicting {
		t.Errorf("status = %q, want conflicting", conflicting.Status)
	}
	if conflicting.PushedAt.Year() != 2020 {
		t.Errorf("pushed at = %v, want the oldest observation, never latest-wins", conflicting.PushedAt)
	}
}

func TestDeprecatedTrueFalse(t *testing.T) {
	deprecated := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("dep", KindPackageDeprecated, model.EvidenceInfo, true),
		fact("reason", KindPackageDeprecatedReason, model.EvidenceInfo, "superseded"),
	}).Deprecated
	if deprecated.Status != FactKnown || !deprecated.Deprecated || deprecated.Reason != "superseded" {
		t.Errorf("deprecated = %+v", deprecated)
	}

	current := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("dep", KindPackageDeprecated, model.EvidenceInfo, false),
	}).Deprecated
	if current.Status != FactKnown || current.Deprecated {
		t.Errorf("current = %+v", current)
	}
}

func TestRevisionKnownUnknownAndConflicting(t *testing.T) {
	known := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("version", KindPackageVersion, model.EvidenceInfo, "v1.0.0"),
	}).Revision
	if known.Status != FactKnown || !known.Known || known.Revision != "v1.0.0" {
		t.Errorf("revision = %+v", known)
	}

	unpinned := factsSpecimen()
	unpinned.Source.Revision = ""
	unknown := ExtractFacts(unpinned, nil).Revision
	if unknown.Status != FactUnknown {
		t.Errorf("status = %q, want unknown for an unpinned repository reference", unknown.Status)
	}

	conflicting := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("blob-a", KindSourceRevision, model.EvidenceInfo, "aaaa"),
	}).Revision
	if conflicting.Status != FactKnown {
		t.Fatalf("revision = %+v", conflicting)
	}
	second := factsSpecimen()
	second.Source.Revision = "v1.0.1"
	mixed := ExtractFacts(second, []model.Evidence{
		fact("blob", KindSourceRevision, model.EvidenceInfo, "aaaa"),
	}).Revision
	if mixed.Status != FactConflicting {
		t.Errorf("status = %q, want conflicting", mixed.Status)
	}
}

func TestDiscoveryRelevance(t *testing.T) {
	matched := ExtractFacts(factsSpecimen(), []model.Evidence{
		fact("match", KindDiscoveryMatch, model.EvidenceInfo, nil),
	}).DiscoveryRelevance
	if matched.Status != FactKnown || !matched.Matched {
		t.Errorf("relevance = %+v", matched)
	}

	if got := ExtractFacts(factsSpecimen(), nil).DiscoveryRelevance; got.Matched {
		t.Errorf("relevance = %+v, want unmatched", got)
	}
}

func TestMalformedFactArtifactBecomesUnknownNeverGuessed(t *testing.T) {
	malformed := model.Evidence{
		ID:         "ev/malformed",
		SubjectID:  factsSubject,
		Kind:       KindSourceLicense,
		Claim:      "license MIT",
		Result:     model.EvidenceInfo,
		Source:     model.SourceRef{URL: "https://api.example.test"},
		ObservedAt: factsObservedAt,
		Artifact:   `{"schema_version":1,"value":`,
	}
	got := ExtractFacts(factsSpecimen(), []model.Evidence{malformed}).Licence
	if got.Status != FactUnknown {
		t.Errorf("status = %q, want unknown for a malformed artifact", got.Status)
	}
	if len(got.Values) != 0 {
		t.Errorf("values = %v, want nothing guessed", got.Values)
	}

	wrongSchema := model.Evidence{
		ID:         "ev/schema",
		SubjectID:  factsSubject,
		Kind:       KindSourceLicense,
		Claim:      "license MIT",
		Result:     model.EvidenceInfo,
		Source:     model.SourceRef{URL: "https://api.example.test"},
		ObservedAt: factsObservedAt,
		Artifact:   `{"schema_version":99,"value":"MIT"}`,
	}
	if got := ExtractFacts(factsSpecimen(), []model.Evidence{wrongSchema}).Licence; got.Status != FactUnknown {
		t.Errorf("status = %q, want unknown for an unsupported artifact schema", got.Status)
	}
}

func TestEvidenceFromAnotherSubjectIsIgnored(t *testing.T) {
	other := fact("lic", KindSourceLicense, model.EvidenceInfo, "MIT")
	other.SubjectID = "public/pkg.go.dev/other.test/pkg@v1.0.0"
	got := ExtractFacts(factsSpecimen(), []model.Evidence{other}).Licence
	if got.Status != FactUnknown {
		t.Errorf("status = %q, want unknown", got.Status)
	}
}

func TestFactsCarryEveryEvidenceIDTheyConsumed(t *testing.T) {
	evidence := []model.Evidence{
		fact("lic", KindSourceLicense, model.EvidenceInfo, "MIT"),
		fact("count", KindKnownAdvisoryCount, model.EvidenceInfo, 0),
		fact("direct", KindDirectDependencyCount, model.EvidenceInfo, 2),
		fact("arch", KindRepositoryArchived, model.EvidenceInfo, false),
		fact("push", KindRepositoryPushedAt, model.EvidenceInfo, "2026-01-01T00:00:00Z"),
		fact("dep", KindPackageDeprecated, model.EvidenceInfo, false),
	}
	facts := ExtractFacts(factsSpecimen(), evidence)

	assertions := map[string][]string{
		"licence":    facts.Licence.EvidenceIDs,
		"advisory":   facts.Advisory.EvidenceIDs,
		"dependency": facts.Dependency.EvidenceIDs,
		"archived":   facts.Archived.EvidenceIDs,
		"last push":  facts.LastPush.EvidenceIDs,
		"deprecated": facts.Deprecated.EvidenceIDs,
		"revision":   facts.Revision.EvidenceIDs,
		"discovery":  facts.DiscoveryRelevance.EvidenceIDs,
	}
	for name, ids := range assertions {
		if name == "revision" || name == "discovery" {
			continue
		}
		if len(ids) != 1 {
			t.Errorf("%s evidence ids = %v, want exactly the contributing observation", name, ids)
		}
	}
}

func TestFactsIgnoreEvidenceForKindsTheyDoNotUnderstand(t *testing.T) {
	got := ExtractFacts(factsSpecimen(), []model.Evidence{{
		ID:         "ev/other",
		SubjectID:  factsSubject,
		Kind:       "development-fixture",
		Claim:      "fixture claim",
		Result:     model.EvidencePass,
		Source:     model.SourceRef{Path: "catalogue/dev/evidence.yaml"},
		ObservedAt: factsObservedAt,
		Artifact:   fmt.Sprintf("fixture/%d", 1),
	}})
	if got.Licence.Status != FactUnknown || got.Advisory.Status != FactUnknown {
		t.Errorf("facts were invented from an unrelated kind: %+v", got)
	}
}
