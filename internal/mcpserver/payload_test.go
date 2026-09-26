package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Payload ceilings in bytes. These are NOT token measurements: they are byte
// budgets that catch a future change which suddenly dumps thousands of
// Evidence records into every tool result. Actual sizes are logged so a
// reviewer can see the headroom.
var payloadByteBudgets = map[string]int{
	"resolve":          8 * 1024,
	"refine":           4 * 1024,
	"enrich":           2 * 1024,
	"inspect_evidence": 16 * 1024,
}

func measure(t *testing.T, name string, value any) int {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	budget := payloadByteBudgets[name]
	t.Logf("payload %s = %d bytes (budget %d bytes)", name, len(encoded), budget)
	if len(encoded) > budget {
		t.Errorf("payload %s = %d bytes, exceeds the %d byte budget", name, len(encoded), budget)
	}
	return len(encoded)
}

// TestPayloadByteBudgets keeps tool results small enough that an agent can
// actually read them.
func TestPayloadByteBudgets(t *testing.T) {
	resolve := newFixture(false)
	_, decision, err := resolve.deps.handleResolve(context.Background(), nil, resolve.resolveRequest(
		CandidateRef{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency},
		CandidateRef{SpecimenID: fixtureLight, ReuseMode: model.ReuseDependency},
		CandidateRef{SpecimenID: fixtureMetadata, ReuseMode: model.ReuseDependency},
	))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	measure(t, "resolve", decision)

	refine := newFixture(false)
	_, refined, err := refine.deps.handleRefine(context.Background(), nil, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: fixtureEligible, ReuseMode: model.ReuseDependency},
			{SpecimenID: fixtureLight, ReuseMode: model.ReuseDependency},
		},
		Feedback: []policyFeedback{{CandidateID: fixtureEligible, Reason: feedbackNotQuite}},
	})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	measure(t, "refine", refined)

	enrich := newFixture(true)
	enrich.enricher.result = enrichment.Result{
		ObservedAt: fixtureClock(),
		Evidence: []model.Evidence{
			{ID: "e1", SubjectID: fixtureEligible, Kind: "source_license", Result: model.EvidenceInfo},
		},
		Specimens: []enrichment.SpecimenReport{{
			SpecimenID: fixtureEligible, Supported: true, EvidenceCount: 1,
			Providers: []enrichment.ProviderReport{{
				ID: "deps.dev", Succeeded: true, EvidenceCount: 1,
				Issues: []enrichment.ProviderIssue{{
					Kind: enrichment.IssueRateLimited, Provider: "deps.dev",
					StatusCode: 429, Message: "rate limited by the provider",
				}},
			}},
		}},
	}
	_, enrichResult, err := enrich.deps.handleEnrich(context.Background(), nil,
		EnrichInput{SpecimenIDs: []string{fixtureEligible}})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	measure(t, "enrich", enrichResult)

	inspect := newFixture(false)
	_, page, err := inspect.deps.handleInspectEvidence(context.Background(), nil, EvidenceInput{
		SubjectID: fixtureEligible, Limit: MaxEvidenceLimit,
	})
	if err != nil {
		t.Fatalf("inspect evidence: %v", err)
	}
	if len(page.Evidence) == 0 {
		t.Fatal("expected a full evidence page to measure")
	}
	measure(t, "inspect_evidence", page)
}
