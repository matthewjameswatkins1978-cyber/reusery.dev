// Package cli implements the reusery command line.
//
// Commands call the catalogue loader, the resolver service and the repository.
// Resolver business logic stays in internal/resolver; PostgreSQL stays behind
// interfaces defined outside this package.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/pkggodev"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent/providers/openai"
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

// Discoverer runs bounded public discovery for one profile. Tests inject a
// fake so CLI unit tests never touch the network.
type Discoverer interface {
	Discover(context.Context, discovery.Profile) (discovery.Result, error)
}

// Normalizer turns raw engineering intent into a structured result. Tests
// inject a fake so CLI unit tests never make a paid model call.
type Normalizer interface {
	Normalize(context.Context, string) (intent.Result, error)
}

// App is the command-line application. Production wiring lives in New; tests
// construct an App directly and inject fakes.
type App struct {
	Stdout io.Writer
	Stderr io.Writer

	// LoadConfig reads configuration (defaults to config.Load).
	LoadConfig func() (config.Config, error)
	// LoadModelConfig reads model-provider configuration. It deliberately
	// does not require PostgreSQL, so `normalize` runs without a database.
	LoadModelConfig func() (config.ModelConfig, error)
	// OpenStore opens the store and returns a cleanup function.
	OpenStore func(ctx context.Context, cfg config.Config) (Store, func(), error)
	// LoadBundle reads a catalogue manifest relative to a repository root.
	LoadBundle func(root, manifest string) (catalog.Bundle, error)
	// LoadProfile reads a discovery profile relative to a repository root.
	LoadProfile func(root, profile string) (discovery.Profile, error)
	// LoadCorpus reads an intent evaluation corpus.
	LoadCorpus func(path string) (intent.Corpus, error)
	// NewDiscoverer builds the discovery runner for one command invocation.
	NewDiscoverer func(store Store, cfg config.Config, clock discovery.Clock) Discoverer
	// NewNormalizer builds the normalisation runner for one command
	// invocation. It fails only on configuration problems.
	NewNormalizer func(cfg config.ModelConfig) (Normalizer, error)
	// Serve runs the HTTP server until ctx is cancelled.
	Serve func(ctx context.Context, cfg config.Config, logger *slog.Logger) error
	// Clock supplies resolution and discovery timestamps.
	Clock resolver.Clock
}

// New returns the production command-line application.
func New() *App {
	return &App{
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		LoadConfig:      config.Load,
		LoadModelConfig: config.LoadModel,
		OpenStore:       openPostgresStore,
		LoadBundle:      catalog.Load,
		LoadProfile:     discovery.LoadProfile,
		LoadCorpus:      intent.LoadCorpus,
		NewDiscoverer:   newPublicDiscoverer,
		NewNormalizer:   newOpenAINormalizer,
		Serve:           serveHTTP,
		Clock:           time.Now,
	}
}

