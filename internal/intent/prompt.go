package intent

import (
	_ "embed"
	"strings"
)

// The normaliser prompt is part of the product behaviour. It is code-owned,
// reviewable, versioned and tested rather than hidden inside a long Go string.
var (
	//go:embed assets/normalizer-v1.txt
	normalizerPrompt string
	//go:embed assets/repair-v1.txt
	repairPrompt string
)

// Prompt and schema versions. Changing either changes generated results, so
// both feed the deterministic identity digest.
const (
	// PromptVersion identifies the normaliser instruction text.
	PromptVersion = "intent-normalizer/v1"
	// RepairPromptVersion identifies the bounded repair instruction text.
	RepairPromptVersion = "intent-repair/v1"
	// SchemaVersion identifies the structured output schema.
	SchemaVersion = 1
	// SchemaName is the structured output schema name sent to the provider.
	SchemaName = "normalized_intent"
)

// Prompt returns the code-owned normaliser instructions.
func Prompt() string { return normalizerPrompt }

// RepairPrompt returns the code-owned bounded repair instructions without the
// prior draft or the validation failures appended.
func RepairPrompt() string { return repairPrompt }

// RenderRepairPrompt composes the full repair instruction. The prior draft and
// the validation failures are appended here, inside the intent domain, so a
// model adapter never learns Reusery's validation vocabulary.
//
// Validation failures are deterministic messages this package produced; they
// never contain Go error strings or implementation internals.
func RenderRepairPrompt(priorDraft string, failures []string) string {
	var b strings.Builder
	b.WriteString(repairPrompt)
	b.WriteString("PRIOR DRAFT\n")
	b.WriteString(priorDraft)
	if !strings.HasSuffix(priorDraft, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\nVALIDATION FAILURES\n")
	for _, failure := range failures {
		b.WriteString("- ")
		b.WriteString(failure)
		b.WriteString("\n")
	}
	return b.String()
}
