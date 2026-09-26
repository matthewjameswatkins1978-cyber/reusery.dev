// Package project is Reusery's bounded project-context layer.
//
// It answers "what fits THIS project?" rather than "what might fit an
// imaginary generic project?". The guiding principle is:
//
//	PROJECT CONTEXT MAY CHANGE FIT.
//	IT MUST NOT CHANGE TRUTH.
//
// A project fingerprint is derived from manifests only. It is never Evidence,
// it can never create behavioural PASS, and it can never turn an unknown
// requirement into a satisfied one. It may add a review requirement, add an
// inspectable trade-off, or break a tie between candidates that were already
// implementation-eligible.
//
// The package depends on interfaces, never on PostgreSQL, and it never
// executes project code, never shells out and never invokes git.
package project

import (
	"errors"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// SchemaVersion is the only fingerprint and context schema supported.
const SchemaVersion = 1

// LanguageGo is the only language Packet 10 can honestly claim to fingerprint.
// A project with neither go.mod nor go.work is unsupported rather than
// guessed at.
const LanguageGo = "go"

// SourceKind distinguishes where a project's manifest facts came from.
// Only two exist in Packet 10; private repositories are Packet 13 scope.
type SourceKind string

const (
	// SourceLocal is a project scanned from an explicitly configured local
	// root. Its filesystem path is never persisted.
	SourceLocal SourceKind = "local"
	// SourceGitHubPublic is a public repository read over the anonymous
	// GitHub REST API without cloning.
	SourceGitHubPublic SourceKind = "github_public"
)

// SupportedSourceKinds returns the closed vocabulary in authored order.
func SupportedSourceKinds() []SourceKind {
	return []SourceKind{SourceLocal, SourceGitHubPublic}
}

// ValidSourceKind reports whether a kind is part of the closed vocabulary.
func ValidSourceKind(kind SourceKind) bool {
	for _, supported := range SupportedSourceKinds() {
		if kind == supported {
			return true
		}
	}
	return false
}

// Scanner bounds. These are hard: the scanner never walks a repository.
const (
	// MaxWorkspaceModules bounds how many go.mod files one scan may read.
	MaxWorkspaceModules = 32
	// MaxManifestBytes bounds a single manifest.
	MaxManifestBytes = 512 * 1024
	// MaxManifestFiles is MaxWorkspaceModules plus the workspace file itself.
	MaxManifestFiles = MaxWorkspaceModules + 1
	// MaxTotalManifestBytes bounds everything one scan reads.
	MaxTotalManifestBytes = 8 * 1024 * 1024
	// ScanTimeout bounds a whole local scan.
	ScanTimeout = 5 * time.Second
)

// Public GitHub scanner bounds.
const (
	// MaxGitHubRequests bounds every HTTP request a public scan may make.
	MaxGitHubRequests = 40
	// GitHubScanTimeout bounds a whole public scan.
	GitHubScanTimeout = 15 * time.Second
	// MaxGitHubResponseBytes bounds a single HTTP response body.
	MaxGitHubResponseBytes = 1 << 20
	// MaxGitHubDecodedManifest bounds one decoded manifest over HTTP.
	MaxGitHubDecodedManifest = 512 * 1024
)

// Decision bounds. Memories are never silently dropped: exceeding one fails.
const (
	// MaxCandidatesPerDecision bounds a project-aware candidate set.
	MaxCandidatesPerDecision = 24
	// MaxActivePreferences bounds how many remembered preferences one
	// decision may apply.
	MaxActivePreferences = 100
	// MaxRecentResolutions bounds the history returned by context inspection.
	MaxRecentResolutions = 100
	// DefaultHistoryLimit and MaxHistoryLimit bound CLI history listings.
	DefaultHistoryLimit = 50
	MaxHistoryLimit     = 100
	// DefaultMCPHistory is the smaller bound an agent pays for by default.
	DefaultMCPHistory = 20
)

// Project-context failures. Each maps to a distinct, safe tool/CLI error.
var (
	// ErrUnsupportedProjectContext means the scanned project has neither a
	// go.mod nor a go.work: Packet 10 is Go-first and will not guess.
	ErrUnsupportedProjectContext = errors.New("project: unsupported project context")
	// ErrInvalidProjectContext means a manifest was malformed or a bound was
	// exceeded or a path escaped the configured root.
	ErrInvalidProjectContext = errors.New("project: invalid project context")
	// ErrProjectRootUnconfigured means a local scan was requested without a
	// configured project root.
	ErrProjectRootUnconfigured = errors.New("project: project root is not configured")
	// ErrProjectNotFound means no such project is stored.
	ErrProjectNotFound = errors.New("project: project does not exist")
	// ErrFingerprintNotFound means a project has no stored fingerprint yet.
	ErrFingerprintNotFound = errors.New("project: project has no fingerprint")
	// ErrPreferenceNotFound means no such preference is stored.
	ErrPreferenceNotFound = errors.New("project: preference does not exist")
	// ErrUnsupportedSourceKind is raised for anything but local and
	// github_public.
	ErrUnsupportedSourceKind = errors.New("project: unsupported source kind")
	// ErrPrivateRepositoryUnsupported is raised before any content is read
	// when a repository is private, missing or otherwise not publicly
	// readable. Packet 13 owns private access; Packet 10 must not stumble
	// into it.
	ErrPrivateRepositoryUnsupported = errors.New("project: not_found_or_private_repository_unsupported")
	// ErrInvalidRepositoryRefusalises a malformed GitHub repository selector.
	ErrInvalidRepository = errors.New("project: repository must be owner/repo")
	// ErrBoundsExceeded means a request asked for more than the packet allows.
	ErrBoundsExceeded = errors.New("project: request exceeds a bound")
	// ErrPreferenceUnsupported is raised when a memory cannot be derived from
	// stored facts, for example a non-archived candidate reported as
	// archived_project or an unknown dependency count.
	ErrPreferenceUnsupported = errors.New("project: preference cannot be derived from stored facts")
	// ErrUnknownFeedbackReason means the caller used a reason outside the
	// Packet 7 vocabulary.
	ErrUnknownFeedbackReason = errors.New("project: unsupported feedback reason")
	// ErrInvalidRequest means the decision request itself was structurally
	// wrong, for example a refine with no feedback.
	ErrInvalidRequest = errors.New("project: invalid request")
)

// Project is the durable identity of one project.
//
// A local project's ID comes from its module paths, so two checkouts of the
// same module identify as the same logical project and no filesystem path is
// ever stored.
type Project struct {
	ID            string
	Name          string
	SourceKind    SourceKind
	SourceLocator string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GoRequirement is one entry of a module's require block.
type GoRequirement struct {
	ModulePath string `json:"module_path"`
	Version    string `json:"version"`
	Indirect   bool   `json:"indirect"`
}

// GoReplacement is one entry of a module's replace block.
//
// A local replacement records only that a replacement exists. The filesystem
// path is deliberately absent: it matters to fit, and the path does not.
type GoReplacement struct {
	OldModulePath string `json:"old_module_path"`
	OldVersion    string `json:"old_version,omitempty"`
	NewModulePath string `json:"new_module_path,omitempty"`
	NewVersion    string `json:"new_version,omitempty"`
	LocalReplace  bool   `json:"local_replacement"`
}

// GoModule is the manifest fact for one module.
type GoModule struct {
	ModulePath   string          `json:"module_path"`
	GoVersion    string          `json:"go_version,omitempty"`
	Toolchain    string          `json:"toolchain,omitempty"`
	Requirements []GoRequirement `json:"requirements"`
	Replacements []GoReplacement `json:"replacements"`
}

// Fingerprint is the versioned, typed set of derived manifest facts.
//
// It contains no source contents, no README, no AST, no symbols, no
// environment dump and no local path. ObservedAt, storage IDs and scan
// warnings are deliberately outside this struct so they can never enter the
// canonical hash.
type Fingerprint struct {
	SchemaVersion int        `json:"schema_version"`
	Language      string     `json:"language"`
	Modules       []GoModule `json:"modules"`
}

// PreferenceKind is the closed vocabulary of explicit project memories.
type PreferenceKind string

const (
	PrefExcludeCandidate PreferenceKind = "exclude_candidate"
	PrefMaxDirectDeps    PreferenceKind = "max_direct_dependencies"
	PrefDenyLicence      PreferenceKind = "deny_licence"
	PrefAvoidDependency  PreferenceKind = "avoid_dependency"
	PrefAvoidReference   PreferenceKind = "avoid_reference"
	PrefDenyArchived     PreferenceKind = "deny_archived"
)

// PreferenceKinds returns the supported kinds in authored order.
func PreferenceKinds() []PreferenceKind {
	return []PreferenceKind{
		PrefExcludeCandidate,
		PrefMaxDirectDeps,
		PrefDenyLicence,
		PrefAvoidDependency,
		PrefAvoidReference,
		PrefDenyArchived,
	}
}

// ValidPreferenceKind reports whether a kind is part of the closed vocabulary.
func ValidPreferenceKind(kind PreferenceKind) bool {
	for _, supported := range PreferenceKinds() {
		if kind == supported {
			return true
		}
	}
	return false
}

// Preference is an explicit, reversible project memory.
//
// It is a human or agent steering decision. It is not Evidence, it carries no
// EvidenceIDs, and it is never inferred automatically from a refine call or an
// outcome event.
type Preference struct {
	ID                 int64
	ProjectID          string
	Kind               PreferenceKind
	PrimitiveID        string
	CandidateID        string
	TextValue          string
	IntValue           *int
	SourceReason       policy.FeedbackReason
	SourceResolutionID int64
	RecordedAt         time.Time
	// ForgottenAt is set when the preference is revoked. The row survives so
	// history is preserved; an active preference has a zero ForgottenAt.
	ForgottenAt time.Time
}

// Active reports whether the preference still influences decisions.
func (p Preference) Active() bool { return p.ForgottenAt.IsZero() }

// PreferenceEffect is one active preference as it appears inside an immutable
// context snapshot.
type PreferenceEffect struct {
	PreferenceID int64                 `json:"preference_id"`
	Kind         PreferenceKind        `json:"kind"`
	PrimitiveID  string                `json:"primitive_id,omitempty"`
	CandidateID  string                `json:"candidate_id,omitempty"`
	TextValue    string                `json:"text_value,omitempty"`
	IntValue     *int                  `json:"int_value,omitempty"`
	SourceReason policy.FeedbackReason `json:"source_reason,omitempty"`
}

// Context is the immutable materialised snapshot of what one decision saw.
//
// It contains only fingerprint identity and active preference effects. No raw
// source, no local path. Its SHA-256 is the project context hash recorded on
// the Resolution, so a historical decision keeps resolving to its own context
// after preferences or the fingerprint later change.
type Context struct {
	SchemaVersion     int                `json:"schema_version"`
	ProjectID         string             `json:"project_id"`
	Language          string             `json:"language"`
	SourceKind        SourceKind         `json:"source_kind"`
	SourceRevision    string             `json:"source_revision,omitempty"`
	FingerprintSHA256 string             `json:"fingerprint_sha256"`
	Preferences       []PreferenceEffect `json:"preferences"`
}

// ProjectEffect is one bounded project fact that touched a candidate during a
// decision. It explains which project context mattered, per candidate.
type ProjectEffect struct {
	SpecimenID    string                 `json:"specimen_id"`
	DependencyFit resolver.DependencyFit `json:"dependency_fit"`
	ModulePath    string                 `json:"module_path,omitempty"`
	ModuleVersion string                 `json:"module_version,omitempty"`
	// Tradeoff is the deterministic sentence the fit contributes to the
	// assessment. Empty when the fit adds no trade-off.
	Tradeoff string `json:"tradeoff,omitempty"`
}

// ScanResult is what a scan produced: the project, its fingerprint, the
// storage-observed values and any safe warnings about skipped input.
type ScanResult struct {
	Project           Project
	Fingerprint       Fingerprint
	FingerprintSHA256 string
	SourceRevision    string
	// Warnings describes input that was skipped for safety, for example
	// workspace modules outside the configured root. A warning never contains
	// a filesystem path.
	Warnings []string
	// Reused reports that an identical fingerprint already existed, so a
	// rescan of an unchanged project creates no duplicate row.
	Reused bool
}

// StoredFingerprint is a persisted fingerprint. The JSON column holds exactly
// the typed Fingerprint schema; it is never unvalidated free-form JSON.
type StoredFingerprint struct {
	ID             int64
	ProjectID      string
	SchemaVersion  int
	SHA256         string
	Fingerprint    Fingerprint
	SourceRevision string
	ObservedAt     time.Time
}

// ProjectResolution is one historical decision with its storage identity.
// The id lets an agent move from history to reusery_inspect_resolution.
type ProjectResolution struct {
	ID         int64
	Resolution model.Resolution
}

// StoredContext is a persisted immutable context snapshot.
type StoredContext struct {
	Hash          string
	ProjectID     string
	FingerprintID int64
	Context       Context
	CreatedAt     time.Time
}
