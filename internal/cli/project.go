package cli

import (
	"context"
	"errors"
	"flag"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
)

// projectUsage is the `reusery project` sub-command help.
const projectUsage = `reusery project - bounded project context and remembered decisions

	Usage:
  reusery project scan [--source local|github_public] [--root DIR] [--github OWNER/REPO] [--ref REF] [--subdir DIR] [--format text|json]
  reusery project show --project-id ID [--format text|json]
  reusery project remember --project-id ID --primitive-id ID --candidate-id ID --reason REASON [--resolution-id N] [--format text|json]
  reusery project forget --project-id ID --preference-id N [--format text|json]
  reusery project history --project-id ID [--limit N] [--format text|json]

  Sources are mutually exclusive. A local scan never stores its filesystem
  path; a public scan never reads a private repository.
`

// commandProject dispatches the project sub-commands.
func (a *App) commandProject(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.errorf("reusery project: sub-command is required")
		_, _ = a.Stderr.Write([]byte(projectUsage))
		return ExitUsage
	}
	switch args[0] {
	case "scan":
		return a.commandProjectScan(ctx, args[1:])
	case "show":
		return a.commandProjectShow(ctx, args[1:])
	case "remember":
		return a.commandProjectRemember(ctx, args[1:])
	case "forget":
		return a.commandProjectForget(ctx, args[1:])
	case "history":
		return a.commandProjectHistory(ctx, args[1:])
	default:
		a.errorf("reusery project: unknown sub-command %q", args[0])
		_, _ = a.Stderr.Write([]byte(projectUsage))
		return ExitUsage
	}
}

// projectService opens the store and builds the project-context service.
func (a *App) projectService(ctx context.Context) (*project.Service, func(), int) {
	store, _, closeStore, code := a.openStore(ctx)
	if code != ExitOK {
		return nil, func() {}, code
	}
	service, err := a.NewProjectService(store, a.Clock)
	if err != nil {
		closeStore()
		a.errorf("reusery project: %v", err)
		return nil, func() {}, ExitError
	}
	return service, closeStore, ExitOK
}

// commandProjectScan derives a bounded fingerprint and stores the derived
// project facts. It never stores the local root.
func (a *App) commandProjectScan(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("project scan", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	source := flags.String("source", "local", "project source: local or github_public")
	root := flags.String("root", ".", "local project root (local source only)")
	github := flags.String("github", "", "public GitHub repository as owner/repo (github_public source only)")
	ref := flags.String("ref", "", "git ref to resolve (github_public source only)")
	subdir := flags.String("subdir", "", "repository subdirectory (github_public source only)")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery project scan: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}
	kind := project.SourceKind(*source)
	if !project.ValidSourceKind(kind) {
		a.errorf("reusery project scan: unsupported --source %q (want local or github_public)", *source)
		return ExitUsage
	}
	// Exactly one source: a default value must not silently choose the other.
	rootSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "root" {
			rootSet = true
		}
	})
	switch kind {
	case project.SourceLocal:
		if *github != "" {
			a.errorf("reusery project scan: --github cannot be combined with --source local")
			return ExitUsage
		}
	case project.SourceGitHubPublic:
		if *github == "" {
			a.errorf("reusery project scan: --github OWNER/REPO is required with --source github_public")
			return ExitUsage
		}
		if rootSet {
			a.errorf("reusery project scan: --root cannot be combined with --source github_public")
			return ExitUsage
		}
	}

	service, closeService, code := a.projectService(ctx)
	if code != ExitOK {
		return code
	}
	defer closeService()

	var (
		result project.ScanResult
		err    error
	)
	if kind == project.SourceLocal {
		result, err = service.ScanLocal(ctx, *root)
	} else {
		result, err = service.ScanPublicGitHub(ctx, *github, *ref, *subdir)
	}
	if err != nil {
		a.errorf("reusery project scan: %v", err)
		return projectExitCode(err)
	}

	if *format == "json" {
		return a.writeJSON(scanToJSON(result))
	}
	a.writeScanText(result)
	return ExitOK
}

