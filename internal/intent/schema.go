package intent

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed assets/normalized-intent-v1.schema.json
var normalizedIntentSchema string

// JSONSchema returns the strict structured-output schema the model must fill.
//
// The schema stays inside the subset that strict structured output supports:
// object, array, string, boolean, enum, properties, required and
// additionalProperties false. It deliberately carries no length or size
// keywords, because every bound is enforced by deterministic Go validation
// anyway — strict schema conformance is never trusted on its own.
//
// There is no field for evidence, observations, candidates, scores, ranking or
// resolver outcomes, so a hostile model response cannot express authority that
// Reusery would then have to defend against.
func JSONSchema() string { return normalizedIntentSchema }

// DecodeDraft decodes structured model output into a Draft.
//
// Decoding is deliberately non-strict about unknown fields: the schema governs
// what Reusery reads, and any field Reusery does not know is dropped rather
// than mapped. That is what keeps a malicious response from introducing
// prohibited authority — unknown keys cannot reach a Result.
func DecodeDraft(structured string) (Draft, error) {
	if structured == "" {
		return Draft{}, fmt.Errorf("%w: provider returned no structured output", ErrDecode)
	}
	var draft Draft
	if err := json.Unmarshal([]byte(structured), &draft); err != nil {
		return Draft{}, fmt.Errorf("%w: structured output is not the expected shape", ErrDecode)
	}
	// Empty arrays stay empty arrays in JSON output instead of becoming null.
	if draft.Requirements == nil {
		draft.Requirements = []Requirement{}
	}
	if draft.Constraints == nil {
		draft.Constraints = []Constraint{}
	}
	if draft.Ambiguities == nil {
		draft.Ambiguities = []Ambiguity{}
	}
	if draft.Assumptions == nil {
		draft.Assumptions = []string{}
	}
	return draft, nil
}
