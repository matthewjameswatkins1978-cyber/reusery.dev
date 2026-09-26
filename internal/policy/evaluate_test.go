package policy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

func basePolicy() Policy {
	return Policy{
		SchemaVersion: SchemaVersion,
		ID:            "test/v1",
		Reuse: ReusePolicy{
			Allowed: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt, model.ReuseReference},
		},
		Licence: LicencePolicy{
			Unknown:  ActionReview,
			Multiple: ActionReview,
			Unlisted: ActionAllow,
		},
		Security: SecurityPolicy{
			KnownAdvisory: ActionReview,
			Unknown:       ActionReview,
		},
		Dependencies: DependencyPolicy{Unknown: ActionReview},
		Maintenance: MaintenancePolicy{
			Archived:   ActionReview,
			Deprecated: ActionReview,
			Stale:      ActionReview,
			Unknown:    ActionReview,
		},
		Source: SourcePolicy{
			RequireRevisionFor: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseAdapt},
			MissingRevision:    ActionReview,
		},
		Selection: SelectionPolicy{MaxOptions: 3},
	}
}

func evaluateTestPolicy(t *testing.T, mutate func(*Policy), specimen model.Specimen, mode model.ReuseMode, facts Facts, now time.Time) []PolicyDecision {
	t.Helper()
	policy := basePolicy()
	if mutate != nil {
		mutate(&policy)
	}
	return Evaluate(policy, specimen, mode, facts, now)
}

func decisionFor(t *testing.T, decisions []PolicyDecision, dimension string) PolicyDecision {
	t.Helper()
	for _, decision := range decisions {
		if decision.Dimension == dimension {
			return decision
		}
	}
	t.Fatalf("no decision for dimension %q in %+v", dimension, decisions)
	return PolicyDecision{}
}

func licenceFacts(observations ...model.Evidence) Facts {
	return Facts{Licence: ExtractFacts(factsSpecimen(), observations).Licence}
}

func TestLicencePolicySemantics(t *testing.T) {
	cases := map[string]struct {
		mutate   func(*Policy)
		facts    Facts
		want     Action
		contains string
		bans     []string
	}{
		"exact single licence allowed": {
			mutate: func(p *Policy) { p.Licence.Allow = []string{"MIT"} },
			facts:  licenceFacts(fact("l", KindSourceLicense, model.EvidenceInfo, "MIT")),
			want:   ActionAllow,
		},
		"exact single licence denied": {
			mutate: func(p *Policy) { p.Licence.Deny = []string{"GPL-3.0"} },
			facts:  licenceFacts(fact("l", KindSourceLicense, model.EvidenceInfo, "GPL-3.0")),
			want:   ActionDeny,
		},
		"licence not in a non-empty allow list applies the unlisted action": {
			mutate: func(p *Policy) {
				p.Licence.Allow = []string{"MIT"}
				p.Licence.Unlisted = ActionReview
			},
			facts:    licenceFacts(fact("l", KindSourceLicense, model.EvidenceInfo, "Apache-2.0")),
			want:     ActionReview,
			contains: "not in the allow list",
		},
		"unlisted action may also be allow": {
			mutate: func(p *Policy) { p.Licence.Allow = []string{"MIT"} },
			facts:  licenceFacts(fact("l", KindSourceLicense, model.EvidenceInfo, "Apache-2.0")),
			want:   ActionAllow,
		},
		"unknown licence": {
			facts:    licenceFacts(fact("l", KindSourceLicense, model.EvidenceUnknown, nil)),
			want:     ActionReview,
			contains: "could not be established",
			bans:     []string{"compatible", "illegal"},
		},
		"multiple licences": {
			facts: licenceFacts(
				fact("l1", KindSourceLicense, model.EvidenceInfo, "MIT"),
				fact("l2", KindSourceLicense, model.EvidenceInfo, "Apache-2.0"),
				fact("rel", KindLicenceRelationship, model.EvidenceUnknown, nil),
			),
			want:     ActionReview,
			contains: "relationship is not established",
		},
		"conflicting licences": {
			facts: licenceFacts(
				fact("l1", KindSourceLicense, model.EvidenceInfo, "MIT"),
				fact("l2", KindSourceLicense, model.EvidenceInfo, "Apache-2.0"),
			),
			want:     ActionReview,
			contains: "conflicting licence values",
		},
		"licence denied even when also allow-listed": {
			mutate: func(p *Policy) {
				p.Licence.Allow = []string{"GPL-3.0", "MIT"}
				p.Licence.Deny = []string{"GPL-3.0"}
			},
			facts: licenceFacts(fact("l", KindSourceLicense, model.EvidenceInfo, "GPL-3.0")),
			want:  ActionDeny,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			decisions := evaluateTestPolicy(t, testCase.mutate, factsSpecimen(), model.ReuseDependency, testCase.facts, time.Time{})
			decision := decisionFor(t, decisions, DimensionLicence)
			if decision.Action != testCase.want {
				t.Errorf("action = %q (%s), want %q", decision.Action, decision.Reason, testCase.want)
			}
			if testCase.contains != "" && !strings.Contains(decision.Reason, testCase.contains) {
				t.Errorf("reason %q does not contain %q", decision.Reason, testCase.contains)
			}
			for _, banned := range testCase.bans {
				if strings.Contains(strings.ToLower(decision.Reason), strings.ToLower(banned)) {
					t.Errorf("reason %q must not contain %q", decision.Reason, banned)
				}
			}
			if strings.Contains(strings.ToLower(decision.Reason), "compatible") {
				t.Errorf("reason %q must never claim legal compatibility", decision.Reason)
			}
			if !strings.Contains(decision.Reason, "policy test/v1") {
				t.Errorf("reason %q must name the policy", decision.Reason)
			}
		})
	}
}

