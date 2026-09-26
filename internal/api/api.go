package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
)

// APIVersion identifies the frozen product contract served by this package.
const APIVersion = "v1"

// Response and request headers added by middleware.
const (
	headerRequestID  = "X-Request-ID"
	headerAPIVersion = "Reusery-API-Version"
	headerNoSniff    = "X-Content-Type-Options"
	headerNoStore    = "Cache-Control"
)

// maxRequestIDLength bounds an inbound X-Request-ID. Longer or malformed
// values are replaced with a generated identifier, never rejected.
const maxRequestIDLength = 128

// Per-operation HTTP budgets. These sit outside the already-bounded internal
// provider and service calls, so the tighter inner deadline always wins.
const (
	BudgetHealth    = 3 * time.Second
	BudgetReady     = 3 * time.Second
	BudgetInspect   = 5 * time.Second
	BudgetResolve   = 10 * time.Second
	BudgetRefine    = 10 * time.Second
	BudgetNormalize = 45 * time.Second
	BudgetDiscover  = 50 * time.Second
	BudgetEnrich    = 35 * time.Second
)

// Request body ceilings per operation. Oversized bodies are rejected with
// HTTP 413 before any application work runs.
const (
	MaxBodyNormalize = 16 << 10
	MaxBodyDiscover  = 64 << 10
	MaxBodyEnrich    = 32 << 10
	MaxBodyResolve   = 256 << 10
	MaxBodyRefine    = 256 << 10
)

// Dependencies is the injected application surface for the HTTP boundary.
//
// It holds interfaces and factories only: no PostgreSQL, no provider HTTP
// clients, no configuration loaders. Tests construct a Handler with fakes and
// never touch the network or the database.
type Dependencies struct {
	// NormalizerFactory builds the Packet 6 normaliser on demand. Returning
	// config.ErrMissingOpenAIAPIKey maps to 503 model_provider_unconfigured
	// without failing startup.
	NormalizerFactory func() (app.Normalizer, error)
	// Discoverer runs one bounded Packet 5 discovery profile.
	Discoverer app.Discoverer
	// Enricher runs one bounded Packet 7 enrichment pass.
	Enricher app.Enricher
	// Resolver is the Packet 7 quality service. The API never exposes the
	// Packet 4 first-acceptable kernel.
	Resolver app.QualityResolver
	// Inspector is the read surface for primitives, contracts, specimens,
	// evidence pages and stored resolutions.
	Inspector app.Inspector
	// ReadyCheckers are the dependencies readiness verifies. Only PostgreSQL
	// is registered: degradable providers are never readiness dependencies.
	ReadyCheckers []app.ReadyChecker
	// Logger receives bounded structured request logs.
	Logger *slog.Logger
	// ExternalOperationsEnabled gates POST /v1/normalize, POST /v1/discover
	// and POST /v1/enrich. It is false unless explicitly configured.
	ExternalOperationsEnabled bool
}

// Handler is the versioned HTTP surface. It is an http.Handler and also
// exposes the registered OpenAPI document so the contract generator can read
// it without starting a server or constructing live services.
type Handler struct {
	deps   Dependencies
	logger *slog.Logger
	mux    *http.ServeMux
	api    huma.API
	// exact maps "METHOD /literal/path" to its operation id.
	exact map[string]string
	// templated maps method plus path prefix to its operation id.
	templated []templatedRoute
}

type templatedRoute struct {
	method      string
	prefix      string
	operationID string
}

var problemHookOnce sync.Once

// NewHandler registers every API v1 operation and returns the handler.
//
// It performs no I/O: no database connection, no provider call, no model
// construction. That is what lets cmd/openapi generate the contract offline.
func NewHandler(deps Dependencies) *Handler {
	installProblemHook()

	// Arrays are documented as arrays, never as "array or null". API v1
	// always serialises a collection as [] and reserves null for optional
	// single objects such as `resolution`.
	huma.DefaultArrayNullable = false

	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	h := &Handler{
		deps:   deps,
		logger: logger,
		mux:    http.NewServeMux(),
		exact:  map[string]string{},
	}
	h.api = humago.New(h.mux, newConfig())
	h.registerRoutes()
	return h
}

// OpenAPI returns the registered OpenAPI 3.1 document.
func (h *Handler) OpenAPI() *huma.OpenAPI { return h.api.OpenAPI() }

