package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
)

// The four Packet 10 project tools. They extend the frozen Packet 9 surface
// additively: no Packet 9 tool is renamed and no filesystem-browsing tool is
// introduced.
const (
	ToolProjectScan     = "reusery_project_scan"
	ToolProjectContext  = "reusery_project_context"
	ToolProjectRemember = "reusery_project_remember"
	ToolProjectForget   = "reusery_project_forget"
)

// Project source values accepted by reusery_project_scan.
const (
	projectSourceLocal        = "local"
	projectSourceGitHubPublic = "github_public"
)

// ProjectScanInput requests a bounded fingerprint.
//
// There is deliberately no path, root or directory field: a local scan uses
// the project root configured when `reusery mcp` started, so an agent can
// never point the server at an arbitrary part of the filesystem.
type ProjectScanInput struct {
	Source     string `json:"source,omitempty" jsonschema:"local or github_public. Defaults to local."`
	Repository string `json:"repository,omitempty" jsonschema:"Public repository as owner/repo. Required for github_public."`
	Ref        string `json:"ref,omitempty" jsonschema:"Optional git ref resolved to an immutable commit SHA."`
	Subdir     string `json:"subdir,omitempty" jsonschema:"Optional repository-relative subdirectory."`
}

// ProjectModuleSummary is one module line of a fingerprint.
type ProjectModuleSummary struct {
	ModulePath string `json:"module_path" jsonschema:"Go module path."`
	GoVersion  string `json:"go_version" jsonschema:"Declared Go version, or the workspace's."`
	Toolchain  string `json:"toolchain" jsonschema:"Declared toolchain, or the workspace's."`
}

// ProjectCounts summarises a fingerprint without dumping every dependency.
type ProjectCounts struct {
	Modules              int `json:"modules" jsonschema:"Modules in the fingerprint."`
	DirectDependencies   int `json:"direct_dependencies" jsonschema:"Sum of direct requirements across modules."`
	IndirectDependencies int `json:"indirect_dependencies" jsonschema:"Sum of indirect requirements across modules."`
	Replacements         int `json:"replacements" jsonschema:"Sum of replace directives across modules."`
}

// ProjectScanResult is the compact reusery_project_scan output. Fuller detail
// is available from reusery_project_context.
type ProjectScanResult struct {
	ProjectID         string                 `json:"project_id" jsonschema:"Derived project identity; never a filesystem path."`
	Name              string                 `json:"name" jsonschema:"Cosmetic display name."`
	SourceKind        project.SourceKind     `json:"source_kind" jsonschema:"local or github_public."`
	SourceLocator     string                 `json:"source_locator" jsonschema:"Public repository and subdirectory; always empty for a local project."`
	SourceRevision    string                 `json:"source_revision" jsonschema:"Immutable commit SHA for a public scan; empty for local."`
	FingerprintSHA256 string                 `json:"fingerprint_sha256" jsonschema:"SHA-256 of the canonical manifest fingerprint."`
	Modules           []ProjectModuleSummary `json:"modules" jsonschema:"One line per module; never null."`
	Counts            ProjectCounts          `json:"counts" jsonschema:"Aggregate manifest counts."`
	Warnings          []string               `json:"warnings" jsonschema:"Safe notes about skipped input; never contains a path."`
}

// ActivePreference is one remembered memory as it appears to an agent.
type ActivePreference struct {
	ID           int64                  `json:"id" jsonschema:"Storage identity."`
	Kind         project.PreferenceKind `json:"kind" jsonschema:"Preference kind."`
	PrimitiveID  string                 `json:"primitive_id,omitempty" jsonschema:"Primitive scope, when the memory is scoped."`
	CandidateID  string                 `json:"candidate_id,omitempty" jsonschema:"Candidate the memory is about."`
	TextValue    string                 `json:"text_value,omitempty" jsonschema:"Exact value, for example a denied licence."`
	IntValue     *int                   `json:"int_value,omitempty" jsonschema:"Numeric ceiling, for a dependency limit."`
	SourceReason string                 `json:"source_reason,omitempty" jsonschema:"Structured feedback reason that produced it."`
	RecordedAt   string                 `json:"recorded_at,omitempty" format:"date-time" jsonschema:"When it was remembered."`
}

// ResolutionSummary is one historical decision, bounded and compact.
type ResolutionSummary struct {
	ResolutionID       int64  `json:"resolution_id" jsonschema:"Storage identity; pass to reusery_inspect_resolution."`
	Outcome            string `json:"outcome" jsonschema:"reuse, adapt, depend, reference or build_locally."`
	SpecimenID         string `json:"specimen_id,omitempty" jsonschema:"Selected specimen, empty for build_locally."`
	PolicyID           string `json:"policy_id" jsonschema:"Policy that justified the decision."`
	PrimitiveID        string `json:"primitive_id" jsonschema:"Primitive resolved."`
	ResolvedAt         string `json:"resolved_at" format:"date-time" jsonschema:"When the decision was recorded."`
	ProjectContextHash string `json:"project_context_hash,omitempty" jsonschema:"Immutable context snapshot the decision saw."`
}

