package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// policyFeedback and the reason constants are aliases so the tests read like
// the tool contract without re-declaring the vocabulary.
type policyFeedback = policy.Feedback

const (
	feedbackNotQuite            = policy.FeedbackNotQuite
	feedbackTooManyDependencies = policy.FeedbackTooManyDependencies
	feedbackAvoidReference      = policy.FeedbackAvoidReference
)

// policyDTOForTest is a complete, valid structured policy. Every field is
// present because the transport schema requires them, exactly as HTTP does.
func policyDTOForTest() Policy {
	return Policy{
		SchemaVersion: 1,
		ID:            "test/v1",
		Reuse: PolicyReuse{
			Allowed:   []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference},
			Preferred: []model.ReuseMode{},
		},
		Licence: PolicyLicence{
			Allow:    []string{"MIT"},
			Deny:     []string{},
			Unknown:  policy.ActionReview,
			Multiple: policy.ActionReview,
			Unlisted: policy.ActionReview,
		},
		Security: PolicySecurity{
			KnownAdvisory: policy.ActionReview,
			Unknown:       policy.ActionReview,
		},
		Dependencies: PolicyDeps{
			Unknown:   policy.ActionReview,
			MaxDirect: nil,
		},
		Maintenance: PolicyMaint{
			Archived:   policy.ActionReview,
			Deprecated: policy.ActionReview,
			Stale:      policy.ActionReview,
			Unknown:    policy.ActionReview,
		},
		Source: PolicySource{
			RequireRevisionFor: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt},
			MissingRevision:    policy.ActionReview,
		},
		Selection: PolicySelection{MaxOptions: 3},
	}
}

// withPolicy attaches an explicit structured policy to a resolve request.
func withPolicy(in ResolveInput, pol Policy) ResolveInput {
	in.Policy = &pol
	return in
}

func intPointer(value int) *int { return &value }

// errorsNew exists so the leak test reads as a plain unknown failure.
func errorsNew(message string) error { return errors.New(message) }

// registeredDescriptions returns every tool description by name so copy-level
// assertions do not need a live protocol session.
func registeredDescriptions() map[string]string {
	descriptions := map[string]string{
		ToolCatalog:           "List the engineering capabilities Reusery knows",
		ToolDiscover:          "Discover plausible public specimens",
		ToolEnrich:            "Record attributable licence",
		ToolResolve:           "Compare candidates under an explicit policy",
		ToolRefine:            "Reject a result with a supported structured reason",
		ToolInspectEvidence:   "Read a bounded, ordered page of stored evidence",
		ToolInspectResolution: "Read a remembered Resolution exactly as recorded",
		ToolReportOutcome:     "Record what actually happened after a Resolution was used",
	}
	return descriptions
}

// callTool invokes one tool and returns the structured output.
func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("call %s returned a tool error: %v", name, textOf(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var structured map[string]any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return result, structured
}

// textOf returns the short human-readable summary of a successful result.
func textOf(result *mcp.CallToolResult) string {
	out := ""
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out += text.Text
		}
	}
	return out
}
