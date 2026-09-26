package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

func TestRefineRequiresFeedback(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	body := refineRequest(apiPolicy(), []Feedback{}, candidate("fixture/eligible", model.ReuseDependency))
	rec := call(t, h, http.MethodPost, "/v1/refine", body)

	requireStatus(t, rec, http.StatusUnprocessableEntity)
	requireContentType(t, rec, "application/problem+json")
	if repo.count() != 0 {
		t.Errorf("persisted resolutions = %d, want 0", repo.count())
	}
}

// TestRefineNotQuiteSelectsADifferentCandidate proves feedback changes the
// decision rather than advancing an array index: the effective policy gains an
// exclusion and a different candidate becomes the answer.
func TestRefineNotQuiteSelectsADifferentCandidate(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	candidates := []CandidateRef{
		candidate("fixture/eligible", model.ReuseDependency),
		candidate("fixture/light", model.ReuseDependency),
	}

	base := call(t, h, http.MethodPost, "/v1/resolve", resolveRequest(apiPolicy(), candidates...))
	requireStatus(t, base, http.StatusOK)
	baseDecision := decodeDecision(t, base)
	selected := baseDecision["selected"].(map[string]any)["specimen_id"]
	if selected != "fixture/eligible" {
		t.Fatalf("base selection = %v, want fixture/eligible", selected)
	}

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(apiPolicy(), []Feedback{
		{CandidateID: "fixture/eligible", Reason: policy.FeedbackNotQuite},
	}, candidates...))

	requireStatus(t, refined, http.StatusOK)
	refinedDecision := decodeDecision(t, refined)
	if refinedDecision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", refinedDecision["status"], refined.Body.String())
	}
	newSelection := refinedDecision["selected"].(map[string]any)["specimen_id"]
	if newSelection == selected {
		t.Errorf("refinement returned the same candidate %v", newSelection)
	}
	if newSelection != "fixture/light" {
		t.Errorf("refined selection = %v, want fixture/light", newSelection)
	}

	// The effective policy changed; this is not pagination.
	effective := refinedDecision["effective_policy"].(map[string]any)
	if effective["id"] != "api/test-v1" {
		t.Errorf("effective policy id = %v", effective["id"])
	}
	feedback := refinedDecision["applied_feedback"].([]any)
	if len(feedback) != 1 {
		t.Fatalf("applied_feedback = %d items, want 1", len(feedback))
	}
	applied := feedback[0].(map[string]any)
	if applied["reason"] != "not_quite" {
		t.Errorf("applied reason = %v", applied["reason"])
	}

	// Negative knowledge is preserved on the persisted resolution.
	resolution := refinedDecision["resolution"].(map[string]any)
	rejected := resolution["rejected"].([]any)
	found := false
	for _, raw := range rejected {
		entry := raw.(map[string]any)
		if entry["specimen_id"] == "fixture/eligible" {
			found = true
			if reasons := joinAny(entry["reasons"].([]any)); !strings.Contains(reasons, "user_feedback:not_quite") {
				t.Errorf("rejection reasons missing feedback marker: %s", reasons)
			}
		}
	}
	if !found {
		t.Errorf("excluded candidate missing from rejected: %s", resolution["rejected"])
	}
	if repo.count() != 2 {
		t.Errorf("persisted resolutions = %d, want 2", repo.count())
	}
}

func TestRefineTooManyDependenciesRefinesMaxDirect(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	pol := apiPolicy()
	pol.Dependencies = PolicyDeps{Unknown: policy.ActionReview, MaxDirect: intPtr(10)}
	candidates := []CandidateRef{
		candidate("fixture/heavy", model.ReuseDependency),
		candidate("fixture/light", model.ReuseDependency),
	}

	base := call(t, h, http.MethodPost, "/v1/resolve", resolveRequest(pol, candidates...))
	requireStatus(t, base, http.StatusOK)
	baseDecision := decodeDecision(t, base)
	if baseDecision["selected"].(map[string]any)["specimen_id"] != "fixture/heavy" {
		t.Fatalf("base selection = %v, want fixture/heavy", baseDecision["selected"])
	}

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(pol, []Feedback{
		{CandidateID: "fixture/heavy", Reason: policy.FeedbackTooManyDependencies},
	}, candidates...))

	requireStatus(t, refined, http.StatusOK)
	refinedDecision := decodeDecision(t, refined)
	if refinedDecision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", refinedDecision["status"], refined.Body.String())
	}
	if got := refinedDecision["selected"].(map[string]any)["specimen_id"]; got != "fixture/light" {
		t.Errorf("refined selection = %v, want fixture/light", got)
	}
	effective := refinedDecision["effective_policy"].(map[string]any)
	if effective["max_direct"] != float64(7) {
		t.Errorf("effective max_direct = %v, want 7", effective["max_direct"])
	}
	feedback := refinedDecision["applied_feedback"].([]any)
	applied := feedback[0].(map[string]any)
	if !strings.Contains(applied["refinement"].(string), "7") {
		t.Errorf("refinement description = %v, want it to mention the new ceiling", applied["refinement"])
	}
}

func TestRefineLicenceNotAllowedBlocksCandidate(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	pol := apiPolicy()
	pol.Licence.Deny = []string{}

	base := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(pol, candidate("fixture/gpl", model.ReuseDependency)))
	requireStatus(t, base, http.StatusOK)
	baseDecision := decodeDecision(t, base)
	if baseDecision["status"] != "needs_verification" {
		t.Fatalf("status = %v, want needs_verification before feedback; body=%s", baseDecision["status"], base.Body.String())
	}

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(pol, []Feedback{
		{CandidateID: "fixture/gpl", Reason: policy.FeedbackLicenceNotAllowed},
	}, candidate("fixture/gpl", model.ReuseDependency)))

	requireStatus(t, refined, http.StatusOK)
	refinedDecision := decodeDecision(t, refined)
	if refinedDecision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", refinedDecision["status"], refined.Body.String())
	}
	resolution := refinedDecision["resolution"].(map[string]any)
	if resolution["outcome"] != "build_locally" {
		t.Errorf("outcome = %v, want build_locally once the only candidate is denied", resolution["outcome"])
	}
	rejected := rejectedText(resolution)
	if !strings.Contains(rejected, "licence") {
		t.Errorf("rejection reasons do not mention the licence: %s", rejected)
	}
}

