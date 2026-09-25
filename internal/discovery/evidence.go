package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// ErrUnsafeOutput reports provider-produced data that violates the Packet 5
// trust rules. It is checked before anything is persisted.
var ErrUnsafeOutput = errors.New("discovery: unsafe provider output")

// Methodology values identify how an observation was obtained. They are part
// of the provenance contract and are stable strings, not free text.
const (
	MethodologyPkgGoDev    = "pkg.go.dev v1 API discovery"
	MethodologyGitHubRepos = "GitHub REST repository search"
	MethodologyGitHubCode  = "GitHub REST code search"
)

// ObservationSpec describes one provider observation. There is deliberately no
// AppliesTo field: provider observations cannot be aimed at a behavioural
// contract requirement, because discovery is not verification.
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

// NewObservation builds attributable provider evidence with a deterministic
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

// EvidenceID returns the deterministic identity of one provider observation:
// "discovery/<provider>/<sha256-hex>" over a length-prefixed canonical form of
// the provider, subject, kind, claim, result, source reference, applies-to,
// methodology, artifact and UTC observation time.
//
// Because ObservedAt participates, a later real discovery run legitimately
// produces a new observation rather than silently rewriting history. Random
// identifiers are never used.
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
	return "discovery/" + providerID + "/" + hex.EncodeToString(sum.Sum(nil))
}

// writeDigestField writes one field as "<len>:<value>". Length prefixes make
// the encoding unambiguous, so no separator needs escaping.
func writeDigestField(w io.Writer, value string) {
	_, _ = fmt.Fprintf(w, "%d:%s", len(value), value)
}

// ValidateCandidate is the defensive pass every provider-produced candidate
// must survive before persistence. A buggy provider cannot inject behavioural
// PASS evidence, aim evidence at a contract requirement, or ship an
// unattributable observation.
func ValidateCandidate(candidate Candidate, primitiveID string) error {
	if candidate.ProviderID == "" {
		return fmt.Errorf("%w: candidate has no provider id", ErrUnsafeOutput)
	}
	specimen := candidate.Specimen
	if specimen.ID == "" {
		return fmt.Errorf("%w: candidate has no specimen id", ErrUnsafeOutput)
	}
	if specimen.PrimitiveID != primitiveID {
		return fmt.Errorf("%w: specimen %q targets primitive %q, want %q",
			ErrUnsafeOutput, specimen.ID, specimen.PrimitiveID, primitiveID)
	}
	if specimen.Source.URL == "" && specimen.Source.Path == "" {
		return fmt.Errorf("%w: specimen %q has no source provenance", ErrUnsafeOutput, specimen.ID)
	}
	for _, mode := range specimen.ReuseMode {
		if !supportedReuseMode(mode) {
			return fmt.Errorf("%w: specimen %q declares unsupported reuse mode %q",
				ErrUnsafeOutput, specimen.ID, mode)
		}
	}

	for _, evidence := range candidate.Evidence {
		if evidence.ID == "" {
			return fmt.Errorf("%w: specimen %q has evidence with no id", ErrUnsafeOutput, specimen.ID)
		}
		if evidence.SubjectID != specimen.ID {
			return fmt.Errorf("%w: evidence %q has subject %q, want %q",
				ErrUnsafeOutput, evidence.ID, evidence.SubjectID, specimen.ID)
		}
		switch evidence.Result {
		case model.EvidenceInfo, model.EvidenceUnknown:
		default:
			return fmt.Errorf("%w: evidence %q has result %q, provider evidence may only be info or unknown",
				ErrUnsafeOutput, evidence.ID, evidence.Result)
		}
		if evidence.AppliesTo != "" {
			return fmt.Errorf("%w: evidence %q applies to requirement %q; discovery evidence never maps to a contract requirement",
				ErrUnsafeOutput, evidence.ID, evidence.AppliesTo)
		}
		if evidence.Source.URL == "" && evidence.Source.Path == "" {
			return fmt.Errorf("%w: evidence %q has no source provenance", ErrUnsafeOutput, evidence.ID)
		}
		if evidence.ObservedAt.IsZero() {
			return fmt.Errorf("%w: evidence %q has no observed_at", ErrUnsafeOutput, evidence.ID)
		}
		if want := EvidenceID(candidate.ProviderID, evidence); evidence.ID != want {
			return fmt.Errorf("%w: evidence id %q is not the deterministic id %q",
				ErrUnsafeOutput, evidence.ID, want)
		}
	}
	return nil
}

func supportedReuseMode(mode model.ReuseMode) bool {
	switch mode {
	case model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference:
		return true
	default:
		return false
	}
}
