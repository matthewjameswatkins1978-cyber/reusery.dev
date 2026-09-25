package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var serviceClock = func() time.Time {
	return time.Date(2026, time.September, 25, 9, 30, 0, 0, time.UTC)
}

// fakeProvider is a deterministic provider double: no network, no clock drift.
type fakeProvider struct {
	id      string
	result  ProviderResult
	err     error
	delay   time.Duration
	calls   int
	request ProviderRequest
}

func (f *fakeProvider) ID() string { return f.id }

func (f *fakeProvider) Discover(ctx context.Context, request ProviderRequest) (ProviderResult, error) {
	f.calls++
	f.request = request
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ProviderResult{}, ctx.Err()
		}
	}
	return f.result, f.err
}

func fakeCandidate(providerID, specimenID string, observedAt time.Time, queries ...string) Candidate {
	source := model.SourceRef{URL: "https://example.test/" + specimenID, Path: specimenID}
	specimen := model.Specimen{
		ID:          specimenID,
		PrimitiveID: "process/bounded-subprocess",
		Name:        specimenID,
		Source:      source,
		ReuseMode:   []model.ReuseMode{model.ReuseReference},
	}
	evidence := make([]model.Evidence, 0, len(queries))
	for _, query := range queries {
		evidence = append(evidence, NewObservation(ObservationSpec{
			ProviderID:  providerID,
			SubjectID:   specimenID,
			Kind:        "discovery_match",
			Claim:       "test provider matched " + specimenID,
			Result:      model.EvidenceInfo,
			Source:      source,
			ObservedAt:  observedAt,
			Methodology: "unit test discovery",
			Artifact:    "query=" + query,
		}))
	}
	return Candidate{ProviderID: providerID, Specimen: specimen, Evidence: evidence}
}

func testProfile(providerIDs ...string) Profile {
	profile := Profile{
		SchemaVersion: SchemaVersion,
		PrimitiveID:   "process/bounded-subprocess",
		ContractID:    "process/bounded-subprocess/v1",
	}
	for _, id := range providerIDs {
		profile.Providers = append(profile.Providers, ProviderPlan{
			ID: id,
			Queries: []Query{
				{Text: "first query", Limit: 4},
				{Text: "second query", Limit: 4},
			},
		})
	}
	return profile
}

func TestServicePersistsDiscoveredCandidates(t *testing.T) {
	t.Run("pkg provider", func(t *testing.T) {
		provider := &fakeProvider{
			id: ProviderPkgGoDev,
			result: ProviderResult{Candidates: []Candidate{
				fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "subprocess cancellation"),
			}},
		}
		store := newSeededStore()
		service := NewService(store, serviceClock, []Provider{provider})

		result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(result.Candidates) != 1 {
			t.Fatalf("candidates = %d, want 1", len(result.Candidates))
		}
		if _, ok := store.specimens["public/pkg.go.dev/example/pkg@v1.0.0"]; !ok {
			t.Error("discovered specimen was not persisted")
		}
		if len(store.evidence) != 1 {
			t.Errorf("evidence = %d, want 1", len(store.evidence))
		}
		if !result.Providers[0].Succeeded {
			t.Error("provider report should show success")
		}
	})

	t.Run("github provider", func(t *testing.T) {
		provider := &fakeProvider{
			id: ProviderGitHubRepositories,
			result: ProviderResult{Candidates: []Candidate{
				fakeCandidate(ProviderGitHubRepositories, "public/github/repository/example/repo", serviceClock(), "subprocess language:go"),
			}},
		}
		store := newSeededStore()
		service := NewService(store, serviceClock, []Provider{provider})

		if _, err := service.Discover(t.Context(), testProfile(ProviderGitHubRepositories)); err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if _, ok := store.specimens["public/github/repository/example/repo"]; !ok {
			t.Error("discovered repository was not persisted")
		}
		if len(store.evidence) != 1 {
			t.Errorf("evidence = %d, want 1", len(store.evidence))
		}
	})
}

