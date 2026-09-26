package enrichment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// Methodology values identify how an observation was obtained. They are part
// of the provenance contract and are stable strings, not free text.
const (
	MethodologyDepsDev        = "deps.dev v3 API"
	MethodologyGitHubMetadata = "GitHub REST repository metadata"
)

// Evidence kinds this packet produces. They alias the policy package's
// vocabulary so a producer and its consumer can never drift apart: fact
// extraction only recognises kinds the enrichment side actually writes.
const (
	KindPackageVersion          = policy.KindPackageVersion
	KindPackagePublishedAt      = policy.KindPackagePublishedAt
	KindPackageDeprecated       = policy.KindPackageDeprecated
	KindPackageDeprecatedReason = policy.KindPackageDeprecatedReason
	KindSourceLicense           = policy.KindSourceLicense
	KindLicenceRelationship     = policy.KindLicenceRelationship
	KindKnownAdvisory           = policy.KindKnownAdvisory
	KindKnownAdvisoryCount      = policy.KindKnownAdvisoryCount
	KindDirectDependency        = policy.KindDirectDependency
	KindDirectDependencyCount   = policy.KindDirectDependencyCount
	KindIndirectDependency      = policy.KindIndirectDependency
	KindIndirectDependencyCount = policy.KindIndirectDependencyCount
	KindSourceRepositoryLink    = "source_repository_link"
	KindRepositoryArchived      = policy.KindRepositoryArchived
	KindRepositoryPushedAt      = policy.KindRepositoryPushedAt
	KindRepositoryDefaultBranch = "repository_default_branch"
)

// ObservationSpec describes one provider observation. There is deliberately no
// AppliesTo field: enrichment evidence cannot be aimed at a behavioural
// contract requirement, because enrichment is not verification.
type ObservationSpec struct {
	ProviderID  string
	SubjectID   string
	Kind        string
	Claim       string
	Result      model.EvidenceResult
	Source      model.SourceRef
	ObservedAt  time.Time
	Methodology string
	Artifact    string
}

// NewObservation builds attributable enrichment evidence with a deterministic
// ID. AppliesTo is always left empty.
func NewObservation(spec ObservationSpec) model.Evidence {
	evidence := model.Evidence{
		SubjectID:   spec.SubjectID,
		Kind:        spec.Kind,
		Claim:       spec.Claim,
		Result:      spec.Result,
		Source:      spec.Source,
		ObservedAt:  spec.ObservedAt.UTC(),
		Methodology: spec.Methodology,
		Artifact:    spec.Artifact,
	}
	evidence.ID = EvidenceID(spec.ProviderID, evidence)
	return evidence
}

// EvidenceID returns the deterministic identity of one enrichment observation:
// "enrichment/<provider>/<sha256-hex>" over a length-prefixed canonical form of
// the provider, subject, kind, claim, result, source reference, applies-to,
// methodology, artifact and UTC observation time.
//
// Because ObservedAt participates, a later real run legitimately records a new
// observation rather than silently rewriting history, while repeating a run
// inside the same observation instant is idempotent. Random identifiers are
// never used.
func EvidenceID(providerID string, evidence model.Evidence) string {
	sum := sha256.New()
	writeDigestField(sum, providerID)
	writeDigestField(sum, evidence.SubjectID)
	writeDigestField(sum, evidence.Kind)
	writeDigestField(sum, evidence.Claim)
	writeDigestField(sum, string(evidence.Result))
	writeDigestField(sum, evidence.Source.URL)
	writeDigestField(sum, evidence.Source.Revision)
	writeDigestField(sum, evidence.Source.Path)
	writeDigestField(sum, evidence.Source.License)
	writeDigestField(sum, evidence.AppliesTo)
	writeDigestField(sum, evidence.Methodology)
	writeDigestField(sum, evidence.Artifact)
	writeDigestField(sum, evidence.ObservedAt.UTC().Format(time.RFC3339Nano))
	return "enrichment/" + providerID + "/" + hex.EncodeToString(sum.Sum(nil))
}

// writeDigestField writes one field as "<len>:<value>". Length prefixes make
// the encoding unambiguous, so no separator needs escaping.
func writeDigestField(w io.Writer, value string) {
	_, _ = fmt.Fprintf(w, "%d:%s", len(value), value)
}

// ValidateObservation is the defensive pass every provider-produced
// observation survives before persistence. A buggy provider cannot inject
// behavioural PASS or FAIL evidence, aim evidence at a contract requirement,
// or ship an unattributable observation.
func ValidateObservation(providerID, specimenID string, evidence model.Evidence) error {
	if providerID == "" {
		return fmt.Errorf("%w: observation has no provider id", ErrUnsafeOutput)
	}
	if evidence.ID == "" {
		return fmt.Errorf("%w: specimen %q has an observation with no id", ErrUnsafeOutput, specimenID)
	}
	if evidence.SubjectID != specimenID {
		return fmt.Errorf("%w: observation %q has subject %q, want %q",
			ErrUnsafeOutput, evidence.ID, evidence.SubjectID, specimenID)
	}
	switch evidence.Result {
	case model.EvidenceInfo, model.EvidenceUnknown:
	default:
		return fmt.Errorf("%w: observation %q has result %q; enrichment evidence may only be info or unknown",
			ErrUnsafeOutput, evidence.ID, evidence.Result)
	}
	if evidence.AppliesTo != "" {
		return fmt.Errorf("%w: observation %q applies to requirement %q; enrichment evidence never maps to a contract requirement",
			ErrUnsafeOutput, evidence.ID, evidence.AppliesTo)
	}
	if evidence.Source.URL == "" && evidence.Source.Path == "" {
		return fmt.Errorf("%w: observation %q has no source provenance", ErrUnsafeOutput, evidence.ID)
	}
	if evidence.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observation %q has no observed_at", ErrUnsafeOutput, evidence.ID)
	}
	if want := EvidenceID(providerID, evidence); evidence.ID != want {
		return fmt.Errorf("%w: observation id %q is not the deterministic id %q",
			ErrUnsafeOutput, evidence.ID, want)
	}
	return nil
}