func (a *App) writeScanText(result project.ScanResult) {
	a.out("project_id: %s\n", result.Project.ID)
	a.out("name: %s\n", result.Project.Name)
	a.out("source: %s\n", result.Project.SourceKind)
	if result.Project.SourceLocator != "" {
		a.out("source_locator: %s\n", result.Project.SourceLocator)
	}
	if result.SourceRevision != "" {
		a.out("source_revision: %s\n", result.SourceRevision)
	}
	a.out("fingerprint: %s\n", result.FingerprintSHA256)
	if result.Reused {
		a.out("stored: existing fingerprint reused (no duplicate)\n")
	}
	for _, warning := range result.Warnings {
		a.out("warning: %s\n", warning)
	}
	a.out("modules: %d\n", len(result.Fingerprint.Modules))
	for _, module := range result.Fingerprint.Modules {
		direct, indirect := 0, 0
		for _, requirement := range module.Requirements {
			if requirement.Indirect {
				indirect++
			} else {
				direct++
			}
		}
		a.out("  %s go=%s direct=%d indirect=%d replacements=%d\n",
			module.ModulePath, module.GoVersion, direct, indirect, len(module.Replacements))
	}
	a.errOut("%d module(s), %d warning(s)\n", len(result.Fingerprint.Modules), len(result.Warnings))
}

