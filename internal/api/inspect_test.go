package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// slashySpecimenID is a real-shaped Reusery id carrying / @ : and %.
const slashySpecimenID = "public/github/code/adhocteam/pushup@9ca7c88e3243264f9b446663e4523c1875950667:proc_attr_linux.go"

func queryID(value string) string { return "?id=" + url.QueryEscape(value) }

func TestGetPrimitiveWithSlashContainingID(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/primitives"+queryID(primitiveID), "")

	requireStatus(t, rec, http.StatusOK)
	body := jsonBody(t, rec)
	if body["id"] != primitiveID {
		t.Errorf("id = %v, want %q", body["id"], primitiveID)
	}
	if body["contract_id"] != contractID {
		t.Errorf("contract_id = %v", body["contract_id"])
	}
}

func TestGetContractWithSlashContainingID(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/contracts"+queryID(contractID), "")

	requireStatus(t, rec, http.StatusOK)
	body := jsonBody(t, rec)
	if body["id"] != contractID {
		t.Errorf("id = %v, want %q", body["id"], contractID)
	}
	requirements := body["requirements"].([]any)
	if len(requirements) != len(requirementIDs) {
		t.Errorf("requirements = %d, want %d", len(requirements), len(requirementIDs))
	}
}

func TestGetSpecimenWithOpaqueDomainID(t *testing.T) {
	repo := seedRepository()
	repo.specimens[slashySpecimenID] = model.Specimen{
		ID:          slashySpecimenID,
		PrimitiveID: primitiveID,
		Name:        "pushup proc_attr_linux",
		ReuseMode:   []model.ReuseMode{model.ReuseReference},
		Source:      model.SourceRef{URL: "https://example.invalid/pushup", Revision: "9ca7c88e3243264f"},
	}
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/specimens"+queryID(slashySpecimenID), "")

	requireStatus(t, rec, http.StatusOK)
	body := jsonBody(t, rec)
	if body["id"] != slashySpecimenID {
		t.Errorf("id = %v, want the full opaque id", body["id"])
	}
	modes := body["reuse_modes"].([]any)
	if len(modes) != 1 || modes[0] != "reference" {
		t.Errorf("reuse_modes = %v", modes)
	}
}

func TestInspectionMissingResourcesAre404(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	cases := map[string]string{
		"primitive":  "/v1/primitives" + queryID("does/not/exist"),
		"contract":   "/v1/contracts" + queryID("does/not/exist"),
		"specimen":   "/v1/specimens" + queryID("does/not/exist"),
		"resolution": "/v1/resolutions/404",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			rec := call(t, h, http.MethodGet, path, "")
			requireStatus(t, rec, http.StatusNotFound)
			requireCode(t, rec, CodeNotFound)
			assertNoSecrets(t, rec)
		})
	}
}

func TestInspectionMissingIDIs422(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/primitives", "")
	requireStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestInspectionUnknownQueryParameterIs422(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/primitives"+queryID(primitiveID)+"&root=/etc", "")
	requireStatus(t, rec, http.StatusUnprocessableEntity)
}

// TestGetResolutionReturnsStoredHistoryWithoutReEvaluation persists a decision
// and then reads it back through the inspection route.
func TestGetResolutionReturnsStoredHistoryWithoutReEvaluation(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	created := call(t, h, http.MethodPost, "/v1/resolve",
		resolveRequest(apiPolicy(), candidate("fixture/eligible", model.ReuseDependency)))
	requireStatus(t, created, http.StatusOK)
	decision := decodeDecision(t, created)
	id := int64(decision["resolution_id"].(float64))

	// Change the world: the stored decision must not follow it.
	delete(repo.specimens, "fixture/eligible")
	delete(repo.evidence, "fixture/eligible")

	rec := call(t, h, http.MethodGet, fmt.Sprintf("/v1/resolutions/%d", id), "")

	requireStatus(t, rec, http.StatusOK)
	body := jsonBody(t, rec)
	if body["resolution_id"] != float64(id) {
		t.Errorf("resolution_id = %v, want %d", body["resolution_id"], id)
	}
	resolution := body["resolution"].(map[string]any)
	if resolution["outcome"] != "depend" {
		t.Errorf("outcome = %v, want depend", resolution["outcome"])
	}
	if resolution["specimen_id"] != "fixture/eligible" {
		t.Errorf("specimen_id = %v", resolution["specimen_id"])
	}
	if resolution["policy_id"] != "api/test-v1" {
		t.Errorf("policy_id = %v", resolution["policy_id"])
	}
}

func TestGetResolutionZeroIs404(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/resolutions/0", "")
	if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 422 or 404; body=%s", rec.Code, rec.Body.String())
	}
}

// ------------------------------------------------------------ pagination

// seedPagedEvidence records n observations for one subject, one second apart.
func seedPagedEvidence(repo *memoryRepository, subject string, n int) {
	start := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		repo.evidence[subject] = append(repo.evidence[subject], model.Evidence{
			ID:         fmt.Sprintf("ev/%s/%03d", subject, i),
			SubjectID:  subject,
			Kind:       "discovery_match",
			Claim:      fmt.Sprintf("observation %d", i),
			Result:     model.EvidenceInfo,
			ObservedAt: start.Add(time.Duration(i) * time.Second),
		})
	}
}

func evidencePage(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	requireStatus(t, rec, http.StatusOK)
	return jsonBody(t, rec)
}

