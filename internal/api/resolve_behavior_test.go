package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

func decodeDecision(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode decision: %v; body=%s", err, rec.Body.String())
	}
	return out
}

func requireNull(t *testing.T, body map[string]any, field string) {
	t.Helper()
	value, ok := body[field]
	if !ok {
		t.Fatalf("field %q is missing from the response", field)
	}
	if value != nil {
		t.Errorf("%s = %v, want null", field, value)
	}
}

func TestResolveImplementationEligibleIsResolved(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	body := resolveRequest(apiPolicy(), candidate("fixture/eligible", model.ReuseDependency))

	rec := call(t, h, http.MethodPost, "/v1/resolve", body)

	requireStatus(t, rec, http.StatusOK)
	decision := decodeDecision(t, rec)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", decision["status"], rec.Body.String())
	}
	if decision["resolution_id"] == nil {
		t.Fatal("resolution_id is null, want a persisted identity")
	}
	if decision["policy_id"] != "api/test-v1" {
		t.Errorf("policy_id = %v, want api/test-v1", decision["policy_id"])
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "depend" {
		t.Errorf("outcome = %v, want depend", resolution["outcome"])
	}
	if resolution["specimen_id"] != "fixture/eligible" {
		t.Errorf("specimen_id = %v", resolution["specimen_id"])
	}
	if resolution["policy_id"] != "api/test-v1" {
		t.Errorf("resolution policy_id = %v", resolution["policy_id"])
	}
	if repo.count() != 1 {
		t.Errorf("persisted resolutions = %d, want exactly 1", repo.count())
	}
	assertNoSecrets(t, rec)
}

// TestResolveMetadataCannotCreateBehaviouralPass is the API-level restatement
// of the standing rule: provider metadata can never satisfy a behavioural
// requirement.
func TestResolveMetadataCannotCreateBehaviouralPass(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)
	body := resolveRequest(apiPolicy(), candidate("fixture/metadata-only", model.ReuseDependency))

	rec := call(t, h, http.MethodPost, "/v1/resolve", body)

	requireStatus(t, rec, http.StatusOK)
	decision := decodeDecision(t, rec)
	if decision["status"] != "needs_verification" {
		t.Fatalf("status = %v, want needs_verification; body=%s", decision["status"], rec.Body.String())
	}
	requireNull(t, decision, "resolution_id")
	requireNull(t, decision, "resolution")

	assessments := decision["assessments"].([]any)
	first := assessments[0].(map[string]any)
	behaviour := first["behaviour"].(map[string]any)
	requirements := behaviour["requirements"].([]any)
	if len(requirements) != len(requirementIDs) {
		t.Fatalf("requirements = %d, want %d", len(requirements), len(requirementIDs))
	}
	for _, raw := range requirements {
		requirement := raw.(map[string]any)
		if requirement["status"] != "unknown" {
			t.Errorf("requirement %v status = %v, want unknown", requirement["requirement_id"], requirement["status"])
		}
	}
	if repo.count() != 0 {
		t.Errorf("persisted resolutions = %d, want 0", repo.count())
	}
}

func TestResolveNeedsVerificationPersistsNothing(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(apiPolicy(), candidate("fixture/metadata-only", model.ReuseDependency)))

	requireStatus(t, rec, http.StatusOK)
	decision := decodeDecision(t, rec)
	if decision["status"] != "needs_verification" {
		t.Fatalf("status = %v, want needs_verification", decision["status"])
	}
	requireNull(t, decision, "resolution_id")
	requireNull(t, decision, "resolution")
	if repo.count() != 0 {
		t.Errorf("persisted resolutions = %d, want 0", repo.count())
	}
	if !strings.Contains(rec.Body.String(), `"shortlist":[`) {
		t.Errorf("shortlist was not serialised as []: %s", rec.Body.String())
	}
}