// commandProjectShow inspects a stored project without ever printing a
// filesystem path.
func (a *App) commandProjectShow(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("project show", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	projectID := flags.String("project-id", "", "project identity derived from a scan")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *projectID == "" {
		a.errorf("reusery project show: --project-id is required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery project show: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	service, closeService, code := a.projectService(ctx)
	if code != ExitOK {
		return code
	}
	defer closeService()

	view, err := service.Get(ctx, *projectID, project.DefaultHistoryLimit)
	if err != nil {
		a.errorf("reusery project show: %v", err)
		return projectExitCode(err)
	}

	if *format == "json" {
		return a.writeJSON(viewToJSON(view))
	}
	a.out("project_id: %s\n", view.Project.ID)
	a.out("name: %s\n", view.Project.Name)
	a.out("source: %s\n", view.Project.SourceKind)
	if view.Project.SourceLocator != "" {
		a.out("source_locator: %s\n", view.Project.SourceLocator)
	}
	if view.SourceRevision != "" {
		a.out("source_revision: %s\n", view.SourceRevision)
	}
	a.out("fingerprint: %s\n", view.FingerprintSHA256)
	a.out("modules:\n")
	for _, module := range view.Fingerprint.Modules {
		a.out("  %s go=%s toolchain=%s\n", module.ModulePath, module.GoVersion, module.Toolchain)
		for _, requirement := range module.Requirements {
			kind := "direct"
			if requirement.Indirect {
				kind = "indirect"
			}
			a.out("    %s %s %s\n", requirement.ModulePath, requirement.Version, kind)
		}
		for _, replacement := range module.Replacements {
			if replacement.LocalReplace {
				a.out("    replace %s => <local module>\n", replacement.OldModulePath)
				continue
			}
			a.out("    replace %s %s => %s %s\n",
				replacement.OldModulePath, replacement.OldVersion, replacement.NewModulePath, replacement.NewVersion)
		}
	}
	a.out("preferences:\n")
	if len(view.Preferences) == 0 {
		a.out("  (none)\n")
	}
	for _, preference := range view.Preferences {
		a.out("  %d %s", preference.ID, preference.Kind)
		if preference.CandidateID != "" {
			a.out(" candidate=%s", preference.CandidateID)
		}
		if preference.TextValue != "" {
			a.out(" value=%s", preference.TextValue)
		}
		if preference.IntValue != nil {
			a.out(" value=%d", *preference.IntValue)
		}
		a.out("\n")
	}
	if view.Forgotten > 0 {
		a.out("forgotten: %d\n", view.Forgotten)
	}
	a.out("recent_decisions: %d\n", len(view.Recent))
	return ExitOK
}

// commandProjectRemember derives and stores one explicit memory.
func (a *App) commandProjectRemember(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("project remember", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	projectID := flags.String("project-id", "", "project identity derived from a scan")
	primitiveID := flags.String("primitive-id", "", "primitive the preference is scoped to")
	candidateID := flags.String("candidate-id", "", "candidate the preference is about")
	reason := flags.String("reason", "", "structured Packet 7 feedback reason")
	resolutionID := flags.Int64("resolution-id", 0, "resolution the memory was derived from")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *projectID == "" || *primitiveID == "" || *candidateID == "" || *reason == "" {
		a.errorf("reusery project remember: --project-id, --primitive-id, --candidate-id and --reason are required")
		return ExitUsage
	}
	if !policy.SupportedFeedback(policy.FeedbackReason(*reason)) {
		a.errorf("reusery project remember: unsupported --reason %q", *reason)
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery project remember: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	service, closeService, code := a.projectService(ctx)
	if code != ExitOK {
		return code
	}
	defer closeService()

	stored, created, err := service.Remember(ctx, project.RememberRequest{
		ProjectID:          *projectID,
		PrimitiveID:        *primitiveID,
		CandidateID:        *candidateID,
		Reason:             policy.FeedbackReason(*reason),
		SourceResolutionID: *resolutionID,
	})
	if err != nil {
		a.errorf("reusery project remember: %v", err)
		return projectExitCode(err)
	}

	if *format == "json" {
		return a.writeJSON(map[string]any{
			"id":            stored.ID,
			"project_id":    stored.ProjectID,
			"kind":          stored.Kind,
			"primitive_id":  stored.PrimitiveID,
			"candidate_id":  stored.CandidateID,
			"text_value":    stored.TextValue,
			"int_value":     stored.IntValue,
			"source_reason": stored.SourceReason,
			"created":       created,
			"recorded_at":   stored.RecordedAt,
		})
	}
	if created {
		a.out("remembered %d %s for %s\n", stored.ID, stored.Kind, stored.ProjectID)
	} else {
		a.out("existing %d %s already active for %s\n", stored.ID, stored.Kind, stored.ProjectID)
	}
	return ExitOK
}

// commandProjectForget revokes a preference without deleting it.
func (a *App) commandProjectForget(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("project forget", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	projectID := flags.String("project-id", "", "project identity derived from a scan")
	preferenceID := flags.Int64("preference-id", 0, "preference to revoke")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *projectID == "" || *preferenceID <= 0 {
		a.errorf("reusery project forget: --project-id and a positive --preference-id are required")
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery project forget: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	service, closeService, code := a.projectService(ctx)
	if code != ExitOK {
		return code
	}
	defer closeService()

	stored, err := service.Forget(ctx, *projectID, *preferenceID)
	if err != nil {
		a.errorf("reusery project forget: %v", err)
		return projectExitCode(err)
	}

	if *format == "json" {
		return a.writeJSON(map[string]any{
			"id":           stored.ID,
			"project_id":   stored.ProjectID,
			"kind":         stored.Kind,
			"forgotten_at": stored.ForgottenAt,
			"active":       stored.Active(),
		})
	}
	a.out("revoked preference %d (%s); the historical row is kept\n", stored.ID, stored.Kind)
	return ExitOK
}

// commandProjectHistory lists bounded, newest-first decision history.
func (a *App) commandProjectHistory(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("project history", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	projectID := flags.String("project-id", "", "project identity derived from a scan")
	limit := flags.Int("limit", project.DefaultHistoryLimit, "maximum resolutions to return")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *projectID == "" {
		a.errorf("reusery project history: --project-id is required")
		return ExitUsage
	}
	if *limit < 1 || *limit > project.MaxHistoryLimit {
		a.errorf("reusery project history: --limit must be between 1 and %d", project.MaxHistoryLimit)
		return ExitUsage
	}
	if !validFormat(*format) {
		a.errorf("reusery project history: invalid --format %q (want text or json)", *format)
		return ExitUsage
	}

	service, closeService, code := a.projectService(ctx)
	if code != ExitOK {
		return code
	}
	defer closeService()

	history, err := service.ListHistory(ctx, *projectID, *limit)
	if err != nil {
		a.errorf("reusery project history: %v", err)
		return projectExitCode(err)
	}

	if *format == "json" {
		entries := make([]map[string]any, 0, len(history))
		for _, resolution := range history {
			entries = append(entries, resolutionToMap(resolution))
		}
		return a.writeJSON(map[string]any{"project_id": *projectID, "resolutions": entries})
	}
	if len(history) == 0 {
		a.out("resolutions: (none)\n")
		return ExitOK
	}
	for _, entry := range history {
		resolution := entry.Resolution
		specimen := resolution.SpecimenID
		if specimen == "" {
			specimen = "-"
		}
		a.out("%d %s outcome=%s policy=%s specimen=%s\n",
			entry.ID, resolution.ResolvedAt.Format("2006-01-02T15:04:05Z07:00"),
			resolution.Outcome, resolution.PolicyID, specimen)
	}
	a.errOut("%d resolution(s)\n", len(history))
	return ExitOK
}

// resolutionToMap projects a stored Resolution for JSON output, including
// its storage identity and the Packet 10 project linkage when present.
func resolutionToMap(entry project.ProjectResolution) map[string]any {
	resolution := entry.Resolution
	payload := map[string]any{
		"resolution_id":        entry.ID,
		"primitive_id":         resolution.PrimitiveID,
		"contract_id":          resolution.ContractID,
		"outcome":              resolution.Outcome,
		"specimen_id":          resolution.SpecimenID,
		"policy_id":            resolution.PolicyID,
		"resolved_at":          resolution.ResolvedAt,
		"reasons":              resolution.Reasons,
		"unknowns":             resolution.Unknowns,
		"project_id":           resolution.ProjectID,
		"project_context_hash": resolution.ProjectContextHash,
	}
	return payload
}

func scanToJSON(result project.ScanResult) map[string]any {
	modules := make([]map[string]any, 0, len(result.Fingerprint.Modules))
	for _, module := range result.Fingerprint.Modules {
		requirements := make([]map[string]any, 0, len(module.Requirements))
		for _, requirement := range module.Requirements {
			requirements = append(requirements, map[string]any{
				"module_path": requirement.ModulePath,
				"version":     requirement.Version,
				"indirect":    requirement.Indirect,
			})
		}
		replacements := make([]map[string]any, 0, len(module.Replacements))
		for _, replacement := range module.Replacements {
			replacements = append(replacements, map[string]any{
				"old_module_path":   replacement.OldModulePath,
				"old_version":       replacement.OldVersion,
				"new_module_path":   replacement.NewModulePath,
				"new_version":       replacement.NewVersion,
				"local_replacement": replacement.LocalReplace,
			})
		}
		modules = append(modules, map[string]any{
			"module_path":  module.ModulePath,
			"go_version":   module.GoVersion,
			"toolchain":    module.Toolchain,
			"requirements": requirements,
			"replacements": replacements,
		})
	}
	return map[string]any{
		"project_id":         result.Project.ID,
		"name":               result.Project.Name,
		"source_kind":        result.Project.SourceKind,
		"source_locator":     result.Project.SourceLocator,
		"source_revision":    result.SourceRevision,
		"fingerprint_sha256": result.FingerprintSHA256,
		"schema_version":     result.Fingerprint.SchemaVersion,
		"language":           result.Fingerprint.Language,
		"reused":             result.Reused,
		"warnings":           result.Warnings,
		"modules":            modules,
	}
}

func viewToJSON(view project.View) map[string]any {
	payload := scanToJSON(project.ScanResult{
		Project:           view.Project,
		Fingerprint:       view.Fingerprint,
		FingerprintSHA256: view.FingerprintSHA256,
		SourceRevision:    view.SourceRevision,
	})
	preferences := make([]map[string]any, 0, len(view.Preferences))
	for _, preference := range view.Preferences {
		preferences = append(preferences, map[string]any{
			"id":            preference.ID,
			"kind":          preference.Kind,
			"primitive_id":  preference.PrimitiveID,
			"candidate_id":  preference.CandidateID,
			"text_value":    preference.TextValue,
			"int_value":     preference.IntValue,
			"source_reason": preference.SourceReason,
			"recorded_at":   preference.RecordedAt,
		})
	}
	recent := make([]map[string]any, 0, len(view.Recent))
	for _, resolution := range view.Recent {
		recent = append(recent, resolutionToMap(resolution))
	}
	payload["active_preferences"] = preferences
	payload["forgotten_preferences"] = view.Forgotten
	payload["recent_decisions"] = recent
	return payload
}

// projectExitCode keeps the exit-code contract: structural problems are usage,
// storage and provider failures are execution.
func projectExitCode(err error) int {
	switch {
	case errors.Is(err, project.ErrUnsupportedProjectContext),
		errors.Is(err, project.ErrInvalidProjectContext),
		errors.Is(err, project.ErrProjectRootUnconfigured),
		errors.Is(err, project.ErrUnsupportedSourceKind),
		errors.Is(err, project.ErrInvalidRepository),
		errors.Is(err, project.ErrUnknownFeedbackReason),
		errors.Is(err, project.ErrPreferenceUnsupported),
		errors.Is(err, project.ErrBoundsExceeded),
		errors.Is(err, policy.ErrInvalidPolicy):
		return ExitUsage
	default:
		return ExitError
	}
}