// newOpenAINormalizer wires the single Packet 6 model adapter. The API key is
// required only here: serve, seed, discover, resolve and resolution never
// construct a normaliser.
func newOpenAINormalizer(cfg config.ModelConfig) (Normalizer, error) {
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

// newPublicDiscoverer wires the real Packet 5 providers. The GitHub token is
// optional: without it GitHub's public unauthenticated limits apply.
func newPublicDiscoverer(store Store, cfg config.Config, clock discovery.Clock) Discoverer {
	client := github.NewClient(cfg.GitHubToken)
	return discovery.NewService(store, clock, []discovery.Provider{
		pkggodev.New(),
		github.NewRepositoryProvider(client),
		github.NewCodeProvider(client),
	})
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
	case "discover":
		return a.commandDiscover(ctx, args[1:])
	case "normalize":
		return a.commandNormalize(ctx, args[1:])
	case "normalize-eval":
		return a.commandNormalizeEval(ctx, args[1:])
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

	store, _, closeStore, code := a.openStore(ctx)
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

	store, _, closeStore, code := a.openStore(ctx)
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

	store, _, closeStore, code := a.openStore(ctx)
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

// commandDiscover runs one bounded public discovery pass.
//
// It deliberately does NOT run resolver.Resolve, does not persist a
// Resolution, never chooses a winner and never claims BUILD LOCALLY:
// discovery produces plausible candidates and INFO/UNKNOWN observations only.
func (a *App) commandDiscover(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("discover", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	root := flags.String("root", ".", "repository root directory")
	profilePath := flags.String("profile", "", "path to the discovery profile, relative to root")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *profilePath == "" {
		a.errorf("reusery discover: --profile is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery discover: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	profile, err := a.LoadProfile(*root, *profilePath)
	if err != nil {
		a.errorf("reusery discover: %v", err)
		if errors.Is(err, discovery.ErrProfile) {
			return ExitUsage
		}
		return ExitError
	}

	store, cfg, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	result, runErr := a.NewDiscoverer(store, cfg, discovery.Clock(a.Clock)).Discover(ctx, profile)
	if runErr != nil && errors.Is(runErr, discovery.ErrProfile) {
		a.errorf("reusery discover: %v", runErr)
		return ExitUsage
	}

	// Provider reports stay inspectable even when the run as a whole failed.
	failure := runErr != nil && len(result.Providers) > 0
	if runErr != nil && !failure {
		a.errorf("reusery discover: %v", runErr)
		return ExitError
	}

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			a.errorf("reusery discover: write output: %v", err)
			return ExitError
		}
	} else {
		a.writeDiscoverText(result)
	}

	if failure {
		a.errorf("reusery discover: %v", runErr)
		return ExitError
	}
	a.errOut("discovered %d candidate(s) across %d provider(s)\n",
		len(result.Candidates), len(result.Providers))
	return ExitOK
}

func (a *App) writeDiscoverText(result discovery.Result) {
	a.out("primitive: %s\n", result.PrimitiveID)
	a.out("contract: %s\n", result.ContractID)
	a.out("observed_at: %s\n", result.ObservedAt.Format(time.RFC3339))

	a.out("providers:\n")
	for _, report := range result.Providers {
		status := "ok"
		if !report.Succeeded {
			status = "failed"
		}
		a.out("  %s (%s, requests=%d, candidates=%d, incomplete=%t)\n",
			report.ID, status, report.Requests, report.CandidateCount, report.Incomplete)
		for _, issue := range report.Issues {
			a.out("    - %s: %s\n", issue.Kind, issue.Message)
			if issue.Query != "" {
				a.out("      query: %s\n", issue.Query)
			}
			if issue.StatusCode != 0 {
				a.out("      status: %d\n", issue.StatusCode)
			}
			if issue.RetryAfter != "" {
				a.out("      retry-after: %s\n", issue.RetryAfter)
			}
		}
	}

	if len(result.Candidates) == 0 {
		a.out("candidates: (none)\n")
		return
	}
	a.out("candidates:\n")
	for _, candidate := range result.Candidates {
		a.out("  %s\n", candidate.Specimen.ID)
		a.out("    provider: %s\n", candidate.ProviderID)
		a.out("    name: %s\n", candidate.Specimen.Name)
		a.out("    reuse_modes: %s\n", joinReuseModes(candidate.Specimen.ReuseMode))
		a.out("    url: %s\n", candidate.Specimen.Source.URL)
		if candidate.Specimen.Source.Revision != "" {
			a.out("    revision: %s\n", candidate.Specimen.Source.Revision)
		}
		if candidate.Specimen.Source.Path != "" {
			a.out("    path: %s\n", candidate.Specimen.Source.Path)
		}
		if candidate.Specimen.Source.License != "" {
			a.out("    license: %s\n", candidate.Specimen.Source.License)
		}
		a.out("    evidence:\n")
		for _, evidence := range candidate.Evidence {
			a.out("      - %s %s: %s\n", evidence.Result, evidence.Kind, evidence.Claim)
		}
	}
}

func joinReuseModes(modes []model.ReuseMode) string {
	parts := make([]string, 0, len(modes))
	for _, mode := range modes {
		parts = append(parts, string(mode))
	}
	return strings.Join(parts, ",")
}

// commandNormalize turns ordinary engineering language into an inspectable
// provisional contract draft.
//
// It deliberately does NOT open PostgreSQL, does NOT run discovery and does
// NOT call the resolver: normalisation structures the question, it does not
// answer it. Exactly one of --text or --file is required, and the whole
// normalisation costs at most two paid model calls.
func (a *App) commandNormalize(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("normalize", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	text := flags.String("text", "", "engineering intent expressed in ordinary language")
	file := flags.String("file", "", "path to a file containing the engineering intent")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}

	if (*text == "") == (*file == "") {
		a.errorf("reusery normalize: exactly one of --text or --file is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery normalize: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	raw := *text
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			a.errorf("reusery normalize: cannot read %s: %v", *file, err)
			return ExitUsage
		}
		raw = string(data)
	}

	cfg, err := a.LoadModelConfig()
	if err != nil {
		a.errorf("reusery normalize: %v", err)
		return ExitError
	}
	normalizer, err := a.NewNormalizer(cfg)
	if err != nil {
		a.errorf("reusery normalize: %v", err)
		return ExitError
	}

	result, err := normalizer.Normalize(ctx, raw)
	if err != nil {
		a.errorf("reusery normalize: %v", err)
		if errors.Is(err, intent.ErrInvalidInput) {
			return ExitUsage
		}
		return ExitError
	}

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			a.errorf("reusery normalize: write output: %v", err)
			return ExitError
		}
		return ExitOK
	}

	a.writeNormalizeText(result)
	return ExitOK
}

// commandNormalizeEval runs the deterministic evaluation corpus. It performs
// paid model calls on purpose and says so before starting. A corpus problem is
// a usage error; a provider failure or an unmet acceptance gate is an
// execution failure.
func (a *App) commandNormalizeEval(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("normalize-eval", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	corpusPath := flags.String("corpus", "", "path to an evaluation corpus YAML file")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *corpusPath == "" {
		a.errorf("reusery normalize-eval: --corpus is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery normalize-eval: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	corpus, err := a.LoadCorpus(*corpusPath)
	if err != nil {
		a.errorf("reusery normalize-eval: %v", err)
		return ExitUsage
	}

	cfg, err := a.LoadModelConfig()
	if err != nil {
		a.errorf("reusery normalize-eval: %v", err)
		return ExitError
	}
	normalizer, err := a.NewNormalizer(cfg)
	if err != nil {
		a.errorf("reusery normalize-eval: %v", err)
		return ExitError
	}

	a.errOut("reusery normalize-eval performs paid model calls: %d corpus case(s)\n",
		len(corpus.Cases))

	report, err := intent.RunCorpus(ctx, normalizer, corpus)
	report.Corpus = *corpusPath
	if err != nil {
		a.errorf("reusery normalize-eval: %v", err)
		return ExitError
	}

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			a.errorf("reusery normalize-eval: write output: %v", err)
			return ExitError
		}
	} else {
		a.writeEvalText(report)
	}

	if !report.Gates.Met() {
		for _, failure := range report.Gates.Failures() {
			a.errorf("reusery normalize-eval: gate failed: %s", failure)
		}
		return ExitError
	}
	a.errOut("all acceptance gates met (%d/%d cases, %d repair(s))\n",
		report.Passed, report.Cases, report.Repairs)
	return ExitOK
}

func (a *App) writeNormalizeText(result intent.Result) {
	a.out("status: %s\n", result.Status)
	a.out("capability: %s\n", result.Capability)
	a.out("summary: %s\n", result.Summary)
	a.out("requested_artifact_level: %s\n", result.RequestedArtifactLevel)
	if result.Status == intent.StatusUnsupported {
		a.out("unsupported_reason: %s\n", result.UnsupportedReason)
	}
	if result.Primitive != nil && result.Contract != nil {
		a.out("primitive: %s\n", result.Primitive.ID)
		a.out("contract: %s (version %s)\n", result.Contract.ID, result.Contract.Version)
	}

	if result.Contract != nil {
		a.out("requirements:\n")
		for _, requirement := range result.Contract.Requirements {
			requirementKind := "optional"
			if requirement.Required {
				requirementKind = "required"
			}
			a.out("  %s [%s] %s: %s\n",
				requirement.ID, requirement.Kind, requirementKind, requirement.Description)
		}
	}

	if len(result.Constraints) > 0 {
		a.out("constraints:\n")
		for _, constraint := range result.Constraints {
			requirementKind := "optional"
			if constraint.Required {
				requirementKind = "required"
			}
			a.out("  [%s] %s: %s\n", constraint.Kind, requirementKind, constraint.Description)
		}
	}

	if len(result.Assumptions) > 0 {
		a.out("assumptions:\n")
		for _, assumption := range result.Assumptions {
			a.out("  - %s\n", assumption)
		}
	}

	if len(result.Ambiguities) > 0 {
		a.out("ambiguities:\n")
		for _, ambiguity := range result.Ambiguities {
			a.out("  - %s\n", ambiguity.Question)
			a.out("    why: %s\n", ambiguity.WhyItMatters)
		}
	}

	metadata := result.Metadata
	a.out("generation:\n")
	a.out("  provider: %s\n", metadata.Provider)
	a.out("  model: %s\n", metadata.Model)
	a.out("  response_id: %s\n", metadata.ResponseID)
	a.out("  prompt_version: %s\n", metadata.PromptVersion)
	a.out("  schema_version: %d\n", metadata.SchemaVersion)
	a.out("  model_calls: %d\n", metadata.Calls)
	a.out("  repaired: %t\n", metadata.Repaired)
	a.out("  tokens: input=%d output=%d reasoning=%d total=%d\n",
		metadata.Usage.InputTokens, metadata.Usage.OutputTokens,
		metadata.Usage.ReasoningTokens, metadata.Usage.TotalTokens)
}

func (a *App) writeEvalText(report intent.Report) {
	a.out("corpus: %s\n", report.Corpus)
	a.out("provider: %s\n", report.Provider)
	a.out("model: %s\n", report.Model)
	a.out("cases: %d\n", report.Cases)
	a.out("passed: %d\n", report.Passed)
	a.out("failed: %d\n", report.Failed)
	a.out("repairs: %d (%.1f%%)\n", report.Repairs, report.Gates.RepairRate*100)
	a.out("usage: input=%d output=%d reasoning=%d total=%d\n",
		report.Usage.InputTokens, report.Usage.OutputTokens,
		report.Usage.ReasoningTokens, report.Usage.TotalTokens)
	a.out("gates:\n")
	a.out("  structurally_valid: %s\n", gateMark(report.Gates.StructurallyValid))
	a.out("  safety_invariants: %s\n", gateMark(report.Gates.SafetyInvariants))
	a.out("  critical_ambiguous: %s\n", gateMark(report.Gates.CriticalAmbiguous))
	a.out("  adversarial_in_schema: %s\n", gateMark(report.Gates.AdversarialInSchema))
	a.out("  semantic_pass_rate: %s (%.1f%%, required %.1f%%)\n",
		gateMark(report.Gates.SemanticPassRateMet),
		report.Gates.SemanticPassRate*100, report.Gates.MinSemanticPassRate*100)
	a.out("  repair_rate: %s (%.1f%%, allowed %.1f%%)\n",
		gateMark(report.Gates.RepairRateMet),
		report.Gates.RepairRate*100, report.Gates.MaxRepairRate*100)

	if len(report.Results) == 0 {
		a.out("results: (none)\n")
		return
	}
	a.out("results:\n")
	for _, result := range report.Results {
		verdict := "fail"
		if result.Passed {
			verdict = "pass"
		}
		repaired := ""
		if result.Repaired {
			repaired = " repaired"
		}
		a.out("  %s %s %s%s\n", result.ID, verdict, result.Status, repaired)
		for _, failure := range result.Failures {
			a.out("    - %s\n", failure)
		}
	}
}

func gateMark(met bool) string {
	if met {
		return "ok"
	}
	return "FAIL"
}

func (a *App) openStore(ctx context.Context) (Store, config.Config, func(), int) {
	cfg, err := a.LoadConfig()
	if err != nil {
		a.errorf("reusery: invalid configuration: %v", err)
		return nil, config.Config{}, nil, ExitError
	}
	store, closeStore, err := a.OpenStore(ctx, cfg)
	if err != nil {
		a.errorf("reusery: cannot open store: %v", err)
		return nil, config.Config{}, nil, ExitError
	}
	return store, cfg, closeStore, ExitOK
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
  reusery discover --profile FILE [--root DIR] [--format text|json]
                                                     run bounded public discovery
  reusery normalize (--text STR | --file FILE) [--format text|json]
                                                     structure intent into a provisional contract
  reusery normalize-eval --corpus FILE [--format text|json]
                                                     run the intent evaluation corpus (PAID model calls)
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
