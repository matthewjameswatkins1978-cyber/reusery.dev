package enrichment

import (
	"context"
	"fmt"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Service runs bounded enrichment through injected providers and persists what
// it observes. It depends on neither PostgreSQL nor the CLI: both arrive as
// interfaces and constructors.
type Service struct {
	store     Store
	providers []Provider
	clock     Clock
	budget    Budget
}

// NewService builds a service with the packet's default budget.
func NewService(store Store, clock Clock, providers []Provider) *Service {
	return NewServiceWithBudget(store, clock, providers, DefaultBudget())
}

// NewServiceWithBudget builds a service with an explicit budget. Tests use it
// to prove providers stop when their budget is exhausted.
func NewServiceWithBudget(store Store, clock Clock, providers []Provider, budget Budget) *Service {
	filtered := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &Service{store: store, providers: filtered, clock: clock, budget: budget}
}

// Enrich loads the named specimens, runs every applicable provider over each
// of them and persists the new observations.
//
// It never resolves, never ranks and never selects a candidate. One provider
// failing never erases another provider's evidence; only a run in which every
// applicable provider call failed operationally is an execution failure.
func (s *Service) Enrich(ctx context.Context, specimenIDs []string) (Result, error) {
	if err := validateSpecimenList(specimenIDs, s.budget.MaxSpecimens); err != nil {
		return Result{}, err
	}

	observedAt := s.clock().UTC()
	result := Result{
		ObservedAt: observedAt,
		Evidence:   []model.Evidence{},
		Specimens:  make([]SpecimenReport, 0, len(specimenIDs)),
	}

	runCtx, cancel := context.WithTimeout(ctx, s.budget.RunTimeout)
	defer cancel()

	providerCalls := 0
	var failedProviders []string
	succeeded := 0

	for _, specimenID := range specimenIDs {
		specimen, err := s.store.GetSpecimen(runCtx, specimenID)
		if err != nil {
			return Result{}, fmt.Errorf("load specimen %q: %w", specimenID, err)
		}
		existing, err := s.store.ListEvidenceBySubject(runCtx, specimenID)
		if err != nil {
			return Result{}, fmt.Errorf("load evidence for %q: %w", specimenID, err)
		}

		report := SpecimenReport{
			SpecimenID: specimenID,
			Providers:  []ProviderReport{},
		}
		applicable, overflow := s.applicable(specimen)
		if len(applicable) == 0 {
			report.Providers = append(report.Providers, ProviderReport{
				ID:        "enrichment",
				Succeeded: true,
				Issues: []ProviderIssue{{
					Kind:       IssueUnsupported,
					Provider:   "enrichment",
					SpecimenID: specimenID,
					Message: fmt.Sprintf("no enrichment provider supports specimen %q; no evidence was manufactured",
						specimenID),
				}},
			})
			result.Specimens = append(result.Specimens, report)
			continue
		}
		report.Supported = true
		if overflow > 0 {
			report.Providers = append(report.Providers, ProviderReport{
				ID:        "enrichment",
				Succeeded: true,
				Issues: []ProviderIssue{{
					Kind:       IssueBudgetExhausted,
					Provider:   "enrichment",
					SpecimenID: specimenID,
					Message: fmt.Sprintf("provider budget of %d reached; %d applicable provider(s) were not run",
						s.budget.MaxProviders, overflow),
				}},
			})
		}

		for _, provider := range applicable {
			providerCalls++

			providerCtx, providerCancel := context.WithTimeout(runCtx, s.budget.Timeout)
			providerResult, runErr := provider.Enrich(providerCtx, Request{
				Specimen:         specimen,
				ExistingEvidence: existing,
				ObservedAt:       observedAt,
				Budget:           s.budget,
			})
			providerCancel()

			providerReport := ProviderReport{
				ID:        provider.ID(),
				Succeeded: runErr == nil,
				Requests:  providerResult.Requests,
				Issues:    providerResult.Issues,
			}
			if runErr != nil {
				failedProviders = append(failedProviders, provider.ID()+":"+specimenID)
				if len(providerReport.Issues) == 0 {
					providerReport.Issues = []ProviderIssue{IssueFromError(provider.ID(), specimenID, runErr)}
				}
				report.Providers = append(report.Providers, providerReport)
				continue
			}
			succeeded++

			for _, observation := range providerResult.Evidence {
				if err := ValidateObservation(provider.ID(), specimenID, observation); err != nil {
					return Result{}, err
				}
			}
			providerReport.EvidenceCount = len(providerResult.Evidence)
			report.EvidenceCount += len(providerResult.Evidence)
			result.Evidence = append(result.Evidence, providerResult.Evidence...)
			report.Providers = append(report.Providers, providerReport)
		}

		result.Specimens = append(result.Specimens, report)
	}

	if providerCalls > 0 && succeeded == 0 {
		return result, &ProvidersFailedError{Providers: failedProviders}
	}
	if len(result.Evidence) > 0 {
		if err := Persist(ctx, s.store, result.Evidence); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// applicable returns the providers that support one specimen, capped at the
// per-specimen provider budget, plus how many were dropped.
func (s *Service) applicable(specimen model.Specimen) ([]Provider, int) {
	applicable := make([]Provider, 0, len(s.providers))
	for _, provider := range s.providers {
		if provider.Supports(specimen) {
			applicable = append(applicable, provider)
		}
	}
	if len(applicable) <= s.budget.MaxProviders {
		return applicable, 0
	}
	overflow := len(applicable) - s.budget.MaxProviders
	return applicable[:s.budget.MaxProviders], overflow
}

func validateSpecimenList(specimenIDs []string, max int) error {
	if len(specimenIDs) == 0 {
		return ErrNoSpecimens
	}
	if len(specimenIDs) > max {
		return fmt.Errorf("%w: %d supplied, maximum %d", ErrTooManySpecimens, len(specimenIDs), max)
	}
	seen := make(map[string]struct{}, len(specimenIDs))
	for _, id := range specimenIDs {
		if id == "" {
			return fmt.Errorf("%w: an empty specimen id was supplied", ErrNoSpecimens)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: %q", ErrDuplicateSpecimen, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