func TestSecurityPolicySemantics(t *testing.T) {
	t.Run("known advisory applies the configured action", func(t *testing.T) {
		facts := Facts{Advisory: ExtractFacts(factsSpecimen(), []model.Evidence{
			fact("a", KindKnownAdvisory, model.EvidenceInfo, "GHSA-1111-2222-3333"),
			fact("c", KindKnownAdvisoryCount, model.EvidenceInfo, 1),
		}).Advisory}
		review := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, facts, time.Time{}), DimensionSecurity)
		if review.Action != ActionReview {
			t.Errorf("action = %q, want review", review.Action)
		}
		if !strings.Contains(review.Reason, "GHSA-1111-2222-3333") {
			t.Errorf("reason = %q", review.Reason)
		}

		deny := decisionFor(t, evaluateTestPolicy(t, func(p *Policy) {
			p.Security.KnownAdvisory = ActionDeny
		}, factsSpecimen(), model.ReuseDependency, facts, time.Time{}), DimensionSecurity)
		if deny.Action != ActionDeny {
			t.Errorf("action = %q, want deny", deny.Action)
		}
	})

	t.Run("zero known advisories allows only the narrow statement", func(t *testing.T) {
		facts := Facts{Advisory: ExtractFacts(factsSpecimen(), []model.Evidence{
			fact("c", KindKnownAdvisoryCount, model.EvidenceInfo, 0),
		}).Advisory}
		decision := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, facts, time.Time{}), DimensionSecurity)
		if decision.Action != ActionAllow {
			t.Errorf("action = %q, want allow for the narrow statement", decision.Action)
		}
		if !strings.Contains(decision.Reason, "no known direct advisory IDs were reported by this source at this time") {
			t.Errorf("reason = %q", decision.Reason)
		}
		for _, banned := range []string{"secure", "safe", "vulnerability", "passed security", "evidencepass", "pass"} {
			if strings.Contains(strings.ToLower(decision.Reason), banned) {
				t.Errorf("reason %q must never say %q", decision.Reason, banned)
			}
		}
	})

	t.Run("unknown advisory applies the unknown action", func(t *testing.T) {
		decision := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, Facts{}, time.Time{}), DimensionSecurity)
		if decision.Action != ActionReview {
			t.Errorf("action = %q, want review", decision.Action)
		}
		if !strings.Contains(decision.Reason, "not established") {
			t.Errorf("reason = %q", decision.Reason)
		}
	})
}

