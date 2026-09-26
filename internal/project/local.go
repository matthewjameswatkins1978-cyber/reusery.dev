package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// ScanLocal derives a project fingerprint from an explicitly configured local
// root.
//
// It reads manifests only: go.work plus the go.mod files it references, or
// root/go.mod when there is no workspace. It never walks the repository, never
// reads a source file, never runs go, git or any shell command, and never
// returns the resolved root — the caller supplied it and it is not a durable
// project fact.
func ScanLocal(ctx context.Context, root string) (ScanResult, error) {
	ctx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()

	if strings.TrimSpace(root) == "" {
		return ScanResult{}, ErrProjectRootUnconfigured
	}
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		return ScanResult{}, err
	}

	reader := &manifestReader{root: resolvedRoot}
	work, moduleManifests, warnings, err := reader.collect(ctx)
	if err != nil {
		return ScanResult{}, err
	}

	inputs, err := reader.readModules(ctx, moduleManifests)
	if err != nil {
		return ScanResult{}, err
	}
	modules, err := modulesFrom(inputs, work)
	if err != nil {
		return ScanResult{}, err
	}

	fingerprint := Fingerprint{
		SchemaVersion: SchemaVersion,
		Language:      LanguageGo,
		Modules:       modules,
	}
	return finish(fingerprint, SourceLocal, "", "", warnings)
}

// resolveRoot canonicalises the supplied root exactly once. Every later
// manifest read is checked against it, including after symlink resolution.
func resolveRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve project root", ErrInvalidProjectContext)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: project root does not exist", ErrInvalidProjectContext)
		}
		resolved = absolute
	}
	return filepath.Clean(resolved), nil
}

// manifestReader reads an explicitly enumerated set of manifest files under a
// bounded file-count and byte budget. It never enumerates the filesystem.
type manifestReader struct {
	root  string
	files int
	bytes int
}

// collect finds the workspace file (if any) and the go.mod files to read,
// validating every workspace entry against the configured root. The workspace
// file is read here because its use entries drive everything else.
func (r *manifestReader) collect(ctx context.Context) (*modfile.WorkFile, []string, []string, error) {
	workPath := filepath.Join(r.root, "go.work")
	work, err := r.readWork(ctx, workPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if work == nil {
		modulePath := filepath.Join(r.root, "go.mod")
		if !fileExists(modulePath) {
			return nil, nil, nil, ErrUnsupportedProjectContext
		}
		return nil, []string{modulePath}, nil, nil
	}

	manifests := make([]string, 0, len(work.Use))
	warnings := []string{}
	for _, use := range work.Use {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: scan cancelled", ErrInvalidProjectContext)
		}
		dir, warning, ok := r.resolveUse(use.Path)
		if !ok {
			warnings = append(warnings, warning)
			continue
		}
		moduleManifest := filepath.Join(dir, "go.mod")
		if !fileExists(moduleManifest) {
			warnings = append(warnings, "workspace use entry has no go.mod and was skipped")
			continue
		}
		manifests = append(manifests, moduleManifest)
		if len(manifests) > MaxWorkspaceModules {
			return nil, nil, nil, fmt.Errorf("%w: workspace declares more than %d modules",
				ErrInvalidProjectContext, MaxWorkspaceModules)
		}
	}
	return work, manifests, warnings, nil
}

// resolveUse validates one go.work use entry. The returned warning never
// contains a filesystem path: the path is not a durable project fact.
func (r *manifestReader) resolveUse(usePath string) (dir string, warning string, ok bool) {
	if strings.TrimSpace(usePath) == "" {
		return "", "empty workspace use entry was skipped", false
	}
	if filepath.IsAbs(usePath) {
		return "", "absolute workspace use entry was skipped", false
	}
	if !filepath.IsLocal(usePath) {
		return "", "workspace use entry outside the configured root was skipped", false
	}

	joined := filepath.Clean(filepath.Join(r.root, usePath))
	if !withinRoot(r.root, joined) {
		return "", "workspace use entry outside the configured root was skipped", false
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "workspace use entry has no go.mod and was skipped", false
		}
		return "", "workspace use entry could not be resolved and was skipped", false
	}
	if !withinRoot(r.root, resolved) {
		return "", "workspace use entry resolves outside the configured root and was skipped", false
	}
	return resolved, "", true
}

// withinRoot reports whether candidate is root itself or beneath it.
func withinRoot(root, candidate string) bool {
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}

// readWork returns nil when there is simply no workspace file; a workspace
// that exists but is unreadable or invalid is a real error.
func (r *manifestReader) readWork(ctx context.Context, path string) (*modfile.WorkFile, error) {
	if !fileExists(path) {
		return nil, nil
	}
	data, err := r.read(ctx, path, "go.work")
	if err != nil {
		return nil, err
	}
	work, err := modfile.ParseWork(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: go.work is not valid: %v", ErrInvalidProjectContext, err)
	}
	return work, nil
}

// readModules reads the enumerated go.mod files into parse inputs.
func (r *manifestReader) readModules(ctx context.Context, paths []string) ([]manifestInput, error) {
	inputs := make([]manifestInput, 0, len(paths))
	for _, path := range paths {
		data, err := r.read(ctx, path, manifestLabel(path))
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, manifestInput{Label: manifestLabel(path), Data: data})
	}
	return inputs, nil
}

// read enforces the per-file size, file-count and total-byte bounds.
func (r *manifestReader) read(ctx context.Context, path, label string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan cancelled", ErrInvalidProjectContext)
	}
	if !withinRoot(r.root, path) {
		return nil, fmt.Errorf("%w: manifest is outside the configured root", ErrInvalidProjectContext)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: stat %s: %v", ErrInvalidProjectContext, label, err)
	}
	if info.Size() > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest %s exceeds %d bytes",
			ErrInvalidProjectContext, label, MaxManifestBytes)
	}
	r.files++
	if r.files > MaxManifestFiles {
		return nil, fmt.Errorf("%w: more than %d manifest files", ErrInvalidProjectContext, MaxManifestFiles)
	}
	if r.bytes+int(info.Size()) > MaxTotalManifestBytes {
		return nil, fmt.Errorf("%w: manifests exceed %d bytes total",
			ErrInvalidProjectContext, MaxTotalManifestBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidProjectContext, label, err)
	}
	r.bytes += len(data)
	return data, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
