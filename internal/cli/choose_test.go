package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

const (
	chooseRequestRel  = "examples/quality-bounded-subprocess.json"
	choosePolicyRel   = "policies/public-go-baseline-v1.yaml"
	chooseFeedbackRel = "examples/quality-feedback-not-quite.json"
)

// seedQualityFixtures loads the canonical seed bundle into the in-memory store
// so `choose` has real specimens and evidence to assess.
func seedQualityFixtures(t *testing.T, store *fakeStore) {
	t.Helper()
	bundle, err := catalog.Load(repoRoot, manifestRel)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	store.primitives[bundle.Primitive.ID] = bundle.Primitive
	store.contracts[bundle.Contract.ID] = bundle.Contract
	for _, specimen := range bundle.Specimens {
		store.specimens[specimen.ID] = specimen
	}
	for _, evidence := range bundle.Evidence {
		store.evidence[evidence.ID] = evidence
	}
}

func chooseApp(t *testing.T, store Store) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, _, _, _ := testApp(t)
	app.Stdout = stdout
	app.Stderr = stderr
	app.OpenStore = func(context.Context, config.Config) (Store, func(), error) {
		return store, func() {}, nil
	}
	app.LoadPolicy = policy.Load
	app.LoadFeedback = policy.LoadFeedback
	// `choose` is offline by construction. If it ever reaches for enrichment
	// the test fails loudly.
	app.NewEnricher = func(Store, config.Config, enrichment.Clock) Enricher {
		t.Error("choose must never construct an enricher")
		return nil
	}
	return app, stdout, stderr
}

func writeChooseRequest(t *testing.T, root, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return name
}

func permissivePolicyYAML() string {
	return `
schema_version: 1
id: test/choose/v1
reuse:
  allowed: [copy, dependency, adapt, reference]
license:
  unknown: allow
  multiple: review
  unlisted: allow
security:
  known_advisory: review
  unknown: allow
dependencies:
  unknown: allow
maintenance:
  archived: review
  deprecated: review
  stale: review
  unknown: allow
source:
  require_revision_for: []
  missing_revision: allow
selection:
  max_options: 3
`
}

func TestChooseRequiresRequestAndPolicy(t *testing.T) {
	app, _, stderr := chooseApp(t, newFakeStore())
	if code := app.Run(context.Background(), []string{"choose"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--request is required") {
		t.Errorf("stderr = %q", stderr.String())
	}

	app2, _, stderr2 := chooseApp(t, newFakeStore())
	if code := app2.Run(context.Background(), []string{"choose", "--request", "x.json"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr2.String(), "--policy is required") {
		t.Errorf("stderr = %q", stderr2.String())
	}
}

func TestChooseRejectsStructuralProblemsAsUsageErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad-policy.yaml"), []byte("schema_version: 9\nid: x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad-request.json"), []byte(`{"primitive_id":"a","rank":1}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := map[string][]string{
		"missing policy file": {"choose", "--root", root, "--request", "x.json", "--policy", "nope.yaml"},
		"policy path escape":  {"choose", "--root", root, "--request", "x.json", "--policy", "../escape.yaml"},
		"invalid policy":      {"choose", "--root", root, "--request", "x.json", "--policy", "bad-policy.yaml"},
		"missing request":     {"choose", "--root", root, "--request", "nope.json", "--policy", choosePolicyRel},
		"request path escape": {"choose", "--root", root, "--request", "../escape.json", "--policy", choosePolicyRel},
		"unknown field":       {"choose", "--root", root, "--request", "bad-request.json", "--policy", choosePolicyRel},
		"bad format":          {"choose", "--root", root, "--request", "bad-request.json", "--policy", choosePolicyRel, "--format", "yaml"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			seedQualityFixtures(t, store)
			app, _, stderr := chooseApp(t, store)
			if code := app.Run(context.Background(), args); code != ExitUsage {
				t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
			}
		})
	}
}