func TestResolveReferenceOnlyResolvesAsReference(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(apiPolicy(), candidate("fixture/reference", model.ReuseReference)))

	requireStatus(t, rec, http.StatusOK)
	decision := decodeDecision(t, rec)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved; body=%s", decision["status"], rec.Body.String())
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "reference" {
		t.Fatalf("outcome = %v, want reference", resolution["outcome"])
	}

	reasons := joinAny(resolution["reasons"].([]any))
	if !strings.Contains(reasons, "selected as reference-only engineering knowledge") {
		t.Errorf("reasons missing the reference-only wording: %s", reasons)
	}
	if !strings.Contains(reasons, "behavioural contract satisfaction is not established") {
		t.Errorf("reasons claim contract satisfaction: %s", reasons)
	}
	for _, forbidden := range []string{"recommended", "verified", "approved"} {
		if strings.Contains(reasons, forbidden) {
			t.Errorf("REFERENCE wording used %q: %s", forbidden, reasons)
		}
	}

	unknowns := joinAny(resolution["unknowns"].([]any))
	for _, requirementID := range requirementIDs {
		if !strings.Contains(unknowns, requirementID) {
			t.Errorf("unknowns missing %q: %s", requirementID, unknowns)
		}
	}
	if repo.count() != 1 {
		t.Errorf("persisted resolutions = %d, want 1", repo.count())
	}
}

func TestResolveAllBlockedBuildsLocally(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	pol := apiPolicy()
	pol.Reuse = PolicyReuse{Allowed: []model.ReuseMode{model.ReuseCopy}, Preferred: []model.ReuseMode{}}

	rec := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(pol, candidate("fixture/eligible", model.ReuseDependency)))

	requireStatus(t, rec, http.StatusOK)
	decision := decodeDecision(t, rec)
	if decision["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved", decision["status"])
	}
	resolution := decision["resolution"].(map[string]any)
	if resolution["outcome"] != "build_locally" {
		t.Errorf("outcome = %v, want build_locally", resolution["outcome"])
	}
	reasons := joinAny(resolution["reasons"].([]any))
	if !strings.Contains(reasons, "denied by policy") {
		t.Errorf("build-locally reasons are not specific: %s", reasons)
	}
	if repo.count() != 1 {
		t.Errorf("persisted resolutions = %d, want 1", repo.count())
	}
}

func TestResolveMissingPrimitiveIs404(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	body, err := json.Marshal(map[string]any{
		"primitive_id": "does/not/exist",
		"contract_id":  contractID,
		"candidates":   []CandidateRef{candidate("fixture/eligible", model.ReuseDependency)},
		"policy":       apiPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := call(t, h, http.MethodPost, "/v1/resolve", string(body))

	requireStatus(t, rec, http.StatusNotFound)
	requireCode(t, rec, CodeNotFound)
}

// TestResolveRejectsFeedback proves an initial resolve request carries none.
func TestResolveRejectsFeedback(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	body := resolveRequest(apiPolicy(), candidate("fixture/eligible", model.ReuseDependency))
	body = strings.TrimSuffix(body, "}") + `,"feedback":[{"candidate_id":"fixture/eligible","reason":"not_quite"}]}`

	rec := call(t, h, http.MethodPost, "/v1/resolve", body)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for feedback on an initial resolve; body=%s", rec.Code, rec.Body.String())
	}
}

func TestResolveRequestValidationIs422(t *testing.T) {
	cases := map[string]string{
		"duplicate candidate": resolveRequest(apiPolicy(),
			candidate("fixture/eligible", model.ReuseDependency),
			candidate("fixture/eligible", model.ReuseDependency)),
		"undeclared reuse mode": resolveRequest(apiPolicy(),
			candidate("fixture/eligible", model.ReuseCopy)),
		"unknown policy action": func() string {
			pol := apiPolicy()
			pol.Licence.Unknown = policy.Action("maybe")
			return resolveRequest(pol, candidate("fixture/eligible", model.ReuseDependency))
		}(),
		"wrong schema version": func() string {
			pol := apiPolicy()
			pol.SchemaVersion = 9
			return resolveRequest(pol, candidate("fixture/eligible", model.ReuseDependency))
		}(),
		"empty policy id": func() string {
			pol := apiPolicy()
			pol.ID = ""
			return resolveRequest(pol, candidate("fixture/eligible", model.ReuseDependency))
		}(),
		"selection out of range": func() string {
			pol := apiPolicy()
			pol.Selection.MaxOptions = 99
			return resolveRequest(pol, candidate("fixture/eligible", model.ReuseDependency))
		}(),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			repo := seedRepository()
			h := resolveHandler(t, repo)

			rec := call(t, h, http.MethodPost, "/v1/resolve", body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			if repo.count() != 0 {
				t.Errorf("persisted resolutions = %d, want 0", repo.count())
			}
		})
	}
}

func joinAny(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.(string))
	}
	return strings.Join(parts, "\n")
}
