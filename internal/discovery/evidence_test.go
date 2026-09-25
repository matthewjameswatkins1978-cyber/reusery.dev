package discovery

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var evidenceIDPattern = regexp.MustCompile(`^discovery/[^/]+/[0-9a-f]{64}$`)

var testObservedAt = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

func sampleObservation() ObservationSpec {
	return ObservationSpec{
		ProviderID:  ProviderPkgGoDev,
		SubjectID:   "public/pkg.go.dev/example/pkg@v1.0.0",
		Kind:        "package_synopsis",
		Claim:       `pkg.go.dev describes package "example/pkg" as "does a thing"`,
		Result:      model.EvidenceInfo,
		Source:      model.SourceRef{URL: "https://pkg.go.dev/example/pkg", Revision: "v1.0.0", Path: "example/pkg"},
		ObservedAt:  testObservedAt,
		Methodology: MethodologyPkgGoDev,
		Artifact:    "query=subprocess cancellation",
	}
}

func TestEvidenceIDIsStableAndDeterministic(t *testing.T) {
	first := NewObservation(sampleObservation())
	second := NewObservation(sampleObservation())

	if first.ID != second.ID {
		t.Fatalf("IDs differ for identical observations: %q vs %q", first.ID, second.ID)
	}
	if !evidenceIDPattern.MatchString(first.ID) {
		t.Errorf("ID = %q, want discovery/<provider>/<64 hex>", first.ID)
	}

	mutations := map[string]func(*ObservationSpec){
		"provider":    func(s *ObservationSpec) { s.ProviderID = ProviderGitHubCode },
		"subject":     func(s *ObservationSpec) { s.SubjectID = "other" },
		"kind":        func(s *ObservationSpec) { s.Kind = "package_module" },
		"claim":       func(s *ObservationSpec) { s.Claim = "different" },
		"result":      func(s *ObservationSpec) { s.Result = model.EvidenceUnknown },
		"url":         func(s *ObservationSpec) { s.Source.URL = "https://example.test" },
		"revision":    func(s *ObservationSpec) { s.Source.Revision = "v2.0.0" },
		"path":        func(s *ObservationSpec) { s.Source.Path = "other/path" },
		"license":     func(s *ObservationSpec) { s.Source.License = "MIT" },
		"methodology": func(s *ObservationSpec) { s.Methodology = "something else" },
		"artifact":    func(s *ObservationSpec) { s.Artifact = "query=other" },
		"time":        func(s *ObservationSpec) { s.ObservedAt = testObservedAt.Add(time.Second) },
	}
	for name, mutate := range mutations {
		t.Run("different "+name, func(t *testing.T) {
			spec := sampleObservation()
			mutate(&spec)
			if got := NewObservation(spec); got.ID == first.ID {
				t.Errorf("ID unchanged at %q after changing %s", got.ID, name)
			}
		})
	}
}

func TestNewObservationNeverTargetsARequirement(t *testing.T) {
	evidence := NewObservation(sampleObservation())
	if evidence.AppliesTo != "" {
		t.Errorf("AppliesTo = %q, want empty: discovery evidence never maps to a requirement", evidence.AppliesTo)
	}
	if evidence.ObservedAt.Location() != time.UTC {
		t.Errorf("ObservedAt = %v, want UTC", evidence.ObservedAt)
	}
	if evidence.Methodology != MethodologyPkgGoDev {
		t.Errorf("Methodology = %q", evidence.Methodology)
	}
}

func validCandidate() Candidate {
	specimen := model.Specimen{
		ID:          "public/pkg.go.dev/example/pkg@v1.0.0",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "example/pkg",
		Source:      model.SourceRef{URL: "https://pkg.go.dev/example/pkg", Revision: "v1.0.0", Path: "example/pkg"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	return Candidate{
		ProviderID: ProviderPkgGoDev,
		Specimen:   specimen,
		Evidence: []model.Evidence{
			NewObservation(ObservationSpec{
				ProviderID:  ProviderPkgGoDev,
				SubjectID:   specimen.ID,
				Kind:        "discovery_match",
				Claim:       "pkg.go.dev matched package \"example/pkg\"",
				Result:      model.EvidenceInfo,
				Source:      model.SourceRef{URL: specimen.Source.URL, Revision: specimen.Source.Revision, Path: specimen.Source.Path},
				ObservedAt:  testObservedAt,
				Methodology: MethodologyPkgGoDev,
				Artifact:    "query=subprocess cancellation",
			}),
		},
	}
}

func TestValidateCandidateAcceptsProviderOutput(t *testing.T) {
	if err := ValidateCandidate(validCandidate(), "process/bounded-subprocess"); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
}

func TestValidateCandidateRejectsUnsafeProviderOutput(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Candidate)
		primitive  string
		wantDetail string
	}{
		{
			name:   "no specimen id",
			mutate: func(c *Candidate) { c.Specimen.ID = "" },
		},
		{
			name:   "specimen targets another primitive",
			mutate: func(c *Candidate) { c.Specimen.PrimitiveID = "other/primitive" },
		},
		{
			name:   "specimen has no provenance",
			mutate: func(c *Candidate) { c.Specimen.Source = model.SourceRef{} },
		},
		{
			name:   "unsupported reuse mode",
			mutate: func(c *Candidate) { c.Specimen.ReuseMode = []model.ReuseMode{"borrow"} },
		},
		{
			name:   "no provider id",
			mutate: func(c *Candidate) { c.ProviderID = "" },
		},
		{
			name:       "evidence has no id",
			mutate:     func(c *Candidate) { c.Evidence[0].ID = "" },
			wantDetail: "no id",
		},
		{
			name:   "evidence subject mismatch",
			mutate: func(c *Candidate) { c.Evidence[0].SubjectID = "someone-else" },
		},
		{
			name:       "evidence claims pass",
			mutate:     func(c *Candidate) { c.Evidence[0].Result = model.EvidencePass },
			wantDetail: "info or unknown",
		},
		{
			name:       "evidence claims fail",
			mutate:     func(c *Candidate) { c.Evidence[0].Result = model.EvidenceFail },
			wantDetail: "info or unknown",
		},
		{
			name:       "evidence aims at a requirement",
			mutate:     func(c *Candidate) { c.Evidence[0].AppliesTo = "supports-cancellation" },
			wantDetail: "never maps to a contract requirement",
		},
		{
			name:   "evidence has no provenance",
			mutate: func(c *Candidate) { c.Evidence[0].Source = model.SourceRef{} },
		},
		{
			name:   "evidence has no observed_at",
			mutate: func(c *Candidate) { c.Evidence[0].ObservedAt = time.Time{} },
		},
		{
			name:   "evidence id is not deterministic",
			mutate: func(c *Candidate) { c.Evidence[0].ID = "discovery/pkg.go.dev/deadbeef" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := validCandidate()
			tt.mutate(&candidate)
			primitive := tt.primitive
			if primitive == "" {
				primitive = "process/bounded-subprocess"
			}

			err := ValidateCandidate(candidate, primitive)
			if !errors.Is(err, ErrUnsafeOutput) {
				t.Fatalf("error = %v, want ErrUnsafeOutput", err)
			}
			if tt.wantDetail != "" && !strings.Contains(err.Error(), tt.wantDetail) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantDetail)
			}
		})
	}
}