func TestServiceToleratesPartialProviderFailure(t *testing.T) {
	working := &fakeProvider{
		id: ProviderPkgGoDev,
		result: ProviderResult{Candidates: []Candidate{
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "subprocess cancellation"),
		}},
	}
	failing := &fakeProvider{
		id:  ProviderGitHubCode,
		err: errors.New("simulated outage"),
	}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{working, failing})

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev, ProviderGitHubCode))
	if err != nil {
		t.Fatalf("partial failure must still succeed: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("candidates = %d, want the successful provider's candidate", len(result.Candidates))
	}
	if len(store.specimens) != 1 {
		t.Errorf("persisted specimens = %d, want 1", len(store.specimens))
	}

	reports := map[string]ProviderReport{}
	for _, report := range result.Providers {
		reports[report.ID] = report
	}
	if !reports[ProviderPkgGoDev].Succeeded {
		t.Error("working provider reported as failed")
	}
	failed := reports[ProviderGitHubCode]
	if failed.Succeeded {
		t.Error("failing provider reported as succeeded")
	}
	if len(failed.Issues) == 0 {
		t.Fatal("failing provider has no inspectable issue")
	}
	if failed.Issues[0].Kind != IssueUnavailable || failed.Issues[0].Provider != ProviderGitHubCode {
		t.Errorf("issue = %#v, want an unavailable issue attributed to the provider", failed.Issues[0])
	}
}

func TestServiceReportsWhenEveryProviderFails(t *testing.T) {
	first := &fakeProvider{id: ProviderPkgGoDev, err: errors.New("boom")}
	second := &fakeProvider{id: ProviderGitHubRepositories, err: errors.New("boom")}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{first, second})

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev, ProviderGitHubRepositories))
	if err == nil {
		t.Fatal("Discover succeeded, want an execution failure")
	}
	var failed *ProvidersFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("error = %v, want ProvidersFailedError", err)
	}
	if len(failed.Providers) != 2 {
		t.Errorf("failed providers = %v, want both", failed.Providers)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("provider reports = %d, want both to stay inspectable", len(result.Providers))
	}
	for _, report := range result.Providers {
		if report.Succeeded || len(report.Issues) == 0 {
			t.Errorf("report = %#v, want a failure with issues", report)
		}
	}
	if len(store.specimens) != 0 || len(store.evidence) != 0 {
		t.Error("nothing may be persisted when no provider found anything")
	}
}

func TestServiceEmptyDiscoveryIsSuccess(t *testing.T) {
	provider := &fakeProvider{id: ProviderPkgGoDev, result: ProviderResult{Candidates: []Candidate{}}}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{provider})

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if err != nil {
		t.Fatalf("empty discovery must succeed: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("candidates = %d, want 0", len(result.Candidates))
	}
	if !result.Providers[0].Succeeded {
		t.Error("provider should report success")
	}
}

func TestServiceMergesDuplicateSpecimens(t *testing.T) {
	provider := &fakeProvider{
		id: ProviderPkgGoDev,
		result: ProviderResult{Candidates: []Candidate{
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "first query"),
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "second query"),
		}},
	}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{provider})

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %d, want one merged candidate", len(result.Candidates))
	}
	if len(result.Candidates[0].Evidence) != 2 {
		t.Errorf("merged evidence = %d, want 2 distinct observations", len(result.Candidates[0].Evidence))
	}
	if len(store.evidence) != 2 {
		t.Errorf("persisted evidence = %d, want 2", len(store.evidence))
	}
}

func TestServiceRejectsConflictingSpecimenIdentity(t *testing.T) {
	first := fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "first query")
	second := first
	second.Specimen.Source = model.SourceRef{URL: "https://elsewhere.test/tampered"}
	provider := &fakeProvider{id: ProviderPkgGoDev, result: ProviderResult{Candidates: []Candidate{first, second}}}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{provider})

	_, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if !errors.Is(err, ErrUnsafeOutput) {
		t.Fatalf("error = %v, want ErrUnsafeOutput for conflicting identity", err)
	}
	if len(store.specimens) != 0 {
		t.Error("conflicting candidates must not be persisted")
	}
}

func TestServiceOrdersCandidatesDeterministically(t *testing.T) {
	observed := serviceClock()
	packageProvider := &fakeProvider{
		id: ProviderPkgGoDev,
		result: ProviderResult{Candidates: []Candidate{
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/z/z@v1.0.0", observed, "q"),
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/a/a@v1.0.0", observed, "q"),
		}},
	}
	repoProvider := &fakeProvider{
		id: ProviderGitHubRepositories,
		result: ProviderResult{Candidates: []Candidate{
			fakeCandidate(ProviderGitHubRepositories, "public/github/repository/b/b", observed, "q"),
			fakeCandidate(ProviderGitHubRepositories, "public/github/repository/a/a", observed, "q"),
		}},
	}

	ids := func() []string {
		store := newSeededStore()
		service := NewService(store, serviceClock, []Provider{packageProvider, repoProvider})
		result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev, ProviderGitHubRepositories))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		out := make([]string, 0, len(result.Candidates))
		for _, candidate := range result.Candidates {
			out = append(out, candidate.Specimen.ID)
		}
		return out
	}

	first := ids()
	second := ids()
	if len(first) != 4 {
		t.Fatalf("candidates = %d, want 4", len(first))
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("ordering is not deterministic: %v vs %v", first, second)
	}

	want := []string{
		"public/pkg.go.dev/a/a@v1.0.0",
		"public/pkg.go.dev/z/z@v1.0.0",
		"public/github/repository/a/a",
		"public/github/repository/b/b",
	}
	if fmt.Sprint(first) != fmt.Sprint(want) {
		t.Errorf("order = %v, want profile provider order then specimen ID: %v", first, want)
	}
	if !sort.StringsAreSorted(first[:2]) {
		t.Error("candidates within one provider must be sorted by specimen ID")
	}
}

