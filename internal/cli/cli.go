// Package cli implements the reusery command line.
//
// Commands call the catalogue loader, the resolver service and the repository.
// Resolver business logic stays in internal/resolver; PostgreSQL stays behind
// interfaces defined outside this package.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/server"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version"
)

// Exit codes. Usage problems are distinguishable from execution failures.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Store is the combined persistence surface the CLI needs.
type Store interface {
	resolver.Repository
	catalog.Store
}

// App is the command-line application. Production wiring lives in New; tests
// construct an App directly and inject fakes.
type App struct {
	Stdout io.Writer
	Stderr io.Writer

	// LoadConfig reads configuration (defaults to config.Load).
	LoadConfig func() (config.Config, error)
	// OpenStore opens the store and returns a cleanup function.
	OpenStore func(ctx context.Context, cfg config.Config) (Store, func(), error)
	// LoadBundle reads a catalogue manifest relative to a repository root.
	LoadBundle func(root, manifest string) (catalog.Bundle, error)
	// Serve runs the HTTP server until ctx is cancelled.
	Serve func(ctx context.Context, cfg config.Config, logger *slog.Logger) error
	// Clock supplies resolution timestamps.
	Clock resolver.Clock
}

// New returns the production command-line application.
func New() *App {
	return &App{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		LoadConfig: config.Load,
		OpenStore:  openPostgresStore,
		LoadBundle: catalog.Load,
		Serve:      serveHTTP,
		Clock:      time.Now,
	}
}

// Run executes one command and returns the process exit code.
// With no arguments the server starts, preserving the pre-CLI behaviour.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.commandServe(ctx)
	}

	switch args[0] {
	case "serve":
		return a.commandServe(ctx)
	case "seed":
		return a.commandSeed(ctx, args[1:])
	case "resolve":
		return a.commandResolve(ctx, args[1:])
	case "resolution":
		return a.commandResolution(ctx, args[1:])
	case "help", "-h", "--help":
		a.usage(a.Stdout)
		return ExitOK
	default:
		a.errorf("reusery: unknown command %q", args[0])
		a.usage(a.Stderr)
		return ExitUsage
	}
}

func (a *App) commandServe(ctx context.Context) int {
	cfg, err := a.LoadConfig()
	if err != nil {
		a.errorf("reusery: invalid configuration: %v", err)
		return ExitError
	}

	logger := slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	attrs := make([]any, 0, len(version.Info())*2+2)
	attrs = append(attrs, "addr", cfg.HTTPAddr)
	for key, value := range version.Info() {
		attrs = append(attrs, key, value)
	}
	logger.Info("starting reusery", attrs...)

	if err := a.Serve(ctx, cfg, logger); err != nil {
		logger.Error("server stopped with error", "error", err)
		return ExitError
	}
	logger.Info("server stopped")
	return ExitOK
}

func (a *App) commandSeed(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	root := flags.String("root", ".", "repository root directory")
	manifest := flags.String("manifest", "", "path to the seed manifest, relative to root")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *manifest == "" {
		a.errorf("reusery seed: --manifest is required")
		return ExitUsage
	}

	bundle, err := a.LoadBundle(*root, *manifest)
	if err != nil {
		a.errorf("reusery seed: %v", err)
		return ExitError
	}

	store, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	if err := catalog.Seed(ctx, bundle, store); err != nil {
		a.errorf("reusery seed: %v", err)
		return ExitError
	}

	a.errOut("seeded primitive %q, contract %q, %d specimens, %d evidence\n",
		bundle.Primitive.ID, bundle.Contract.ID, len(bundle.Specimens), len(bundle.Evidence))
	return ExitOK
}

// resolveJSON is the canonical machine-readable resolve output.
type resolveJSON struct {
	ResolutionID int64 `json:"resolution_id"`
	resolver.Decision
}

// resolutionJSON is the canonical machine-readable inspection output.
type resolutionJSON struct {
	ResolutionID int64            `json:"resolution_id"`
	Resolution   model.Resolution `json:"resolution"`
}

