package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/mcpserver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version"
)

// stdinSelector is the document argument that means "read standard input".
const stdinSelector = "-"

// Bounds for the bounded read commands.
const (
	cliEvidenceDefaultLimit = 50
	cliEvidenceMaxLimit     = 100
)

// ---------------------------------------------------------- document inputs

// decodeStdin decodes one strict JSON document from standard input. Unknown
// fields are rejected so a typo fails loudly, exactly as a file input does.
func (a *App) decodeStdin(into any) error {
	decoder := json.NewDecoder(a.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("decode standard input: %w", err)
	}
	return nil
}

// readRequestDocument reads a resolve request from a file or standard input.
func (a *App) readRequestDocument(path string) (resolver.Request, error) {
	if path == stdinSelector {
		var request resolver.Request
		if err := a.decodeStdin(&request); err != nil {
			return resolver.Request{}, err
		}
		return request, nil
	}
	return readRequest(path)
}

// readJSONDocument reads a root-confined JSON document, or standard input.
//
// Ordinary file inputs keep every path-security check: choosing "-" only
// changes where the bytes come from, never where a path is allowed to reach.
func (a *App) readJSONDocument(root, path string, into any) error {
	if path == stdinSelector {
		return a.decodeStdin(into)
	}
	return readJSONUnder(root, path, into)
}

// loadProfileDocument reads a discovery profile from a file or stdin.
func (a *App) loadProfileDocument(root, path string) (discovery.Profile, error) {
	if path == stdinSelector {
		return discovery.DecodeProfile(a.Stdin, stdinSelector)
	}
	return a.LoadProfile(root, path)
}

// loadPolicyDocument reads a policy profile from a file or stdin.
func (a *App) loadPolicyDocument(root, path string) (policy.Policy, error) {
	if path == stdinSelector {
		return policy.Decode(a.Stdin, stdinSelector)
	}
	return a.LoadPolicy(root, path)
}

// loadFeedbackDocument reads a feedback document from a file or stdin.
func (a *App) loadFeedbackDocument(root, path string) ([]policy.Feedback, error) {
	if path == stdinSelector {
		return policy.DecodeFeedback(a.Stdin, stdinSelector)
	}
	return a.LoadFeedback(root, path)
}

// rejectMultipleStdin refuses an ambiguous request: a process has one standard
// input, so two document arguments cannot both be "-".
func (a *App) rejectMultipleStdin(command string, paths map[string]string) error {
	count := 0
	for _, path := range paths {
		if path == stdinSelector {
			count++
		}
	}
	if count > 1 {
		names := make([]string, 0, len(paths))
		for name, path := range paths {
			if path == stdinSelector {
				names = append(names, name)
			}
		}
		return fmt.Errorf("%s: only one of %s may read standard input at once",
			command, strings.Join(sortedCopy(names), ", "))
	}
	return nil
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ------------------------------------------------------------------ commands

// commandMCP serves the Model Context Protocol over stdio.
//
// stdout carries MCP frames and nothing else; every log line and every error
// goes to stderr. A failure to reach PostgreSQL happens before the server is
// constructed, so no partial frame is ever written.
func (a *App) commandMCP(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	projectRoot := flags.String("project-root", "",
		"local project root configured for this MCP server; process configuration, never a tool argument")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if flags.NArg() > 0 {
		a.errorf("reusery mcp: unexpected arguments: %s", strings.Join(flags.Args(), " "))
		return ExitUsage
	}
	if *projectRoot != "" {
		if info, err := os.Stat(*projectRoot); err != nil || !info.IsDir() {
			a.errorf("reusery mcp: --project-root %q is not a directory", *projectRoot)
			return ExitUsage
		}
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		a.errorf("reusery mcp: invalid configuration: %v", err)
		return ExitError
	}
	store, closeStore, err := a.OpenStore(ctx, cfg)
	if err != nil {
		a.errorf("reusery mcp: cannot open store: %v", redact(err, cfg.DatabaseURL))
		return ExitError
	}
	defer closeStore()

	// Project context is part of the standing surface: if the store cannot
	// provide it the server would advertise tools it cannot run, so it fails
	// before the first frame rather than mid-conversation.
	projectService, err := app.NewProjectService(store, a.Clock)
	if err != nil {
		a.errorf("reusery mcp: project context unavailable: %v", err)
		return ExitError
	}

	logger := slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("starting mcp server",
		"transport", "stdio",
		"external_operations", cfg.MCPEnableExternalOperations,
		"project_root", *projectRoot,
		"version", version.Version)

	if err := mcpserver.Run(ctx, mcpserver.Dependencies{
		Catalog:                   store,
		Discoverer:                a.NewDiscoverer(store, cfg, discovery.Clock(a.Clock)),
		Enricher:                  a.NewEnricher(store, cfg, enrichment.Clock(a.Clock)),
		Resolver:                  app.NewQualityResolver(store, a.Clock),
		Inspector:                 store,
		Outcomes:                  app.NewOutcomeRecorder(store, a.Clock),
		Projects:                  projectService,
		Logger:                    logger,
		ExternalOperationsEnabled: cfg.MCPEnableExternalOperations,
		ProjectRoot:               *projectRoot,
	}); err != nil && !errors.Is(err, context.Canceled) {
		a.errorf("reusery mcp: %v", redact(err, cfg.DatabaseURL))
		return ExitError
	}
	logger.Info("mcp server stopped")
	return ExitOK
}

// commandVersion prints build metadata from the one existing version source.
func (a *App) commandVersion(args []string) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery version: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	if *format == "json" {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(version.Info()); err != nil {
			a.errorf("reusery version: write output: %v", err)
			return ExitError
		}
		return ExitOK
	}

	a.out("%s %s (commit %s, built %s)\n", version.Name, version.Version, version.Commit, version.BuildTime)
	return ExitOK
}

