// Package app is the shared composition root for Reusery's application
// services.
//
// It owns the production factories that build the Packet 5 discovery service,
// the Packet 6 normaliser, the Packet 7 enrichment and quality services, and
// the read interfaces the HTTP boundary inspects. The CLI and the HTTP API
// both call these factories, so Packet 9's MCP adapter can call them too.
//
// It is deliberately not an application framework: no request types, no
// routing, no configuration loading, no lifecycle. Business logic stays in
// the packages it composes.
package app

import (
	"context"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/pkggodev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/depsdev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment/providers/githubmeta"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent/providers/openai"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// Store is the combined persistence surface the application services need.
//
// It carries the read methods the shared read-only helpers need and the two
// append-only methods Packet 9 added, so the CLI and the MCP server can share
// one store interface instead of widening each helper independently.
type Store interface {
	resolver.Repository
	catalog.Store
	// Inspector covers the read surface both transports inspect with.
	Inspector
	// ListPrimitives returns at most limit primitives in id order.
	ListPrimitives(ctx context.Context, limit int) ([]model.Primitive, error)
	// InsertFeedback appends one factual post-resolution event.
	InsertFeedback(ctx context.Context, f outcome.Feedback) (int64, error)
	// ListFeedback returns stored post-resolution events in chronological order.
	ListFeedback(ctx context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error)
}

// Normalizer turns raw engineering intent into a structured Packet 6 result.
type Normalizer interface {
	Normalize(context.Context, string) (intent.Result, error)
}

// Discoverer runs one bounded Packet 5 discovery profile.
type Discoverer interface {
	Discover(context.Context, discovery.Profile) (discovery.Result, error)
}

// Enricher runs one bounded Packet 7 enrichment pass over named specimens.
type Enricher interface {
	Enrich(context.Context, []string) (enrichment.Result, error)
}

// QualityResolver is the Packet 7 quality decision service. It is the only
// resolver the public service boundary exposes.
type QualityResolver interface {
	Choose(ctx context.Context, pol policy.Policy, request resolver.QualityRequest) (resolver.StoredQualityDecision, error)
	Resolution(ctx context.Context, id int64) (model.Resolution, error)
}

// Inspector is the minimal read surface an API client needs. It is satisfied
// by the PostgreSQL store but is declared here so no transport package has to
// know that storage exists.
type Inspector interface {
	GetPrimitive(context.Context, string) (model.Primitive, error)
	GetContract(context.Context, string) (model.Contract, error)
	GetSpecimen(context.Context, string) (model.Specimen, error)
	GetResolution(context.Context, int64) (model.Resolution, error)
	// ListEvidenceAfter returns at most limit observations for subjectID in
	// (observed_at, id) order, continuing strictly after the supplied key.
	// A zero after and empty afterID start at the beginning.
	ListEvidenceAfter(ctx context.Context, subjectID string, after time.Time, afterID string, limit int) ([]model.Evidence, error)
}

// ReadyChecker reports whether a dependency required for serving traffic is
// ready. Only PostgreSQL is registered; degradable providers never are.
type ReadyChecker func(ctx context.Context) error

// Catalog is the read surface capability discovery needs. It is deliberately
// separate from Inspector so neither the HTTP API nor the MCP server has to
// grow a method it does not use.
type Catalog interface {
	// ListPrimitives returns at most limit primitives in id order.
	ListPrimitives(ctx context.Context, limit int) ([]model.Primitive, error)
	GetPrimitive(context.Context, string) (model.Primitive, error)
	GetContract(context.Context, string) (model.Contract, error)
}

// OutcomeRecorder appends factual post-resolution events. Reporting an outcome
// never alters a Resolution, never creates Evidence and never re-resolves.
type OutcomeRecorder interface {
	Report(ctx context.Context, resolutionID int64, kind outcome.Kind, note string) (outcome.StoredFeedback, error)
	List(ctx context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error)
}

// NewOutcomeRecorder builds the append-only outcome log over the existing
// repository boundary.
func NewOutcomeRecorder(store Store, clock func() time.Time) OutcomeRecorder {
	return outcome.NewService(store, clock)
}

// NewNormalizer wires the single Packet 6 model adapter.
//
// The API key is required only here: serve, seed, discover, resolve and
// resolution never construct a normaliser, so the server starts without one.
func NewNormalizer(cfg config.ModelConfig) (Normalizer, error) {
	key, err := cfg.RequireAPIKey()
	if err != nil {
		return nil, err
	}
	provider, err := openai.New(key, cfg.Model())
	if err != nil {
		return nil, err
	}
	return intent.NewNormalizer(provider), nil
}

// NewDiscoverer wires the real Packet 5 providers. The GitHub token is
// optional: without it GitHub's public unauthenticated limits apply.
func NewDiscoverer(store Store, cfg config.Config, clock discovery.Clock) Discoverer {
	client := github.NewClient(cfg.GitHubToken)
	return discovery.NewService(store, clock, []discovery.Provider{
		pkggodev.New(),
		github.NewRepositoryProvider(client),
		github.NewCodeProvider(client),
	})
}

// NewEnricher wires the real Packet 7 enrichment providers over the same
// optional GitHub token.
func NewEnricher(store Store, cfg config.Config, clock enrichment.Clock) Enricher {
	return enrichment.NewService(store, clock, []enrichment.Provider{
		depsdev.New(),
		githubmeta.New(cfg.GitHubToken),
	})
}

// NewQualityResolver builds the Packet 7 quality decision service over the
// existing repository boundary.
func NewQualityResolver(store Store, clock resolver.Clock) QualityResolver {
	return resolver.NewQualityService(store, clock)
}