func TestDependencyPolicySemantics(t *testing.T) {
	withMax := func(max int) func(*Policy) {
		return func(p *Policy) { p.Dependencies.MaxDirect = &max }
	}

	factsWith := func(observations ...model.Evidence) Facts {
		return Facts{Dependency: ExtractFacts(factsSpecimen(), observations).Dependency}
	}

	t.Run("within maximum allows", func(t *testing.T) {
		decisions := evaluateTestPolicy(t, withMax(5), factsSpecimen(), model.ReuseDependency,
			factsWith(fact("c", KindDirectDependencyCount, model.EvidenceInfo, 2)), time.Time{})
		decision := decisionFor(t, decisions, DimensionDependencies)
		if decision.Action != ActionAllow {
			t.Errorf("action = %q", decision.Action)
		}
		if !strings.Contains(decision.Reason, "within configured maximum 5") {
			t.Errorf("reason = %q", decision.Reason)
		}
	})

	t.Run("exceeding maximum denies", func(t *testing.T) {
		decisions := evaluateTestPolicy(t, withMax(5), factsSpecimen(), model.ReuseDependency,
			factsWith(fact("c", KindDirectDependencyCount, model.EvidenceInfo, 12)), time.Time{})
		decision := decisionFor(t, decisions, DimensionDependencies)
		if decision.Action != ActionDeny {
			t.Errorf("action = %q, want deny", decision.Action)
		}
		if !strings.Contains(decision.Reason, "exceeds configured maximum 5") {
			t.Errorf("reason = %q", decision.Reason)
		}
	})

	t.Run("unknown count applies the unknown action when a maximum exists", func(t *testing.T) {
		decisions := evaluateTestPolicy(t, withMax(5), factsSpecimen(), model.ReuseDependency, Facts{}, time.Time{})
		decision := decisionFor(t, decisions, DimensionDependencies)
		if decision.Action != ActionReview {
			t.Errorf("action = %q, want review", decision.Action)
		}
	})

	t.Run("no maximum means no quantity decision at all", func(t *testing.T) {
		decisions := evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency,
			factsWith(fact("c", KindDirectDependencyCount, model.EvidenceInfo, 999)), time.Time{})
		for _, decision := range decisions {
			if decision.Dimension == DimensionDependencies {
				t.Fatalf("unexpected dependency decision without a threshold: %+v", decision)
			}
		}
	})
}

