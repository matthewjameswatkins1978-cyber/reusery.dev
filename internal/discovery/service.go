package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrProviderUnavailable reports that a profile names a provider this process
// did not construct. It is an execution failure, not a profile problem.
var ErrProviderUnavailable = errors.New("discovery: provider was not constructed")

// ProvidersFailedError reports that every configured provider failed
// operationally. The accompanying Result still carries the provider reports so
// the failure stays inspectable.
type ProvidersFailedError struct {
	Providers []string
}

func (e *ProvidersFailedError) Error() string {
	return "discovery: all providers failed operationally: " + strings.Join(e.Providers, ", ")
}

// Service runs bounded public discovery through injected providers and
// persists what it finds. It depends on neither PostgreSQL nor the CLI: both
// arrive as interfaces and constructors.
type Service struct {
	store     Store
	providers map[string]Provider
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
	byID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		byID[provider.ID()] = provider
	}
	return &Service{store: store, providers: byID, clock: clock, budget: budget}
}

// Discover executes the profile in provider order and persists the plausible
// candidates it finds.
//
// It never resolves, never ranks and never feeds provider order to the
// resolver kernel: discovery produces specimens and INFO/UNKNOWN observations
// only. Operational provider failure never creates a specimen, an observation
// or a resolution for a candidate that was not found.
//
// If every provider fails operationally the Result still carries the provider
// reports and a ProvidersFailedError is returned. If providers succeed but
// find nothing, that is a successful empty discovery.
func (s *Service) Discover(ctx context.Context, profile Profile) (Result, error) {
	if err := profile.Validate(); err != nil {
		return Result{}, err
	}

	primitive, err := s.store.GetPrimitive(ctx, profile.PrimitiveID)
	if err != nil {
		return Result{}, fmt.Errorf("load primitive %q: %w", profile.PrimitiveID, err)
	}
	contract, err := s.store.GetContract(ctx, profile.ContractID)
	if err != nil {
		return Result{}, fmt.Errorf("load contract %q: %w", profile.ContractID, err)
	}
	if err := ValidateProfileDomain(profile, primitive, contract); err != nil {
		return Result{}, err
	}

	observedAt := s.clock().UTC()
	result := Result{
		PrimitiveID: primitive.ID,
		ContractID:  contract.ID,
		ObservedAt:  observedAt,
		Candidates:  []Candidate{},
		Providers:   []ProviderReport{},
	}

	collected := NewCandidateSet()
	providerOrder := make([]string, 0, len(profile.Providers))
	failed := make([]string, 0, len(profile.Providers))

	for _, plan := range profile.Providers {
		provider, ok := s.providers[plan.ID]
		if !ok {
			return Result{}, fmt.Errorf("%w: %q", ErrProviderUnavailable, plan.ID)
		}
		providerOrder = append(providerOrder, plan.ID)

		runCtx, cancel := context.WithTimeout(ctx, s.budget.Timeout)
		res, runErr := provider.Discover(runCtx, ProviderRequest{
			Primitive:  primitive,
			Contract:   contract,
			Queries:    plan.Queries,
			Budget:     s.budget,
			ObservedAt: observedAt,
		})
		cancel()

		if runErr != nil {
			failed = append(failed, plan.ID)
			issues := res.Issues
			if len(issues) == 0 {
				issues = []ProviderIssue{IssueFromError(plan.ID, runErr, "")}
			}
			result.Providers = append(result.Providers, ProviderReport{
				ID:         plan.ID,
				Succeeded:  false,
				Requests:   res.Requests,
				Incomplete: res.Incomplete,
				Issues:     issues,
			})
			continue
		}

		for _, candidate := range res.Candidates {
			if err := collected.Add(candidate); err != nil {
				return Result{}, err
			}
		}
		result.Providers = append(result.Providers, ProviderReport{
			ID:         plan.ID,
			Succeeded:  true,
			Requests:   res.Requests,
			Incomplete: res.Incomplete,
			Issues:     res.Issues,
		})
	}

	discovered := collected.List()
	final := boundCandidates(discovered, providerOrder, s.budget.MaxCandidates)
	applyReportCounts(&result.Providers, discovered, final, s.budget.MaxCandidates)

	for _, candidate := range final {
		if err := ValidateCandidate(candidate, primitive.ID); err != nil {
			return Result{}, err
		}
	}
	result.Candidates = final

	if len(failed) == len(profile.Providers) {
		return result, &ProvidersFailedError{Providers: failed}
	}
	if err := Persist(ctx, s.store, final); err != nil {
		return Result{}, err
	}
	return result, nil
}

// boundCandidates applies the run-wide candidate budget and returns the
// deterministic output order: profile provider order, then stable specimen ID.
// This order is traceability, not quality: it is deliberately unrelated to
// provider relevance and is never handed to the resolver kernel.
func boundCandidates(discovered []Candidate, providerOrder []string, max int) []Candidate {
	byProvider := make(map[string][]Candidate, len(providerOrder))
	for _, candidate := range discovered {
		byProvider[candidate.ProviderID] = append(byProvider[candidate.ProviderID], candidate)
	}

	ordered := make([]Candidate, 0, len(discovered))
	for _, providerID := range providerOrder {
		candidates := byProvider[providerID]
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Specimen.ID < candidates[j].Specimen.ID
		})
		ordered = append(ordered, candidates...)
	}

	if len(ordered) > max {
		ordered = ordered[:max]
	}
	return ordered
}

// applyReportCounts reconciles each provider report with what actually made it
// into the bounded result, including candidates dropped by the run budget.
func applyReportCounts(reports *[]ProviderReport, discovered, final []Candidate, max int) {
	discoveredBy := make(map[string]int, len(discovered))
	for _, candidate := range discovered {
		discoveredBy[candidate.ProviderID]++
	}
	returnedBy := make(map[string]int, len(final))
	for _, candidate := range final {
		returnedBy[candidate.ProviderID]++
	}

	for i := range *reports {
		report := &(*reports)[i]
		report.CandidateCount = returnedBy[report.ID]
		if dropped := discoveredBy[report.ID] - returnedBy[report.ID]; dropped > 0 {
			report.Incomplete = true
			report.Issues = append(report.Issues, ProviderIssue{
				Kind:     IssueBudgetExhausted,
				Provider: report.ID,
				Message: fmt.Sprintf("run budget of %d candidates reached; %d candidate(s) discovered by this provider were not returned",
					max, dropped),
			})
		}
	}
}
