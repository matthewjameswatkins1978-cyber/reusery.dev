package intent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Identifier scheme for a generated, ephemeral normalised intent.
//
// The model never supplies an identifier. Application code derives one
// deterministically from the normalised input, the validated structured draft,
// the prompt version and the schema version, so the same request normalises to
// the same identity and a changed claim changes the identity. No random UUIDs:
// reproducibility is the point.
const (
	// IDPrefix namespaces every generated identity.
	IDPrefix = "intent/"
	// ContractVersion is the version of every generated contract.
	ContractVersion = "1"
)

// PrimitiveID returns the generated primitive identity for a digest.
func PrimitiveID(digest string) string { return IDPrefix + digest }

// ContractID returns the generated contract identity for a digest.
func ContractID(digest string) string { return PrimitiveID(digest) + "/v1" }

// RequirementID returns the nth application-assigned requirement identifier in
// model output order: req-001, req-002, ...
func RequirementID(index int) string { return fmt.Sprintf("req-%03d", index+1) }

// IdentityDigest returns the SHA-256 hex digest that names one normalisation.
//
// The digest covers the normalised raw input, the validated structured draft,
// the prompt version and the schema version. It never covers an API secret.
func IdentityDigest(input string, draft Draft) string {
	hash := sha256.New()

	writeField(hash, "input", input)
	writeField(hash, "prompt_version", PromptVersion)
	writeField(hash, "schema_version", fmt.Sprintf("%d", SchemaVersion))

	writeField(hash, "status", string(draft.Status))
	writeField(hash, "capability", draft.Capability)
	writeField(hash, "summary", draft.Summary)
	writeField(hash, "requested_artifact_level", string(draft.RequestedArtifactLevel))

	for i, requirement := range draft.Requirements {
		prefix := fmt.Sprintf("requirement.%d.", i)
		writeField(hash, prefix+"description", requirement.Description)
		writeField(hash, prefix+"kind", requirement.Kind)
		writeField(hash, prefix+"required", fmt.Sprintf("%t", requirement.Required))
	}
	for i, constraint := range draft.Constraints {
		prefix := fmt.Sprintf("constraint.%d.", i)
		writeField(hash, prefix+"kind", constraint.Kind)
		writeField(hash, prefix+"description", constraint.Description)
		writeField(hash, prefix+"required", fmt.Sprintf("%t", constraint.Required))
	}
	for i, ambiguity := range draft.Ambiguities {
		prefix := fmt.Sprintf("ambiguity.%d.", i)
		writeField(hash, prefix+"question", ambiguity.Question)
		writeField(hash, prefix+"why_it_matters", ambiguity.WhyItMatters)
	}
	for i, assumption := range draft.Assumptions {
		writeField(hash, fmt.Sprintf("assumption.%d.", i), assumption)
	}
	writeField(hash, "unsupported_reason", draft.UnsupportedReason)

	return hex.EncodeToString(hash.Sum(nil))
}

// writeField appends one length-delimited field. Keys are ours and never
// contain a NUL byte, so key, separator and length make the encoding
// unambiguous regardless of the values.
func writeField(writer io.Writer, key, value string) {
	// A hash writer never fails; ignoring the error keeps this helper total.
	_, _ = io.WriteString(writer, key)
	_, _ = writer.Write([]byte{0})
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = io.WriteString(writer, value)
}

// Build maps a validated draft onto the provisional domain objects a caller
// can inspect. It is called only after Validate has returned no failures.
//
// Primitive and Contract are generated only when the draft carries
// requirements. An unsupported result, or a clarification result with nothing
// clear enough to state yet, has no contract to generate — and certainly no
// evidence, no candidate and no resolver outcome, none of which this function
// can produce.
func Build(input string, draft Draft, metadata GenerationMetadata) Result {
	result := Result{
		Input:                  input,
		Status:                 draft.Status,
		RequestedArtifactLevel: draft.RequestedArtifactLevel,
		Capability:             draft.Capability,
		Summary:                draft.Summary,
		Constraints:            append([]Constraint{}, draft.Constraints...),
		Ambiguities:            append([]Ambiguity{}, draft.Ambiguities...),
		Assumptions:            append([]string{}, draft.Assumptions...),
		Metadata:               metadata,
	}
	if draft.Status == StatusUnsupported {
		result.UnsupportedReason = draft.UnsupportedReason
	}
	if len(draft.Requirements) == 0 {
		return result
	}

	digest := IdentityDigest(input, draft)
	primitiveID := PrimitiveID(digest)
	contractID := ContractID(digest)

	requirements := make([]model.Requirement, 0, len(draft.Requirements))
	for i, requirement := range draft.Requirements {
		requirements = append(requirements, model.Requirement{
			ID:          RequirementID(i),
			Description: requirement.Description,
			Kind:        requirement.Kind,
			Required:    requirement.Required,
		})
	}

	primitive := model.Primitive{
		ID:          primitiveID,
		Name:        draft.Capability,
		Description: draft.Summary,
		ContractID:  contractID,
	}
	contract := model.Contract{
		ID:           contractID,
		PrimitiveID:  primitiveID,
		Version:      ContractVersion,
		Summary:      draft.Summary,
		Requirements: requirements,
	}
	result.Primitive = &primitive
	result.Contract = &contract
	return result
}