func TestMaintenancePolicySemantics(t *testing.T) {
	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

	t.Run("archived review and deny", func(t *testing.T) {
		facts := Facts{Archived: ExtractFacts(factsSpecimen(), []model.Evidence{
			fact("a", KindRepositoryArchived, model.EvidenceInfo, true),
		}).Archived}

		review := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseReference, facts, now), DimensionMaintenanceArchived)
		if review.Action != ActionReview || !strings.Contains(review.Reason, "archived") {
			t.Errorf("review = %+v", review)
		}

		deny := decisionFor(t, evaluateTestPolicy(t, func(p *Policy) {
			p.Maintenance.Archived = ActionDeny
		}, factsSpecimen(), model.ReuseReference, facts, now), DimensionMaintenanceArchived)
		if deny.Action != ActionDeny {
			t.Errorf("action = %q, want deny", deny.Action)
		}
	})

	t.Run("deprecated applies the configured action with its reason", func(t *testing.T) {
		facts := Facts{Deprecated: ExtractFacts(factsSpecimen(), []model.Evidence{
			fact("d", KindPackageDeprecated, model.EvidenceInfo, true),
			fact("r", KindPackageDeprecatedReason, model.EvidenceInfo, "superseded"),
		}).Deprecated}
		decision := decisionFor(t, evaluateTestPolicy(t, func(p *Policy) {
			p.Maintenance.Deprecated = ActionDeny
		}, factsSpecimen(), model.ReuseDependency, facts, now), DimensionMaintenanceDeprecated)
		if decision.Action != ActionDeny {
			t.Errorf("action = %q", decision.Action)
		}
		if !strings.Contains(decision.Reason, "superseded") {
			t.Errorf("reason = %q", decision.Reason)
		}
	})

	t.Run("stale threshold exact boundary", func(t *testing.T) {
		mutate := func(p *Policy) { p.Maintenance.MaxDaysSincePush = intPtr(365) }

		atBoundary := Facts{LastPush: LastPushFact{Status: FactKnown, Known: true, PushedAt: now.AddDate(0, 0, -365)}}
		decision := decisionFor(t, evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, atBoundary, now), DimensionMaintenanceStale)
		if decision.Action != ActionAllow {
			t.Errorf("action = %q at exactly 365 days, want allow", decision.Action)
		}

		over := Facts{LastPush: LastPushFact{Status: FactKnown, Known: true, PushedAt: now.AddDate(0, 0, -366)}}
		decision = decisionFor(t, evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, over, now), DimensionMaintenanceStale)
		if decision.Action != ActionReview {
			t.Errorf("action = %q at 366 days, want the stale action", decision.Action)
		}
		if !strings.Contains(decision.Reason, "last push is 366 days old and policy limit is 365 days") {
			t.Errorf("reason = %q", decision.Reason)
		}

		missing := Facts{}
		decision = decisionFor(t, evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, missing, now), DimensionMaintenanceStale)
		if decision.Action != ActionReview {
			t.Errorf("action = %q, want the unknown action when a threshold exists but the fact does not", decision.Action)
		}
	})

	t.Run("no threshold means no staleness decision", func(t *testing.T) {
		facts := Facts{LastPush: LastPushFact{Status: FactUnknown}}
		for _, decision := range evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseReference, facts, now) {
			if decision.Dimension == DimensionMaintenanceStale {
				t.Fatalf("unexpected staleness decision without a threshold: %+v", decision)
			}
		}
	})

	t.Run("max_days_since_release is evaluated against the newest publication", func(t *testing.T) {
		mutate := func(p *Policy) { p.Maintenance.MaxDaysSinceRelease = intPtr(30) }
		facts := Facts{PublishedAt: PublishedAtFact{Status: FactKnown, Known: true, PublishedAt: now.AddDate(0, 0, -31)}}
		decision := decisionFor(t, evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, facts, now), DimensionMaintenanceStale)
		if decision.Action != ActionReview || !strings.Contains(decision.Reason, "latest package publication") {
			t.Errorf("decision = %+v", decision)
		}
	})
}

func TestSourceProvenancePolicySemantics(t *testing.T) {
	pinned := Facts{Revision: RevisionFact{Status: FactKnown, Known: true, Revision: "abc123"}}
	unpinned := Facts{Revision: RevisionFact{Status: FactUnknown}}

	t.Run("pinned revision allows", func(t *testing.T) {
		decision := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, pinned, time.Time{}), DimensionSourceRevision)
		if decision.Action != ActionAllow || !strings.Contains(decision.Reason, "source is pinned to revision abc123") {
			t.Errorf("decision = %+v", decision)
		}
	})

	t.Run("missing revision review and deny for a required mode", func(t *testing.T) {
		review := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, unpinned, time.Time{}), DimensionSourceRevision)
		if review.Action != ActionReview {
			t.Errorf("action = %q, want review", review.Action)
		}
		if !strings.Contains(review.Reason, "requires a pinned or versioned revision") {
			t.Errorf("reason = %q", review.Reason)
		}

		deny := decisionFor(t, evaluateTestPolicy(t, func(p *Policy) {
			p.Source.MissingRevision = ActionDeny
		}, factsSpecimen(), model.ReuseCopy, unpinned, time.Time{}), DimensionSourceRevision)
		if deny.Action != ActionDeny {
			t.Errorf("action = %q, want deny", deny.Action)
		}
	})

	t.Run("reference without a pin surfaces the weakness without denying", func(t *testing.T) {
		decision := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseReference, unpinned, time.Time{}), DimensionSourceRevision)
		if decision.Action != ActionReview {
			t.Errorf("action = %q, want review so the weakness is surfaced", decision.Action)
		}
		if !strings.Contains(decision.Reason, "provenance weakness") {
			t.Errorf("reason = %q", decision.Reason)
		}
	})
}