func TestRefineAvoidDependencyExcludesCandidate(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(apiPolicy(), []Feedback{
		{CandidateID: "fixture/eligible", Reason: policy.FeedbackAvoidDependency},
	}, candidate("fixture/eligible", model.ReuseDependency)))

	requireStatus(t, refined, http.StatusOK)
	decision := decodeDecision(t, refined)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", decision["status"], refined.Body.String())
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "build_locally" {
		t.Errorf("outcome = %v, want build_locally", resolution["outcome"])
	}
	reasons := joinAny(resolution["reasons"].([]any))
	if !strings.Contains(reasons, "excluded by user feedback") {
		t.Errorf("build-locally reasons missing the exclusion statement: %s", reasons)
	}
	rejected := rejectedText(resolution)
	if !strings.Contains(rejected, "user_feedback:avoid_dependency") {
		t.Errorf("negative knowledge missing: %s", rejected)
	}
}

func TestRefineAvoidReferenceExcludesReferenceCandidate(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(apiPolicy(), []Feedback{
		{CandidateID: "fixture/reference", Reason: policy.FeedbackAvoidReference},
	}, candidate("fixture/reference", model.ReuseReference)))

	requireStatus(t, refined, http.StatusOK)
	decision := decodeDecision(t, refined)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", decision["status"], refined.Body.String())
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "build_locally" {
		t.Errorf("outcome = %v, want build_locally", resolution["outcome"])
	}
	rejected := rejectedText(resolution)
	if !strings.Contains(rejected, "user_feedback:avoid_reference") {
		t.Errorf("negative knowledge missing: %s", rejected)
	}
}

func TestRefineArchivedProjectChangesEffectivePolicy(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	pol := apiPolicy()
	pol.Maintenance.Archived = policy.ActionReview

	base := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(pol, candidate("fixture/archived", model.ReuseDependency)))
	requireStatus(t, base, http.StatusOK)
	baseDecision := decodeDecision(t, base)
	if baseDecision["status"] != "needs_verification" {
		t.Fatalf("status = %v, want needs_verification before feedback; body=%s", baseDecision["status"], base.Body.String())
	}

	refined := call(t, h, http.MethodPost, "/v1/refine", refineRequest(pol, []Feedback{
		{CandidateID: "fixture/archived", Reason: policy.FeedbackArchivedProject},
	}, candidate("fixture/archived", model.ReuseDependency)))

	requireStatus(t, refined, http.StatusOK)
	decision := decodeDecision(t, refined)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", decision["status"], refined.Body.String())
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "build_locally" {
		t.Errorf("outcome = %v, want build_locally", resolution["outcome"])
	}
	rejected := rejectedText(resolution)
	if !strings.Contains(rejected, "user_feedback:archived_project") {
		t.Errorf("negative knowledge missing: %s", rejected)
	}
}

// TestRefineIsDeterministic runs the identical refinement twice and requires
// the same decision both times; only the storage identity and timestamp move.
func TestRefineIsDeterministic(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	candidates := []CandidateRef{
		candidate("fixture/eligible", model.ReuseDependency),
		candidate("fixture/light", model.ReuseDependency),
	}
	feedback := []Feedback{{CandidateID: "fixture/eligible", Reason: policy.FeedbackNotQuite}}
	body := refineRequest(apiPolicy(), feedback, candidates...)

	first := decodeDecision(t, call(t, h, http.MethodPost, "/v1/refine", body))
	second := decodeDecision(t, call(t, h, http.MethodPost, "/v1/refine", body))

	delete(first, "resolution_id")
	delete(second, "resolution_id")
	firstResolution := first["resolution"].(map[string]any)
	secondResolution := second["resolution"].(map[string]any)
	delete(firstResolution, "resolved_at")
	delete(secondResolution, "resolved_at")

	firstJSON := mustJSON(t, first)
	secondJSON := mustJSON(t, second)
	if firstJSON != secondJSON {
		t.Errorf("refinement is not deterministic\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

// TestRefineUnknownCandidateIs422 covers the documented feedback vocabulary.
func TestRefineUnknownCandidateIs422(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodPost, "/v1/refine", refineRequest(apiPolicy(), []Feedback{
		{CandidateID: "fixture/not-here", Reason: policy.FeedbackNotQuite},
	}, candidate("fixture/eligible", model.ReuseDependency)))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefineUnsupportedReasonIs422(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodPost, "/v1/refine", refineRequest(apiPolicy(), []Feedback{
		{CandidateID: "fixture/eligible", Reason: policy.FeedbackReason("prefer_stdlib")},
	}, candidate("fixture/eligible", model.ReuseDependency)))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func rejectedText(resolution map[string]any) string {
	var out strings.Builder
	for _, raw := range asAnySlice(resolution["rejected"]) {
		entry := raw.(map[string]any)
		out.WriteString(entry["specimen_id"].(string))
		out.WriteString(": ")
		out.WriteString(joinAny(entry["reasons"].([]any)))
		out.WriteString("\n")
	}
	return out.String()
}

func intPtr(value int) *int { return &value }

func asAnySlice(value any) []any {
	if value == nil {
		return nil
	}
	return value.([]any)
}