func TestChooseFeedbackStructuralErrorIsAUsageError(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [{"specimen_id": "fixture/process/bounded-subprocess/complete-dependency", "reuse_mode": "dependency"}]
	}`)
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())
	writeChooseRequest(t, root, "feedback.json", `{"feedback":[{"candidate_id":"ghost","reason":"not_quite"}]}`)

	store := newFakeStore()
	seedQualityFixtures(t, store)
	app, _, stderr := chooseApp(t, store)
	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json",
		"--policy", "policy.yaml", "--feedback", "feedback.json",
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not in the request") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestChooseReturnsNeedsVerificationWithoutPersisting(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())
	// A plausible candidate whose required behavioural evidence has simply not
	// been collected yet: no PASS, no FAIL. Reusery must wait rather than
	// conclude that local code is better.
	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [
			{"specimen_id": "fixture/process/bounded-subprocess/unknown-behaviour", "reuse_mode": "dependency"}
		]
	}`)

	store := newFakeStore()
	seedQualityFixtures(t, store)
	store.specimens["fixture/process/bounded-subprocess/unknown-behaviour"] = model.Specimen{
		ID:          "fixture/process/bounded-subprocess/unknown-behaviour",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "Development fixture: plausible but unverified",
		Source:      model.SourceRef{URL: "https://example.test/unknown", Revision: "v1.0.0"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}

	app, stdout, stderr := chooseApp(t, store)

	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json", "--policy", "policy.yaml",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (needs_verification is a valid outcome); stderr=%q", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: needs_verification") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "resolution_id:") {
		t.Errorf("nothing must be persisted:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "outcome: build_locally") {
		t.Errorf("UNKNOWN must never become BUILD LOCALLY:\n%s", stdout.String())
	}
	if len(store.resolutions) != 0 {
		t.Errorf("persisted %d resolutions", len(store.resolutions))
	}
	if !strings.Contains(stderr.String(), "needs verification") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestChooseResolvesToDependWhenBehaviourAndPolicyBothPermit(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())

	store := newFakeStore()
	seedQualityFixtures(t, store)

	// Give the complete fixture a pinned revision so provenance is satisfied.
	specimen := store.specimens["fixture/process/bounded-subprocess/complete-dependency"]
	specimen.Source.Revision = "v1.0.0"
	store.specimens[specimen.ID] = specimen

	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [
			{"specimen_id": "fixture/process/bounded-subprocess/complete-dependency", "reuse_mode": "dependency"},
			{"specimen_id": "fixture/process/bounded-subprocess/partial-adapt", "reuse_mode": "adapt"}
		]
	}`)

	app, stdout, stderr := chooseApp(t, store)
	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json", "--policy", "policy.yaml",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"status: resolved",
		"policy: test/choose/v1",
		"outcome: depend",
		"resolution_id: 1",
		"shortlist:",
		"all candidates:",
		"applied feedback: (none)",
		"requirements:",
		"policy:",
		"pros:",
		"cons:",
		"unknowns:",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("stdout missing %q:\n%s", want, output)
		}
	}
	if len(store.resolutions) != 1 {
		t.Fatalf("persisted %d resolutions, want 1", len(store.resolutions))
	}
	if store.resolutions[0].PolicyID != "test/choose/v1" {
		t.Errorf("policy id = %q", store.resolutions[0].PolicyID)
	}
	if store.resolutions[0].Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q", store.resolutions[0].Outcome)
	}
}

func TestChooseJSONOutputShape(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())
	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [
			{"specimen_id": "fixture/process/bounded-subprocess/complete-dependency", "reuse_mode": "dependency"}
		]
	}`)

	store := newFakeStore()
	seedQualityFixtures(t, store)
	specimen := store.specimens["fixture/process/bounded-subprocess/complete-dependency"]
	specimen.Source.Revision = "v1.0.0"
	store.specimens[specimen.ID] = specimen

	app, stdout, stderr := chooseApp(t, store)
	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json", "--policy", "policy.yaml", "--format", "json",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}

	var payload struct {
		Status          resolver.DecisionStatus        `json:"status"`
		PolicyID        string                         `json:"policy_id"`
		EffectivePolicy policy.EffectiveSummary        `json:"effective_policy"`
		ResolutionID    int64                          `json:"resolution_id"`
		Resolution      *model.Resolution              `json:"resolution"`
		Selected        *resolver.CandidateAssessment  `json:"selected"`
		Shortlist       []resolver.CandidateAssessment `json:"shortlist"`
		Assessments     []resolver.CandidateAssessment `json:"assessments"`
		AppliedFeedback []policy.AppliedFeedback       `json:"applied_feedback"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != resolver.StatusResolved {
		t.Errorf("status = %q", payload.Status)
	}
	if payload.PolicyID != "test/choose/v1" || payload.EffectivePolicy.ID != "test/choose/v1" {
		t.Errorf("policy = %q / %+v", payload.PolicyID, payload.EffectivePolicy)
	}
	if payload.ResolutionID == 0 || payload.Resolution == nil || payload.Selected == nil {
		t.Errorf("resolved output is missing persisted fields: %+v", payload)
	}
	if len(payload.Shortlist) == 0 || len(payload.Assessments) == 0 {
		t.Errorf("shortlist/assessments missing: %+v", payload)
	}
	if payload.AppliedFeedback == nil {
		t.Error("applied_feedback must always be present")
	}

	raw := strings.ToLower(stdout.String())
	for _, banned := range []string{"quality_score", `"confidence"`, "stargazers", "stars_count"} {
		if strings.Contains(raw, banned) {
			t.Errorf("output mentions %q:\n%s", banned, stdout.String())
		}
	}
}

func TestChooseJSONOmitsResolutionIDWhenNothingIsPersisted(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())
	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [
			{"specimen_id": "fixture/process/bounded-subprocess/unknown-behaviour", "reuse_mode": "dependency"}
		]
	}`)

	store := newFakeStore()
	seedQualityFixtures(t, store)
	store.specimens["fixture/process/bounded-subprocess/unknown-behaviour"] = model.Specimen{
		ID:          "fixture/process/bounded-subprocess/unknown-behaviour",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "Development fixture: plausible but unverified",
		Source:      model.SourceRef{URL: "https://example.test/unknown", Revision: "v1.0.0"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	app, stdout, _ := chooseApp(t, store)
	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json", "--policy", "policy.yaml", "--format", "json",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout.String(), `"resolution_id"`) {
		t.Errorf("resolution_id must be omitted when nothing persisted:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"resolution"`) {
		t.Errorf("resolution must be omitted when nothing persisted:\n%s", stdout.String())
	}
}

func TestChooseAppliesFeedbackAndRecordsNegativeKnowledge(t *testing.T) {
	root := t.TempDir()
	writeChooseRequest(t, root, "policy.yaml", permissivePolicyYAML())
	writeChooseRequest(t, root, "request.json", `{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": [
			{"specimen_id": "fixture/process/bounded-subprocess/complete-dependency", "reuse_mode": "dependency"},
			{"specimen_id": "fixture/process/bounded-subprocess/partial-adapt", "reuse_mode": "adapt"}
		]
	}`)
	writeChooseRequest(t, root, "feedback.json", `{
		"feedback": [{"candidate_id": "fixture/process/bounded-subprocess/complete-dependency", "reason": "not_quite"}]
	}`)

	store := newFakeStore()
	seedQualityFixtures(t, store)
	specimen := store.specimens["fixture/process/bounded-subprocess/complete-dependency"]
	specimen.Source.Revision = "v1.0.0"
	store.specimens[specimen.ID] = specimen

	app, stdout, stderr := chooseApp(t, store)
	code := app.Run(context.Background(), []string{
		"choose", "--root", root, "--request", "request.json",
		"--policy", "policy.yaml", "--feedback", "feedback.json",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "applied feedback:") || !strings.Contains(output, "not_quite") {
		t.Errorf("applied feedback missing:\n%s", output)
	}
	if !strings.Contains(output, "user_feedback:not_quite") {
		t.Errorf("negative knowledge missing from rejections:\n%s", output)
	}
	if strings.Contains(output, "outcome: depend") {
		t.Errorf("the excluded candidate was still selected:\n%s", output)
	}
}

func TestChooseUsesTheRepositoryBaselineProfile(t *testing.T) {
	store := newFakeStore()
	seedQualityFixtures(t, store)
	app, stdout, stderr := chooseApp(t, store)

	code := app.Run(context.Background(), []string{
		"choose", "--root", repoRoot, "--request", chooseRequestRel, "--policy", choosePolicyRel,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "policy: public-go-baseline/v1") {
		t.Errorf("stdout = %q", output)
	}
	if !strings.Contains(output, "shortlist:") {
		t.Errorf("stdout missing the shortlist:\n%s", output)
	}
	for _, banned := range []string{"quality_score", "confidence", "secure", "vulnerability-free"} {
		if strings.Contains(strings.ToLower(output), banned) {
			t.Errorf("stdout mentions %q:\n%s", banned, output)
		}
	}
}