func TestEvidenceDefaultLimitIsFifty(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, slashySpecimenID, 60)
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/evidence?subject_id="+url.QueryEscape(slashySpecimenID), "")
	page := evidencePage(t, rec)

	items := page["evidence"].([]any)
	if len(items) != 50 {
		t.Errorf("evidence = %d items, want the default 50", len(items))
	}
	if page["limit"] != float64(50) {
		t.Errorf("limit = %v, want 50", page["limit"])
	}
	if page["next_cursor"] == nil {
		t.Error("next_cursor is null, want a cursor for a further page")
	}
	if page["subject_id"] != slashySpecimenID {
		t.Errorf("subject_id = %v", page["subject_id"])
	}
}

func TestEvidenceCursorPagesWithoutDuplicateOrSkip(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, slashySpecimenID, 7)
	h := resolveHandler(t, repo)

	seen := []string{}
	var cursor any = "PLACEHOLDER"
	pages := 0
	for cursor != nil {
		path := "/v1/evidence?subject_id=" + url.QueryEscape(slashySpecimenID) + "&limit=3"
		if cursorString, ok := cursor.(string); ok && cursorString != "" && cursorString != "PLACEHOLDER" {
			path += "&cursor=" + url.QueryEscape(cursorString)
		}
		rec := call(t, h, http.MethodGet, path, "")
		page := evidencePage(t, rec)
		for _, raw := range page["evidence"].([]any) {
			item := raw.(map[string]any)
			seen = append(seen, item["id"].(string))
		}
		cursor = page["next_cursor"]
		pages++
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != 7 {
		t.Fatalf("paged %d observations, want 7", len(seen))
	}
	seenSet := map[string]bool{}
	for _, id := range seen {
		if seenSet[id] {
			t.Fatalf("duplicate observation %q across pages", id)
		}
		seenSet[id] = true
	}
	for i := 0; i < 7; i++ {
		want := fmt.Sprintf("ev/%s/%03d", slashySpecimenID, i)
		if !seenSet[want] {
			t.Errorf("missing observation %q", want)
		}
	}
}

func TestEvidenceOrderByObservedAtThenID(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, "fixture/paged", 5)
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/evidence?subject_id=fixture/paged&limit=100", "")
	page := evidencePage(t, rec)
	items := page["evidence"].([]any)
	if len(items) != 5 {
		t.Fatalf("evidence = %d, want 5", len(items))
	}
	var last time.Time
	var lastID string
	for _, raw := range items {
		item := raw.(map[string]any)
		current, err := time.Parse(time.RFC3339Nano, item["observed_at"].(string))
		if err != nil {
			t.Fatalf("parse observed_at: %v", err)
		}
		id := item["id"].(string)
		if current.Before(last) {
			t.Fatalf("observed_at went backwards: %v after %v", current, last)
		}
		if current.Equal(last) && id <= lastID {
			t.Fatalf("ids out of order at equal timestamps: %q after %q", id, lastID)
		}
		last, lastID = current, id
	}
}

func TestEvidenceLimitBounds(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, "fixture/paged", 5)
	h := resolveHandler(t, repo)

	for _, test := range []struct {
		name  string
		query string
		want  int
	}{
		{"limit 1", "&limit=1", 1},
		{"limit 100", "&limit=100", 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := call(t, h, http.MethodGet, "/v1/evidence?subject_id=fixture/paged"+test.query, "")
			page := evidencePage(t, rec)
			items := page["evidence"].([]any)
			if len(items) != test.want {
				t.Errorf("evidence = %d, want %d", len(items), test.want)
			}
		})
	}

	t.Run("limit 101 rejected", func(t *testing.T) {
		rec := call(t, h, http.MethodGet, "/v1/evidence?subject_id=fixture/paged&limit=101", "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
	})
}

// TestEvidenceEmptyFinalPage covers a cursor that already points at the last
// observation: the API answers with an empty page and a null cursor rather
// than an error.
func TestEvidenceEmptyFinalPage(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, "fixture/paged", 3)
	h := resolveHandler(t, repo)

	last := orderedEvidence(repo.evidence["fixture/paged"])[2]
	cursor, err := encodeCursor(last.ObservedAt, last.ID)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	rec := call(t, h, http.MethodGet,
		"/v1/evidence?subject_id=fixture/paged&limit=3&cursor="+url.QueryEscape(cursor), "")
	page := evidencePage(t, rec)

	items := page["evidence"].([]any)
	if len(items) != 0 {
		t.Errorf("evidence = %d, want 0", len(items))
	}
	if page["next_cursor"] != nil {
		t.Errorf("next_cursor = %v, want null", page["next_cursor"])
	}
}

func TestEvidenceMalformedCursorIs422(t *testing.T) {
	repo := seedRepository()
	seedPagedEvidence(repo, "fixture/paged", 3)
	h := resolveHandler(t, repo)

	for _, cursor := range []string{"not-a-cursor", "!!!", strings.Repeat("a", 3000)} {
		rec := call(t, h, http.MethodGet,
			"/v1/evidence?subject_id=fixture/paged&cursor="+url.QueryEscape(cursor), "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("cursor %q: status = %d, want 422", truncate(cursor, 40), rec.Code)
		}
	}
}

func TestEvidenceCursorRoundTrip(t *testing.T) {
	observed := time.Date(2026, time.September, 1, 12, 30, 45, 0, time.UTC)
	encoded, err := encodeCursor(observed, "ev/example/001")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.ObservedAt.Equal(observed) || decoded.EvidenceID != "ev/example/001" {
		t.Errorf("round trip = %+v", decoded)
	}
	if decoded.Version != cursorVersion {
		t.Errorf("version = %d", decoded.Version)
	}
}

func TestEvidenceMissingSubjectIs422(t *testing.T) {
	repo := seedRepository()
	h := resolveHandler(t, repo)

	rec := call(t, h, http.MethodGet, "/v1/evidence", "")
	requireStatus(t, rec, http.StatusUnprocessableEntity)
}
