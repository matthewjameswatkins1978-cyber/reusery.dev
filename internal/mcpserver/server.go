// Package mcpserver exposes Reusery's existing application services over the
// Model Context Protocol.
//
// It is an adapter, not a resolver. Every tool maps onto a service that the
// CLI and the HTTP API already use: the Packet 5 discovery service, the
// Packet 7 enrichment and quality services, the shared store read surface and
// the append-only outcome log. Deleting this package would change no resolver
// behaviour.
//
// Transport: stdio only. `reusery mcp` reserves stdout exclusively for MCP
// frames, so nothing in this package may write to stdout; operational logging
// goes to stderr. Remote MCP is deliberately deferred until identity and abuse
// controls exist.
package mcpserver

import (
	"context"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version"
)

// Server identity and contract constants.
const (
	// ServerName is the MCP implementation name agents see.
	ServerName = "reusery"
	// ContractVersion identifies the tool contract. A breaking tool change
	// bumps this instead of silently changing agents underneath.
	ContractVersion = 1
)

// Bounds shared by the read tools.
const (
	// MaxCatalogPrimitives bounds the capability list.
	MaxCatalogPrimitives = app.MaxCapabilities
	// DefaultEvidenceLimit and MaxEvidenceLimit bound evidence paging. The
	// ceiling is lower than the CLI's so an agent call stays cheap.
	DefaultEvidenceLimit = 20
	MaxEvidenceLimit     = 50
	// OutcomeListLimit bounds the feedback history returned with a resolution.
	OutcomeListLimit = 100
)

// Dependencies is the injected application surface for the MCP boundary.
//
// It holds interfaces only: no PostgreSQL, no provider HTTP clients, no model
// provider. The MCP server never constructs a normaliser — a calling agent is
// already the reasoning surface, so paying for a second model call to translate
// its own structured reasoning back into JSON would be pure waste.
type Dependencies struct {
	// Catalog answers "what engineering behaviours does Reusery know?".
	Catalog app.Catalog
	// Discoverer and Enricher are the only tools gated behind
	// REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS.
	Discoverer app.Discoverer
	Enricher   app.Enricher
	// Resolver is the Packet 7 quality service: the same one HTTP and the CLI
	// use. There is no second resolver.
	Resolver app.QualityResolver
	// Inspector is the read surface for specimens, evidence and resolutions.
	Inspector app.Inspector
	// Outcomes records factual post-resolution events.
	Outcomes app.OutcomeRecorder
	// Projects owns bounded project context and explicit preference memory.
	// It is nil-able so the contract snapshot can be generated offline.
	Projects *project.Service
	// ProjectRoot is the local project root configured when the server
	// started. It is process configuration, never a tool argument: an agent
	// must not be handed an arbitrary filesystem-reading tool. Empty means no
	// local scanning is available and reusery_project_scan answers
	// project_root_unconfigured for the local source.
	ProjectRoot string
	// Logger must write to stderr. nil selects a discarding logger.
	Logger *slog.Logger
	// ExternalOperationsEnabled gates reusery_discover and reusery_enrich.
	ExternalOperationsEnabled bool
}

// Instructions is the short server description every agent connection pays
// for. Keep it small: it is a token tax on every session.
func Instructions() string {
	return "Reusery answers whether a good implementation of an engineering " +
		"capability already exists, before you write another one. Use it for " +
		"bounded capabilities and for licence, dependency, maintenance and " +
		"provenance decisions.\n" +
		"Invariants: provider discovery is not verification; metadata can never " +
		"satisfy a behavioural requirement; REFERENCE does not mean contract " +
		"satisfaction; needs_verification is a valid unresolved state, not a " +
		"failure; BUILD LOCALLY is a valid outcome; no known advisories is not " +
		"'secure'; a licence allowed by policy is not legal advice; provider " +
		"rank is not quality; absence of evidence is not evidence of quality.\n" +
		"Inspect evidence when uncertainty matters, refine with a supported " +
		"structured reason when the fit is poor, and report an outcome only " +
		"when you actually know it."
}

// NewServer builds the MCP server and registers every Packet 9 tool.
//
// It performs no I/O: no database connection, no provider call, no model
// construction. That is what lets the contract generator build the tool
// snapshot offline.
func NewServer(deps Dependencies) *mcp.Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	deps.Logger = logger

	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: version.Version,
	}, &mcp.ServerOptions{
		Instructions: Instructions(),
		Logger:       logger,
	})

	registerTools(server, deps)
	return server
}

// Run serves MCP over stdio until ctx is cancelled.
//
// stdout carries MCP frames and nothing else.
func Run(ctx context.Context, deps Dependencies) error {
	return NewServer(deps).Run(ctx, &mcp.StdioTransport{})
}