// register adds one operation and records its id for structured request logs.
func register[I, O any](h *Handler, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	huma.Register(h.api, op, handler)
	h.exact[op.Method+" "+op.Path] = op.OperationID
	if i := strings.Index(op.Path, "{"); i >= 0 {
		h.templated = append(h.templated, templatedRoute{
			method:      op.Method,
			prefix:      op.Path[:i],
			operationID: op.OperationID,
		})
	}
}

// operationIDFor resolves the contract operation id for logging. Generated
// documentation routes are labelled rather than silently unmapped.
func (h *Handler) operationIDFor(method, path string) string {
	if id, ok := h.exact[method+" "+path]; ok {
		return id
	}
	for _, route := range h.templated {
		if route.method == method && strings.HasPrefix(path, route.prefix) {
			return route.operationID
		}
	}
	switch {
	case strings.HasPrefix(path, "/openapi"):
		return "getOpenAPISpec"
	case path == "/docs":
		return "getAPIDocs"
	case strings.HasPrefix(path, "/schemas/"):
		return "getSchema"
	}
	return "unmatched"
}

// newConfig builds the Huma configuration for API v1.
//
// It is assembled directly rather than via huma.DefaultConfig so the shipped
// contract stays a clean, hand-reviewable document without injected $schema
// links, and so unknown query parameters are rejected.
func newConfig() huma.Config {
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:   "Reusery API",
				Version: APIVersion,
				Description: "Stable HTTP surface over Reusery's existing application services.\n\n" +
					"Important semantics this contract preserves:\n\n" +
					"- **discovery is not verification.** Discovery only records INFO/UNKNOWN observations.\n" +
					"- **REFERENCE is not contract satisfaction.** A reference resolution keeps every\n" +
					"  unresolved required behavioural requirement as an unknown.\n" +
					"- **needs_verification is not a failure.** It is a valid HTTP 200 result that\n" +
					"  persists nothing.\n" +
					"- **BUILD LOCALLY is a valid resolution outcome**, not an error.\n" +
					"- Metadata from providers can never produce a behavioural pass.\n\n" +
					"Reusery is not yet an authenticated production service; see docs/http-api.md.",
			},
			Tags: []*huma.Tag{
				{Name: "Operations", Description: "Liveness and readiness probes. They never depend on degradable providers."},
				{Name: "Intent", Description: "Packet 6 intent normalisation. ready, needs_clarification and unsupported are all valid HTTP 200 results."},
				{Name: "Discovery", Description: "Packet 5 bounded public discovery. Discovery records INFO/UNKNOWN observations only: discovery is not verification."},
				{Name: "Evidence", Description: "Packet 7 metadata enrichment and evidence inspection. Provider metadata can never create a behavioural pass."},
				{Name: "Resolver", Description: "Packet 7 quality resolution and stateless refinement. needs_verification is not a failure and BUILD LOCALLY is a valid resolution."},
				{Name: "Inspection", Description: "Read-only access to stored primitives, contracts, specimens and resolutions. Opaque Reusery ids travel as query parameters."},
			},
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer),
			},
		},
		OpenAPIPath:                  "/openapi",
		DocsPath:                     "/docs",
		SchemasPath:                  "/schemas",
		Formats:                      huma.DefaultFormats,
		DefaultFormat:                "application/json",
		RejectUnknownQueryParameters: true,
	}
}

// documentNullableObjects expresses JSON null for object-typed response fields
// that Huma documents as a plain $ref.
//
// Huma deliberately panics on `nullable:"true"` for an object reference, so
// without this the contract would claim `resolution` is always an object while
// a needs_verification response correctly returns null. The anyOf form is the
// OpenAPI 3.1 way of saying "object or nothing".
func (h *Handler) documentNullableObjects() {
	const prefix = "#/components/schemas/"
	nullable := map[string][]string{
		"DecisionResponse": {"resolution", "selected"},
		"IntentResult":     {"primitive", "contract"},
	}
	schemas := h.api.OpenAPI().Components.Schemas
	for name, fields := range nullable {
		schema := schemas.SchemaFromRef(prefix + name)
		if schema == nil || schema.Properties == nil {
			continue
		}
		for _, field := range fields {
			property := schema.Properties[field]
			if property == nil || property.Ref == "" {
				continue
			}
			schema.Properties[field] = &huma.Schema{
				Description: property.Description,
				AnyOf: []*huma.Schema{
					{Ref: property.Ref},
					{Type: "null"},
				},
			}
		}
	}
}