// capabilityJSON is one line of the capability list.
type capabilityJSON struct {
	PrimitiveID     string   `json:"primitive_id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ContractID      string   `json:"contract_id"`
	ContractSummary string   `json:"contract_summary"`
	Tags            []string `json:"tags"`
}

// requirementJSON is one contract claim.
type requirementJSON struct {
	RequirementID string `json:"requirement_id"`
	Description   string `json:"description"`
	Kind          string `json:"kind"`
	Required      bool   `json:"required"`
}

// contractJSON is a contract with its ordered requirements.
type contractJSON struct {
	ContractID   string            `json:"contract_id"`
	PrimitiveID  string            `json:"primitive_id"`
	Version      string            `json:"version"`
	Summary      string            `json:"summary"`
	Requirements []requirementJSON `json:"requirements"`
}

// commandCatalog lists known capabilities or inspects one primitive. It uses
// the same application helper as the MCP reusery_catalog tool.
func (a *App) commandCatalog(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("catalog", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	primitiveID := flags.String("primitive-id", "", "opaque primitive id to inspect; omit to list capabilities")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery catalog: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	store, _, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	if *primitiveID != "" {
		primitive, err := store.GetPrimitive(ctx, *primitiveID)
		if err != nil {
			a.errorf("reusery catalog: %v", err)
			return ExitError
		}
		contract, err := store.GetContract(ctx, primitive.ContractID)
		if err != nil {
			a.errorf("reusery catalog: %v", err)
			return ExitError
		}
		payload := map[string]any{
			"primitive": toCapabilityJSON(app.Capability{Primitive: primitive, ContractSummary: contract.Summary}),
			"contract":  toContractJSON(contract),
		}
		if *format == "json" {
			return a.writeJSON(payload)
		}
		a.out("primitive: %s\n", primitive.ID)
		a.out("name: %s\n", primitive.Name)
		a.out("description: %s\n", primitive.Description)
		a.out("contract: %s\n", contract.ID)
		a.out("summary: %s\n", contract.Summary)
		a.out("requirements:\n")
		for _, requirement := range contract.Requirements {
			a.out("  %s required=%t kind=%s: %s\n", requirement.ID, requirement.Required, requirement.Kind, requirement.Description)
		}
		return ExitOK
	}

	capabilities, err := app.ListCapabilities(ctx, store, app.MaxCapabilities)
	if err != nil {
		a.errorf("reusery catalog: %v", err)
		return ExitError
	}
	list := make([]capabilityJSON, 0, len(capabilities))
	for _, capability := range capabilities {
		list = append(list, toCapabilityJSON(capability))
	}
	if *format == "json" {
		return a.writeJSON(map[string]any{"primitives": list})
	}
	if len(list) == 0 {
		a.out("capabilities: (none)\n")
		return ExitOK
	}
	for _, entry := range list {
		a.out("%s\n  %s\n  contract: %s\n", entry.PrimitiveID, entry.Name, entry.ContractID)
		if entry.ContractSummary != "" {
			a.out("  %s\n", entry.ContractSummary)
		}
	}
	a.errOut("%d capability(ies)\n", len(list))
	return ExitOK
}

// evidencePageJSON is one bounded page of stored evidence.
type evidencePageJSON struct {
	SubjectID  string           `json:"subject_id"`
	Evidence   []model.Evidence `json:"evidence"`
	Limit      int              `json:"limit"`
	NextCursor *evidenceCursor  `json:"next_cursor"`
}

// evidenceCursor is the continuation key, deliberately not the HTTP cursor.
type evidenceCursor struct {
	ObservedAt time.Time `json:"observed_at"`
	EvidenceID string    `json:"evidence_id"`
}

// commandEvidence reads a bounded page of stored evidence.
func (a *App) commandEvidence(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("evidence", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	subjectID := flags.String("subject-id", "", "opaque specimen id whose evidence is wanted")
	limit := flags.Int("limit", cliEvidenceDefaultLimit, "page size (1-100)")
	afterObservedAt := flags.String("after-observed-at", "", "RFC3339 continuation timestamp")
	afterID := flags.String("after-id", "", "continuation evidence id")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *subjectID == "" {
		a.errorf("reusery evidence: --subject-id is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery evidence: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}
	if *limit < 1 || *limit > cliEvidenceMaxLimit {
		a.errorf("reusery evidence: --limit must be between 1 and %d", cliEvidenceMaxLimit)
		return ExitUsage
	}

	var after time.Time
	if *afterObservedAt != "" {
		parsed, err := time.Parse(time.RFC3339, *afterObservedAt)
		if err != nil {
			a.errorf("reusery evidence: --after-observed-at must be RFC3339: %v", err)
			return ExitUsage
		}
		after = parsed
	}
	if (*afterObservedAt == "") != (*afterID == "") {
		a.errorf("reusery evidence: --after-observed-at and --after-id must be supplied together")
		return ExitUsage
	}

	store, _, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	// One extra row decides whether a next cursor exists without ever
	// returning an unbounded page.
	rows, err := store.ListEvidenceAfter(ctx, *subjectID, after, *afterID, *limit+1)
	if err != nil {
		a.errorf("reusery evidence: %v", err)
		return ExitError
	}
	hasNext := len(rows) > *limit
	if hasNext {
		rows = rows[:*limit]
	}

	page := evidencePageJSON{SubjectID: *subjectID, Evidence: rows, Limit: *limit}
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor = &evidenceCursor{ObservedAt: last.ObservedAt, EvidenceID: last.ID}
	}
	if *format == "json" {
		return a.writeJSON(page)
	}
	if len(page.Evidence) == 0 {
		a.out("evidence: (none)\n")
		return ExitOK
	}
	for _, item := range page.Evidence {
		a.out("%s %s %s: %s\n", item.ObservedAt.Format(time.RFC3339), item.Result, item.Kind, item.Claim)
	}
	if page.NextCursor != nil {
		a.errOut("%d observation(s); more available (--after-observed-at %s --after-id %s)\n",
			len(page.Evidence), page.NextCursor.ObservedAt.Format(time.RFC3339), page.NextCursor.EvidenceID)
	} else {
		a.errOut("%d observation(s); last page\n", len(page.Evidence))
	}
	return ExitOK
}

// outcomeJSON is one recorded post-resolution event.
type outcomeJSON struct {
	ID           int64        `json:"id"`
	ResolutionID int64        `json:"resolution_id"`
	Kind         outcome.Kind `json:"kind"`
	Note         string       `json:"note"`
	RecordedAt   time.Time    `json:"recorded_at"`
}

// commandOutcome records what actually happened after a Resolution was used.
func (a *App) commandOutcome(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("outcome", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	resolutionID := flags.Int64("resolution-id", 0, "storage identity of the resolution")
	kind := flags.String("kind", "", "adopted, rejected, integration_succeeded, integration_failed or abandoned")
	note := flags.String("note", "", "optional note of at most 1000 characters")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *resolutionID <= 0 {
		a.errorf("reusery outcome: --resolution-id must be a positive integer")
		return ExitUsage
	}
	if !outcome.ValidKind(outcome.Kind(*kind)) {
		a.errorf("reusery outcome: unsupported --kind %q (want %s)", *kind, joinKinds())
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery outcome: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	store, _, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	service := app.NewOutcomeRecorder(store, a.Clock)
	stored, err := service.Report(ctx, *resolutionID, outcome.Kind(*kind), *note)
	if err != nil {
		if errors.Is(err, outcome.ErrResolutionNotFound) {
			a.errorf("reusery outcome: resolution %d does not exist", *resolutionID)
			return ExitError
		}
		a.errorf("reusery outcome: %v", err)
		return ExitError
	}

	payload := outcomeJSON{
		ID: stored.ID, ResolutionID: stored.ResolutionID,
		Kind: stored.Kind, Note: stored.Note, RecordedAt: stored.RecordedAt,
	}
	if *format == "json" {
		return a.writeJSON(payload)
	}
	a.out("recorded %s for resolution %d (event %d at %s)\n",
		stored.Kind, stored.ResolutionID, stored.ID, stored.RecordedAt.Format(time.RFC3339))
	return ExitOK
}

// commandOutcomes lists recorded post-resolution events in chronological order.
func (a *App) commandOutcomes(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("outcomes", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	resolutionID := flags.Int64("resolution-id", 0, "storage identity of the resolution")
	limit := flags.Int("limit", outcome.DefaultListLimit, "maximum events to return")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *resolutionID <= 0 {
		a.errorf("reusery outcomes: --resolution-id must be a positive integer")
		return ExitUsage
	}
	if *limit < 1 || *limit > outcome.MaxListLimit {
		a.errorf("reusery outcomes: --limit must be between 1 and %d", outcome.MaxListLimit)
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery outcomes: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	store, _, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return code
	}
	defer closeStore()

	service := app.NewOutcomeRecorder(store, a.Clock)
	events, err := service.List(ctx, *resolutionID, *limit)
	if err != nil {
		a.errorf("reusery outcomes: %v", err)
		return ExitError
	}

	list := make([]outcomeJSON, 0, len(events))
	for _, event := range events {
		list = append(list, outcomeJSON{
			ID: event.ID, ResolutionID: event.ResolutionID,
			Kind: event.Kind, Note: event.Note, RecordedAt: event.RecordedAt,
		})
	}
	if *format == "json" {
		return a.writeJSON(map[string]any{"resolution_id": *resolutionID, "events": list})
	}
	if len(list) == 0 {
		a.out("outcomes: (none)\n")
		return ExitOK
	}
	for _, event := range list {
		a.out("%s event=%d %s", event.RecordedAt.Format(time.RFC3339), event.ID, event.Kind)
		if event.Note != "" {
			a.out(" note=%q", event.Note)
		}
		a.out("\n")
	}
	return ExitOK
}

// writeJSON writes an indented JSON document on stdout only.
func (a *App) writeJSON(payload any) int {
	encoder := json.NewEncoder(a.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		a.errorf("reusery: write output: %v", err)
		return ExitError
	}
	return ExitOK
}

func toCapabilityJSON(capability app.Capability) capabilityJSON {
	return capabilityJSON{
		PrimitiveID:     capability.Primitive.ID,
		Name:            capability.Primitive.Name,
		Description:     capability.Primitive.Description,
		ContractID:      capability.Primitive.ContractID,
		ContractSummary: capability.ContractSummary,
		Tags:            append([]string{}, capability.Primitive.Tags...),
	}
}

func toContractJSON(contract model.Contract) contractJSON {
	requirements := make([]requirementJSON, 0, len(contract.Requirements))
	for _, requirement := range contract.Requirements {
		requirements = append(requirements, requirementJSON{
			RequirementID: requirement.ID,
			Description:   requirement.Description,
			Kind:          requirement.Kind,
			Required:      requirement.Required,
		})
	}
	return contractJSON{
		ContractID:   contract.ID,
		PrimitiveID:  contract.PrimitiveID,
		Version:      contract.Version,
		Summary:      contract.Summary,
		Requirements: requirements,
	}
}

func joinKinds() string {
	kinds := outcome.Kinds()
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, string(kind))
	}
	return strings.Join(parts, ", ")
}

// redact removes a secret from an error before it reaches stderr.
func redact(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "***"))
}