// ProjectContextInput selects one project's bounded context.
type ProjectContextInput struct {
	ProjectID string `json:"project_id" minLength:"1" jsonschema:"Derived project identity."`
	Limit     int    `json:"limit,omitempty" minimum:"1" maximum:"100" jsonschema:"Recent resolutions to return; defaults to 20."`
}

// ProjectContextResult is the reusery_project_context output: fingerprint
// summary, active preferences and recent decisions. No raw source, no path.
type ProjectContextResult struct {
	ProjectID         string                 `json:"project_id" jsonschema:"Derived project identity."`
	Name              string                 `json:"name" jsonschema:"Cosmetic display name."`
	SourceKind        project.SourceKind     `json:"source_kind" jsonschema:"local or github_public."`
	SourceLocator     string                 `json:"source_locator" jsonschema:"Public repository and subdirectory; empty for local."`
	SourceRevision    string                 `json:"source_revision" jsonschema:"Immutable commit SHA for a public scan."`
	FingerprintSHA256 string                 `json:"fingerprint_sha256" jsonschema:"SHA-256 of the canonical manifest fingerprint."`
	Language          string                 `json:"language" jsonschema:"Fingerprint language."`
	Modules           []ProjectModuleSummary `json:"modules" jsonschema:"One line per module; never null."`
	Counts            ProjectCounts          `json:"counts" jsonschema:"Aggregate manifest counts."`
	ActivePreferences []ActivePreference     `json:"active_preferences" jsonschema:"Memories that still influence decisions; never null."`
	Forgotten         int                    `json:"forgotten" jsonschema:"Revoked memories kept for history."`
	RecentResolutions []ResolutionSummary    `json:"recent_resolutions" jsonschema:"Newest-first decision history; never null."`
}

// ProjectRememberInput asks Reusery to derive and store one explicit memory.
// The caller supplies a structured reason, never a preference value: the value
// is derived from stored candidate facts.
type ProjectRememberInput struct {
	ProjectID          string `json:"project_id" minLength:"1" jsonschema:"Derived project identity."`
	PrimitiveID        string `json:"primitive_id" minLength:"1" jsonschema:"Primitive the memory is scoped to."`
	CandidateID        string `json:"candidate_id" minLength:"1" jsonschema:"Candidate the memory is about."`
	Reason             string `json:"reason" jsonschema:"not_quite, too_many_dependencies, licence_not_allowed, avoid_dependency, avoid_reference or archived_project."`
	SourceResolutionID int64  `json:"source_resolution_id,omitempty" jsonschema:"Resolution the memory was derived from."`
}

// ProjectRememberResult acknowledges one remembered memory.
type ProjectRememberResult struct {
	ID         int64                  `json:"id" jsonschema:"Storage identity."`
	ProjectID  string                 `json:"project_id" jsonschema:"Project the memory belongs to."`
	Kind       project.PreferenceKind `json:"kind" jsonschema:"Derived preference kind."`
	Effect     string                 `json:"effect" jsonschema:"Deterministic description of what the memory changes."`
	Created    bool                   `json:"created" jsonschema:"False when an identical active memory already existed."`
	RecordedAt string                 `json:"recorded_at" format:"date-time" jsonschema:"When it was remembered."`
}

// ProjectForgetInput revokes a remembered memory without deleting it.
type ProjectForgetInput struct {
	ProjectID    string `json:"project_id" minLength:"1" jsonschema:"Derived project identity."`
	PreferenceID int64  `json:"preference_id" minimum:"1" jsonschema:"Storage identity of the preference."`
}

// ProjectForgetResult acknowledges a revocation. Forgetting is idempotent.
type ProjectForgetResult struct {
	ID          int64                  `json:"id" jsonschema:"Storage identity."`
	ProjectID   string                 `json:"project_id" jsonschema:"Project the memory belonged to."`
	Kind        project.PreferenceKind `json:"kind" jsonschema:"Preference kind."`
	ForgottenAt string                 `json:"forgotten_at" format:"date-time" jsonschema:"When it was revoked."`
	Active      bool                   `json:"active" jsonschema:"Always false after a forget."`
}