func (a *App) commandResolve(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("resolve", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	requestPath := flags.String("request", "", "path to a JSON resolve request")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *requestPath == "" {
		a.errorf("reusery resolve: --request is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery resolve: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	request, err := readRequest(*requestPath)
	if err != nil {
		a.errorf("reusery resolve: %v", err)
		return ExitError
	}

	store, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	service := resolver.NewService(store, a.Clock)
	stored, err := service.Resolve(ctx, request)
	if err != nil {
		a.errorf("reusery resolve: %v", err)
		return ExitError
	}
	a.errOut("resolution %d stored (outcome=%s)\n", stored.ResolutionID, stored.Decision.Resolution.Outcome)

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(resolveJSON{ResolutionID: stored.ResolutionID, Decision: stored.Decision}); err != nil {
			a.errorf("reusery resolve: write output: %v", err)
			return ExitError
		}
		return ExitOK
	}

	a.writeResolveText(stored.ResolutionID, stored.Decision)
	return ExitOK
}

func (a *App) commandResolution(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("resolution", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	id := flags.Int64("id", 0, "storage resolution ID")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *id <= 0 {
		a.errorf("reusery resolution: --id is required and must be positive")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery resolution: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	store, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	// The stored resolution is the remembered decision; current evidence is
	// deliberately not re-evaluated.
	resolution, err := store.GetResolution(ctx, *id)
	if err != nil {
		a.errorf("reusery resolution: %v", err)
		return ExitError
	}

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(resolutionJSON{ResolutionID: *id, Resolution: resolution}); err != nil {
			a.errorf("reusery resolution: write output: %v", err)
			return ExitError
		}
		return ExitOK
	}

	a.writeResolutionText(*id, resolution)
	return ExitOK
}

func (a *App) openStore(ctx context.Context) (Store, func(), int) {
	cfg, err := a.LoadConfig()
	if err != nil {
		a.errorf("reusery: invalid configuration: %v", err)
		return nil, nil, ExitError
	}
	store, closeStore, err := a.OpenStore(ctx, cfg)
	if err != nil {
		a.errorf("reusery: cannot open store: %v", err)
		return nil, nil, ExitError
	}
	return store, closeStore, ExitOK
}

func (a *App) writeResolveText(id int64, decision resolver.Decision) {
	resolution := decision.Resolution
	a.out("resolution_id: %d\n", id)
	a.out("outcome: %s\n", resolution.Outcome)
	a.out("primitive: %s\n", resolution.PrimitiveID)
	a.out("contract: %s\n", resolution.ContractID)
	if resolution.SpecimenID != "" {
		a.out("specimen: %s\n", resolution.SpecimenID)
	}
	a.out("resolved_at: %s\n", resolution.ResolvedAt.Format(time.RFC3339))
	a.writeList("reasons", resolution.Reasons)

	for _, rejected := range resolution.Rejected {
		a.out("rejected:\n  %s\n", rejected.SpecimenID)
		for _, reason := range rejected.Reasons {
			a.out("    - %s\n", reason)
		}
	}
	a.writeList("unknowns", resolution.Unknowns)
	a.writeList("evidence_ids", resolution.EvidenceIDs)

	a.out("considered:\n")
	for _, candidate := range decision.Considered {
		a.out("  %s (mode=%s, selected=%t)\n", candidate.SpecimenID, candidate.ReuseMode, candidate.Selected)
		for _, requirement := range candidate.Evaluation.Requirements {
			a.out("    %s=%s\n", requirement.RequirementID, requirement.Status)
		}
	}
}

func (a *App) writeResolutionText(id int64, resolution model.Resolution) {
	a.out("resolution_id: %d\n", id)
	a.out("outcome: %s\n", resolution.Outcome)
	a.out("primitive: %s\n", resolution.PrimitiveID)
	a.out("contract: %s\n", resolution.ContractID)
	if resolution.SpecimenID != "" {
		a.out("specimen: %s\n", resolution.SpecimenID)
	}
	a.out("resolved_at: %s\n", resolution.ResolvedAt.Format(time.RFC3339))
	a.writeList("reasons", resolution.Reasons)
	for _, rejected := range resolution.Rejected {
		a.out("rejected:\n  %s\n", rejected.SpecimenID)
		for _, reason := range rejected.Reasons {
			a.out("    - %s\n", reason)
		}
	}
	a.writeList("unknowns", resolution.Unknowns)
	a.writeList("evidence_ids", resolution.EvidenceIDs)
}

func (a *App) writeList(label string, values []string) {
	if len(values) == 0 {
		return
	}
	a.out("%s:\n", label)
	for _, value := range values {
		a.out("  - %s\n", value)
	}
}

func (a *App) usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `reusery — engineering reuse and verification

Usage:
  reusery serve                                      start the HTTP server (default)
  reusery seed --root DIR --manifest FILE            load a catalogue seed bundle
  reusery resolve --request FILE [--format text|json]  run a resolve request
  reusery resolution --id N [--format text|json]     inspect a stored resolution
  reusery help                                       show this help
`)
}

// out writes to stdout. A closed pipe is not an actionable failure for a CLI,
// so write errors are absorbed once here instead of at every call site.
func (a *App) out(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stdout, format, args...)
}

// errOut writes to stderr without appending a newline.
func (a *App) errOut(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stderr, format, args...)
}

func (a *App) errorf(format string, args ...any) {
	a.errOut(format+"\n", args...)
}

func validFormat(format string) bool {
	return format == "text" || format == "json"
}

// readRequest decodes a resolve request, rejecting unknown JSON fields so
// typos fail loudly instead of being silently ignored.
func readRequest(path string) (resolver.Request, error) {
	file, err := os.Open(path)
	if err != nil {
		return resolver.Request{}, fmt.Errorf("open request %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var request resolver.Request
	if err := decoder.Decode(&request); err != nil {
		return resolver.Request{}, fmt.Errorf("decode request %s: %w", path, err)
	}
	return request, nil
}

func openPostgresStore(ctx context.Context, cfg config.Config) (Store, func(), error) {
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	return postgres.NewStore(pool), pool.Close, nil
}

func serveHTTP(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("postgres connected")

	srv := server.New(cfg.HTTPAddr, logger, postgres.ReadyChecker(pool))
	return srv.Start(ctx)
}