func TestServiceRejectsStructurallyInvalidProfiles(t *testing.T) {
	store := newSeededStore()
	service := NewService(store, serviceClock, nil)

	t.Run("unknown provider id", func(t *testing.T) {
		_, err := service.Discover(t.Context(), testProfile("sourcegraph"))
		if !errors.Is(err, ErrProfile) {
			t.Errorf("error = %v, want a structural ErrProfile", err)
		}
	})

	t.Run("provider not constructed", func(t *testing.T) {
		_, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Errorf("error = %v, want ErrProviderUnavailable", err)
		}
	})

	t.Run("primitive missing from registry", func(t *testing.T) {
		profile := testProfile(ProviderPkgGoDev)
		profile.PrimitiveID = "unknown/primitive"
		_, err := service.Discover(t.Context(), profile)
		if err == nil {
			t.Fatal("Discover succeeded, want a load failure")
		}
	})
}

func TestServiceBoundsCandidatesAndRequests(t *testing.T) {
	observed := serviceClock()
	candidates := make([]Candidate, 0, 30)
	for i := 0; i < 30; i++ {
		candidates = append(candidates, fakeCandidate(
			ProviderPkgGoDev, fmt.Sprintf("public/pkg.go.dev/example/pkg%02d@v1.0.0", i), observed, "q"))
	}
	provider := &fakeProvider{id: ProviderPkgGoDev, result: ProviderResult{Candidates: candidates}}
	store := newSeededStore()
	budget := DefaultBudget()
	service := NewServiceWithBudget(store, serviceClock, []Provider{provider}, budget)

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Candidates) != budget.MaxCandidates {
		t.Errorf("candidates = %d, want the budget ceiling of %d", len(result.Candidates), budget.MaxCandidates)
	}
	if len(store.specimens) != budget.MaxCandidates {
		t.Errorf("persisted = %d, want %d", len(store.specimens), budget.MaxCandidates)
	}

	report := result.Providers[0]
	if !report.Incomplete {
		t.Error("report must be marked incomplete when the candidate budget truncates it")
	}
	if len(report.Issues) == 0 || report.Issues[0].Kind != IssueBudgetExhausted {
		t.Errorf("issues = %#v, want a budget_exhausted issue", report.Issues)
	}

	if provider.request.Budget.MaxHTTPRequests != budget.MaxHTTPRequests {
		t.Errorf("provider received MaxHTTPRequests=%d, want %d", provider.request.Budget.MaxHTTPRequests, budget.MaxHTTPRequests)
	}
	if provider.request.Queries[0].Text != "first query" {
		t.Errorf("provider received queries %#v", provider.request.Queries)
	}
}

func TestServiceClassifiesProviderTimeout(t *testing.T) {
	provider := &fakeProvider{id: ProviderPkgGoDev, delay: time.Second}
	store := newSeededStore()
	budget := DefaultBudget()
	budget.Timeout = 20 * time.Millisecond
	service := NewServiceWithBudget(store, serviceClock, []Provider{provider}, budget)

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if err == nil {
		t.Fatal("Discover succeeded, want a failure")
	}
	var failed *ProvidersFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("error = %v, want ProvidersFailedError", err)
	}
	report := result.Providers[0]
	if report.Succeeded {
		t.Error("timed-out provider must not report success")
	}
	if len(report.Issues) == 0 || report.Issues[0].Kind != IssueTimeout {
		t.Errorf("issues = %#v, want a timeout issue", report.Issues)
	}
	if len(store.specimens) != 0 {
		t.Error("a timed-out provider must not create candidates")
	}
}