func TestReusePolicySemantics(t *testing.T) {
	allowed := decisionFor(t, evaluateTestPolicy(t, nil, factsSpecimen(), model.ReuseDependency, Facts{}, time.Time{}), DimensionReuse)
	if allowed.Action != ActionAllow {
		t.Errorf("action = %q", allowed.Action)
	}

	denied := decisionFor(t, evaluateTestPolicy(t, func(p *Policy) {
		p.Reuse.Allowed = []model.ReuseMode{model.ReuseReference}
	}, factsSpecimen(), model.ReuseDependency, Facts{}, time.Time{}), DimensionReuse)
	if denied.Action != ActionDeny {
		t.Errorf("action = %q, want deny", denied.Action)
	}
}

func TestDecisionOrderIsDeterministicAndScoreless(t *testing.T) {
	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	facts := Facts{
		Licence:            ExtractFacts(factsSpecimen(), []model.Evidence{fact("l", KindSourceLicense, model.EvidenceInfo, "MIT")}).Licence,
		Advisory:           ExtractFacts(factsSpecimen(), []model.Evidence{fact("c", KindKnownAdvisoryCount, model.EvidenceInfo, 0)}).Advisory,
		Dependency:         ExtractFacts(factsSpecimen(), []model.Evidence{fact("d", KindDirectDependencyCount, model.EvidenceInfo, 1)}).Dependency,
		Archived:           ExtractFacts(factsSpecimen(), []model.Evidence{fact("a", KindRepositoryArchived, model.EvidenceInfo, false)}).Archived,
		Deprecated:         ExtractFacts(factsSpecimen(), []model.Evidence{fact("p", KindPackageDeprecated, model.EvidenceInfo, false)}).Deprecated,
		LastPush:           LastPushFact{Status: FactUnknown},
		Revision:           RevisionFact{Status: FactKnown, Known: true, Revision: "abc"},
		DiscoveryRelevance: DiscoveryRelevanceFact{Status: FactKnown, Matched: true},
	}
	mutate := func(p *Policy) {
		p.Maintenance.MaxDaysSincePush = intPtr(365)
		p.Dependencies.MaxDirect = intPtr(5)
	}

	first := evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, facts, now)
	second := evaluateTestPolicy(t, mutate, factsSpecimen(), model.ReuseDependency, facts, now)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("evaluation is not deterministic:\n%+v\n%+v", first, second)
	}

	wantOrder := []string{
		DimensionReuse,
		DimensionLicence,
		DimensionSecurity,
		DimensionDependencies,
		DimensionMaintenanceArchived,
		DimensionMaintenanceDeprecated,
		DimensionMaintenanceStale,
		DimensionSourceRevision,
	}
	if len(first) != len(wantOrder) {
		t.Fatalf("got %d decisions, want %d: %+v", len(first), len(wantOrder), first)
	}
	for index, dimension := range wantOrder {
		if first[index].Dimension != dimension {
			t.Errorf("decision[%d] = %q, want %q", index, first[index].Dimension, dimension)
		}
	}

	for _, decision := range first {
		payload, err := json.Marshal(decision)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var fields map[string]any
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, banned := range []string{"score", "confidence", "weight", "rank", "quality"} {
			if _, present := fields[banned]; present {
				t.Errorf("decision exposes a %q field: %s", banned, payload)
			}
		}
	}
}

func TestCountActionsTreatsReviewAsNeitherAllowNorDeny(t *testing.T) {
	decisions := []PolicyDecision{
		{Dimension: DimensionLicence, Action: ActionAllow},
		{Dimension: DimensionSecurity, Action: ActionReview},
		{Dimension: DimensionMaintenanceArchived, Action: ActionDeny},
	}
	denies, reviews := CountActions(decisions)
	if denies != 1 || reviews != 1 {
		t.Errorf("denies=%d reviews=%d, want 1/1", denies, reviews)
	}
}

func intPtr(value int) *int { return &value }
