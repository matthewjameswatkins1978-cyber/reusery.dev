package project

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// finish assembles a ScanResult from a derived fingerprint and enforces the
// persistence boundary for the source locator.
//
// The local case is the important one: a local project's identity comes from
// its module paths, so a filesystem path is rejected outright rather than
// merely omitted.
func finish(f Fingerprint, kind SourceKind, locator, revision string, warnings []string) (ScanResult, error) {
	if !ValidSourceKind(kind) {
		return ScanResult{}, fmt.Errorf("%w %q", ErrUnsupportedSourceKind, string(kind))
	}
	if len(f.Modules) == 0 {
		return ScanResult{}, ErrUnsupportedProjectContext
	}
	if kind == SourceLocal && locator != "" {
		return ScanResult{}, fmt.Errorf("%w: a local project never records a filesystem path", ErrInvalidProjectContext)
	}
	if kind == SourceGitHubPublic && (strings.Contains(locator, `\`) || strings.Contains(locator, "..")) {
		return ScanResult{}, fmt.Errorf("%w: repository locator is malformed", ErrInvalidRepository)
	}

	sha, err := FingerprintHash(f)
	if err != nil {
		return ScanResult{}, err
	}
	return ScanResult{
		Project: Project{
			ID:            ProjectID(f),
			Name:          DisplayName(f),
			SourceKind:    kind,
			SourceLocator: locator,
		},
		Fingerprint:       f,
		FingerprintSHA256: sha,
		SourceRevision:    revision,
		Warnings:          nonNilStrings(warnings),
	}, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// manifestInput is one already-read manifest. Label appears only in error
// messages and is never persisted: for a local scan it is a directory name,
// for a public scan it is a repository-relative path.
type manifestInput struct {
	Label string
	Data  []byte
}

// modulesFrom turns parsed manifest bytes into typed module facts.
//
// The workspace's own go and toolchain statements are the fallback for a
// module that does not declare its own, because a workspace sets them for
// every module it uses.
func modulesFrom(inputs []manifestInput, work *modfile.WorkFile) ([]GoModule, error) {
	workspaceGo := ""
	workspaceToolchain := ""
	if work != nil {
		if work.Go != nil {
			workspaceGo = work.Go.Version
		}
		if work.Toolchain != nil {
			workspaceToolchain = work.Toolchain.Name
		}
	}

	modules := make([]GoModule, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		file, err := modfile.Parse(input.Label, input.Data, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %s is not a valid go.mod: %v",
				ErrInvalidProjectContext, input.Label, err)
		}
		if file.Module == nil || strings.TrimSpace(file.Module.Mod.Path) == "" {
			return nil, fmt.Errorf("%w: %s declares no module path",
				ErrInvalidProjectContext, input.Label)
		}
		modulePath := file.Module.Mod.Path
		if _, duplicate := seen[modulePath]; duplicate {
			continue
		}
		seen[modulePath] = struct{}{}

		goVersion := workspaceGo
		if file.Go != nil && file.Go.Version != "" {
			goVersion = file.Go.Version
		}
		toolchain := workspaceToolchain
		if file.Toolchain != nil && file.Toolchain.Name != "" {
			toolchain = file.Toolchain.Name
		}

		module := GoModule{
			ModulePath:   modulePath,
			GoVersion:    goVersion,
			Toolchain:    toolchain,
			Requirements: make([]GoRequirement, 0, len(file.Require)),
			Replacements: make([]GoReplacement, 0, len(file.Replace)),
		}
		for _, require := range file.Require {
			module.Requirements = append(module.Requirements, GoRequirement{
				ModulePath: require.Mod.Path,
				Version:    require.Mod.Version,
				Indirect:   require.Indirect,
			})
		}
		for _, replace := range file.Replace {
			module.Replacements = append(module.Replacements, GoReplacement{
				OldModulePath: replace.Old.Path,
				OldVersion:    replace.Old.Version,
				// A local replacement records only that a replacement exists.
				// The filesystem path is not a durable project fact.
				NewModulePath: conditionalModulePath(replace.New),
				NewVersion:    replace.New.Version,
				LocalReplace:  modfile.IsDirectoryPath(replace.New.Path),
			})
		}
		modules = append(modules, module)
	}
	if len(modules) == 0 {
		return nil, ErrUnsupportedProjectContext
	}
	return modules, nil
}

// conditionalModulePath returns the new module path for a non-local
// replacement, and an empty string for a local one, so a filesystem path can
// never reach a fingerprint.
func conditionalModulePath(next module.Version) string {
	if modfile.IsDirectoryPath(next.Path) {
		return ""
	}
	return next.Path
}

// manifestLabel is a safe, path-free label for a local manifest: the name of
// the directory that contains it, never a path.
func manifestLabel(path string) string {
	return filepath.Base(filepath.Dir(path))
}