func TestServiceRejectsBehaviouralEvidence(t *testing.T) {
	tests := []struct {
		name       string
		evidence   func(string, time.Time) []model.Evidence
		wantDetail string
	}{
		{
			name: "provider attempts pass",
			evidence: func(subject string, at time.Time) []model.Evidence {
				return []model.Evidence{NewObservation(ObservationSpec{
					ProviderID:  ProviderGitHubCode,
					SubjectID:   subject,
					Kind:        "supports-cancellation",
					Claim:       "provider claims cancellation works",
					Result:      model.EvidencePass,
					Source:      model.SourceRef{URL: "https://example.test/x"},
					ObservedAt:  at,
					Methodology: "unit test discovery",
					Artifact:    "query=q",
				})}
			},
			wantDetail: "info or unknown",
		},
		{
			name: "provider aims evidence at a requirement",
			evidence: func(subject string, at time.Time) []model.Evidence {
				base := NewObservation(ObservationSpec{
					ProviderID:  ProviderGitHubCode,
					SubjectID:   subject,
					Kind:        "discovery_match",
					Claim:       "matched",
					Result:      model.EvidenceInfo,
					Source:      model.SourceRef{URL: "https://example.test/x"},
					ObservedAt:  at,
					Methodology: "unit test discovery",
					Artifact:    "query=q",
				})
				base.AppliesTo = "supports-cancellation"
				base.ID = EvidenceID(ProviderGitHubCode, base)
				return []model.Evidence{base}
			},
			wantDetail: "never maps to a contract requirement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specimenID := "public/github/code/example/repo@abc:cmd%2Fmain.go"
			source := model.SourceRef{URL: "https://example.test/x", Revision: "abc", Path: "cmd/main.go"}
			provider := &fakeProvider{
				id: ProviderGitHubCode,
				result: ProviderResult{Candidates: []Candidate{{
					ProviderID: ProviderGitHubCode,
					Specimen: model.Specimen{
						ID:          specimenID,
						PrimitiveID: "process/bounded-subprocess",
						Name:        "cmd/main.go",
						Source:      source,
						ReuseMode:   []model.ReuseMode{model.ReuseReference},
					},
					Evidence: tt.evidence(specimenID, serviceClock()),
				}}},
			}
			store := newSeededStore()
			service := NewService(store, serviceClock, []Provider{provider})

			_, err := service.Discover(t.Context(), testProfile(ProviderGitHubCode))
			if !errors.Is(err, ErrUnsafeOutput) {
				t.Fatalf("error = %v, want ErrUnsafeOutput", err)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantDetail)
			}
			if len(store.specimens) != 0 || len(store.evidence) != 0 {
				t.Error("unsafe provider output must not be persisted")
			}
		})
	}
}

func TestServiceFailsLoudlyOnEvidenceConflict(t *testing.T) {
	candidate := fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/example/pkg@v1.0.0", serviceClock(), "q")
	store := newSeededStore()
	tampered := candidate.Evidence[0]
	tampered.Claim = "tampered after the digest was computed"
	store.evidence[tampered.ID] = tampered

	provider := &fakeProvider{id: ProviderPkgGoDev, result: ProviderResult{Candidates: []Candidate{candidate}}}
	service := NewService(store, serviceClock, []Provider{provider})

	_, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("error = %v, want ErrEvidenceConflict", err)
	}
}

func TestServiceAppliesOneClockToEveryObservation(t *testing.T) {
	provider := &fakeProvider{
		id: ProviderPkgGoDev,
		result: ProviderResult{Candidates: []Candidate{
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/a/a@v1.0.0", serviceClock(), "q1", "q2"),
			fakeCandidate(ProviderPkgGoDev, "public/pkg.go.dev/b/b@v1.0.0", serviceClock(), "q1"),
		}},
	}
	store := newSeededStore()
	service := NewService(store, serviceClock, []Provider{provider})

	result, err := service.Discover(t.Context(), testProfile(ProviderPkgGoDev))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !result.ObservedAt.Equal(serviceClock()) {
		t.Errorf("ObservedAt = %v, want %v", result.ObservedAt, serviceClock())
	}
	if provider.request.ObservedAt.Location() != time.UTC {
		t.Errorf("provider observed at %v, want UTC", provider.request.ObservedAt)
	}
	for id, evidence := range store.evidence {
		if !evidence.ObservedAt.Equal(serviceClock()) {
			t.Errorf("evidence %s observed at %v, want exactly %v", id, evidence.ObservedAt, serviceClock())
		}
	}
	if len(store.evidence) != 3 {
		t.Errorf("evidence = %d, want 3 observations sharing one run timestamp", len(store.evidence))
	}
}