// handleProjectScan fingerprints a project and stores the derived facts.
func (d Dependencies) handleProjectScan(ctx context.Context, _ *mcp.CallToolRequest, in ProjectScanInput) (*mcp.CallToolResult, ProjectScanResult, error) {
	var out ProjectScanResult
	if d.Projects == nil {
		return nil, out, classify(errMissingService)
	}

	source := in.Source
	if source == "" {
		source = projectSourceLocal
	}
	kind := project.SourceKind(source)
	if !project.ValidSourceKind(kind) {
		return nil, out, newToolError(CodeInvalidRequest,
			fmt.Sprintf("source must be %q or %q", projectSourceLocal, projectSourceGitHubPublic))
	}

	var (
		result project.ScanResult
		err    error
	)
	switch kind {
	case project.SourceLocal:
		if strings.TrimSpace(d.ProjectRoot) == "" {
			return nil, out, newToolError(CodeProjectRootUnconfigured,
				"no local project root is configured for this server; start it with reusery mcp --project-root DIR")
		}
		result, err = d.Projects.ScanLocal(ctx, d.ProjectRoot)
	default:
		if !d.ExternalOperationsEnabled {
			return nil, out, externalOperationsDisabled()
		}
		if strings.TrimSpace(in.Repository) == "" {
			return nil, out, newToolError(CodeInvalidRequest, "repository is required for a github_public scan")
		}
		result, err = d.Projects.ScanPublicGitHub(ctx, in.Repository, in.Ref, in.Subdir)
	}
	if err != nil {
		return nil, out, classify(err)
	}

	out = mapProjectScan(result)
	return summaryText(fmt.Sprintf(
		"Scanned %s as a %s project: %d module(s), %d direct dependencies, fingerprint %s.",
		out.Name, out.SourceKind, out.Counts.Modules, out.Counts.DirectDependencies,
		shortDigest(out.FingerprintSHA256))), out, nil
}

// handleProjectContext returns the bounded inspection view of one project.
func (d Dependencies) handleProjectContext(ctx context.Context, _ *mcp.CallToolRequest, in ProjectContextInput) (*mcp.CallToolResult, ProjectContextResult, error) {
	var out ProjectContextResult
	if d.Projects == nil {
		return nil, out, classify(errMissingService)
	}
	limit := in.Limit
	if limit == 0 {
		limit = project.DefaultMCPHistory
	}
	if limit < 1 || limit > project.MaxRecentResolutions {
		return nil, out, newToolError(CodeInvalidRequest,
			fmt.Sprintf("limit must be between 1 and %d", project.MaxRecentResolutions))
	}

	view, err := d.Projects.Get(ctx, in.ProjectID, limit)
	if err != nil {
		return nil, out, classify(err)
	}
	out = mapProjectContext(view)
	return summaryText(fmt.Sprintf(
		"%s: %d module(s), %d active preference(s), %d recent decision(s).",
		out.Name, out.Counts.Modules, len(out.ActivePreferences), len(out.RecentResolutions))), out, nil
}

// handleProjectRemember derives and stores one explicit memory.
//
// This is the ONLY way a preference is created: neither reusery_refine nor
// reusery_report_outcome ever writes memory on their own.
func (d Dependencies) handleProjectRemember(ctx context.Context, _ *mcp.CallToolRequest, in ProjectRememberInput) (*mcp.CallToolResult, ProjectRememberResult, error) {
	var out ProjectRememberResult
	if d.Projects == nil {
		return nil, out, classify(errMissingService)
	}

	stored, created, err := d.Projects.Remember(ctx, project.RememberRequest{
		ProjectID:          in.ProjectID,
		PrimitiveID:        in.PrimitiveID,
		CandidateID:        in.CandidateID,
		Reason:             policyFeedbackReason(in.Reason),
		SourceResolutionID: in.SourceResolutionID,
	})
	if err != nil {
		return nil, out, classify(err)
	}

	out = ProjectRememberResult{
		ID:         stored.ID,
		ProjectID:  stored.ProjectID,
		Kind:       stored.Kind,
		Effect:     describePreference(stored),
		Created:    created,
		RecordedAt: formatTime(stored.RecordedAt),
	}
	verb := "remembered"
	if !created {
		verb = "reused the existing active"
	}
	return summaryText(fmt.Sprintf("%s %s: %s.", verb, stored.Kind, out.Effect)), out, nil
}

// handleProjectForget revokes a memory without deleting it.
func (d Dependencies) handleProjectForget(ctx context.Context, _ *mcp.CallToolRequest, in ProjectForgetInput) (*mcp.CallToolResult, ProjectForgetResult, error) {
	var out ProjectForgetResult
	if d.Projects == nil {
		return nil, out, classify(errMissingService)
	}
	stored, err := d.Projects.Forget(ctx, in.ProjectID, in.PreferenceID)
	if err != nil {
		return nil, out, classify(err)
	}
	out = ProjectForgetResult{
		ID:          stored.ID,
		ProjectID:   stored.ProjectID,
		Kind:        stored.Kind,
		ForgottenAt: formatTime(stored.ForgottenAt),
		Active:      stored.Active(),
	}
	return summaryText(fmt.Sprintf(
		"Revoked %s preference %d; the historical row is kept so the decision history stays inspectable.",
		stored.Kind, stored.ID)), out, nil
}
