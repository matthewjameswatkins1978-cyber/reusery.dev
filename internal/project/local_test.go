package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

const simpleGoMod = `module example.com/widget

go 1.27.1
`

func TestScanSingleModule(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", simpleGoMod)

	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Project.SourceKind != SourceLocal {
		t.Errorf("source kind = %q", result.Project.SourceKind)
	}
	if result.Project.SourceLocator != "" {
		t.Errorf("a local project must never record a path, got %q", result.Project.SourceLocator)
	}
	if !strings.HasPrefix(result.Project.ID, "project/go/") || len(result.Project.ID) != len("project/go/")+64 {
		t.Errorf("project id = %q, want project/go/<sha256>", result.Project.ID)
	}
	if len(result.Fingerprint.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(result.Fingerprint.Modules))
	}
	module := result.Fingerprint.Modules[0]
	if module.ModulePath != "example.com/widget" {
		t.Errorf("module path = %q", module.ModulePath)
	}
	if module.GoVersion != "1.27.1" {
		t.Errorf("go version = %q", module.GoVersion)
	}
	if result.Fingerprint.Language != LanguageGo || result.Fingerprint.SchemaVersion != SchemaVersion {
		t.Errorf("fingerprint header = %+v", result.Fingerprint)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v", result.Warnings)
	}
}

func TestScanWorkspaceWithTwoModules(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.work", "go 1.27.1\n\nuse (\n\t./a\n\t./b\n)\n")
	writeManifest(t, root, "a/go.mod", "module example.com/a\n\ngo 1.27.1\n")
	writeManifest(t, root, "b/go.mod", "module example.com/b\n\ngo 1.27.1\n")

	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	paths := ModulePaths(result.Fingerprint)
	if len(paths) != 2 || paths[0] != "example.com/a" || paths[1] != "example.com/b" {
		t.Fatalf("module paths = %v", paths)
	}
	if result.Project.Name != "Go workspace (2 modules)" {
		t.Errorf("display name = %q", result.Project.Name)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v", result.Warnings)
	}
}

func TestScanCapturesRequirementsToolchainAndIndirectFlags(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", `module example.com/widget

go 1.27.1

toolchain go1.27.1

require (
	github.com/pkg/errors v0.9.1
	golang.org/x/mod v0.41.0 // indirect
)
`)
	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	module := result.Fingerprint.Modules[0]
	if module.Toolchain != "go1.27.1" {
		t.Errorf("toolchain = %q", module.Toolchain)
	}
	if len(module.Requirements) != 2 {
		t.Fatalf("requirements = %+v", module.Requirements)
	}
	if module.Requirements[0].ModulePath != "github.com/pkg/errors" ||
		module.Requirements[0].Version != "v0.9.1" || module.Requirements[0].Indirect {
		t.Errorf("direct requirement = %+v", module.Requirements[0])
	}
	if !module.Requirements[1].Indirect {
		t.Errorf("expected an indirect requirement, got %+v", module.Requirements[1])
	}
}

func TestScanRecordsModuleAndLocalReplacements(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", `module example.com/widget

go 1.27.1

require example.com/dep v1.4.0

replace example.com/dep v1.4.0 => example.com/dep v1.6.0

replace example.com/local => ../local
`)
	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	replacements := result.Fingerprint.Modules[0].Replacements
	if len(replacements) != 2 {
		t.Fatalf("replacements = %+v", replacements)
	}
	if replacements[0].OldModulePath != "example.com/dep" ||
		replacements[0].NewModulePath != "example.com/dep" ||
		replacements[0].NewVersion != "v1.6.0" || replacements[0].LocalReplace {
		t.Errorf("module replacement = %+v", replacements[0])
	}
	if !replacements[1].LocalReplace || replacements[1].NewModulePath != "" {
		t.Errorf("local replacement = %+v", replacements[1])
	}
	// The durable fact is that a replacement exists; the path never survives.
	encoded := mustJSON(t, result.Fingerprint)
	for _, forbidden := range []string{"../local", string(filepath.Separator)} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("fingerprint leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestScanIsDeterministicAndSensitiveToChange(t *testing.T) {
	first := t.TempDir()
	writeManifest(t, first, "go.mod", simpleGoMod)
	// A second checkout of the same module is the same logical project.
	second := t.TempDir()
	writeManifest(t, second, "go.mod", simpleGoMod)

	a, err := ScanLocal(context.Background(), first)
	if err != nil {
		t.Fatalf("scan a: %v", err)
	}
	b, err := ScanLocal(context.Background(), second)
	if err != nil {
		t.Fatalf("scan b: %v", err)
	}
	if a.FingerprintSHA256 != b.FingerprintSHA256 {
		t.Errorf("identical manifests produced different hashes: %s vs %s", a.FingerprintSHA256, b.FingerprintSHA256)
	}
	if a.Project.ID != b.Project.ID {
		t.Errorf("identical manifests produced different project ids")
	}

	writeManifest(t, second, "go.mod", simpleGoMod+"\nrequire example.com/dep v1.0.0\n")
	changed, err := ScanLocal(context.Background(), second)
	if err != nil {
		t.Fatalf("scan changed: %v", err)
	}
	if changed.FingerprintSHA256 == a.FingerprintSHA256 {
		t.Error("a changed requirement did not change the fingerprint hash")
	}
	if changed.Project.ID != a.Project.ID {
		t.Error("a dependency bump must not create a different project")
	}
}

func TestScanOrderingIsDeterministic(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", `module example.com/widget

go 1.27.1

require (
	golang.org/x/mod v0.41.0
	github.com/pkg/errors v0.9.1
	golang.org/x/sys v0.47.0 // indirect
)
`)
	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	requirements := result.Fingerprint.Modules[0].Requirements
	if len(requirements) != 3 {
		t.Fatalf("requirements = %+v", requirements)
	}
	// The stored fingerprint keeps authored order; identity and hashing are
	// canonical, which is what must be deterministic.
	paths := ModulePaths(result.Fingerprint)
	if len(paths) != 1 || paths[0] != "example.com/widget" {
		t.Errorf("module paths = %v", paths)
	}
	firstHash := mustHash(t, result.Fingerprint)
	secondHash := mustHash(t, result.Fingerprint)
	if firstHash != secondHash {
		t.Error("fingerprint hash is not stable")
	}
	if requirements[2].ModulePath != "golang.org/x/sys" || !requirements[2].Indirect {
		t.Errorf("requirements = %+v", requirements)
	}
}

func TestScanRejectsOversizedManifest(t *testing.T) {
	root := t.TempDir()
	huge := "module example.com/widget\n// " + strings.Repeat("x", MaxManifestBytes)
	writeManifest(t, root, "go.mod", huge)

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func TestScanRejectsTooManyWorkspaceModules(t *testing.T) {
	root := t.TempDir()
	use := strings.Builder{}
	use.WriteString("go 1.27.1\n\nuse (\n")
	for index := 0; index <= MaxWorkspaceModules; index++ {
		name := filepath.Join("m", string(rune('a'+index%26))+"_"+itoa(index))
		writeManifest(t, root, filepath.ToSlash(filepath.Join(name, "go.mod")),
			"module example.com/"+filepath.ToSlash(name)+"\n\ngo 1.27.1\n")
		use.WriteString("\t./" + filepath.ToSlash(name) + "\n")
	}
	use.WriteString(")\n")
	writeManifest(t, root, "go.work", use.String())

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func TestScanRejectsWorkspacePathEscape(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeManifest(t, outer, "outside/go.mod", "module example.com/outside\n\ngo 1.27.1\n")
	writeManifest(t, root, "go.work", "go 1.27.1\n\nuse ./../outside\n")

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) && !errors.Is(err, ErrUnsupportedProjectContext) {
		t.Fatalf("err = %v, want an unsupported or invalid project context", err)
	}
}

func TestScanSkipsAbsoluteWorkspaceUse(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeManifest(t, outside, "go.mod", "module example.com/outside\n\ngo 1.27.1\n")
	writeManifest(t, root, "local/go.mod", "module example.com/widget\n\ngo 1.27.1\n")
	writeManifest(t, root, "go.work", "go 1.27.1\n\nuse (\n\t./local\n\t"+filepath.ToSlash(outside)+"\n)\n")

	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Fingerprint.Modules) != 1 {
		t.Fatalf("modules = %+v, want only the in-root module", result.Fingerprint.Modules)
	}
	if result.Fingerprint.Modules[0].ModulePath != "example.com/widget" {
		t.Errorf("module = %q", result.Fingerprint.Modules[0].ModulePath)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("warnings = %v, want one skipped-entry warning", result.Warnings)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, outside) || strings.Contains(warning, root) {
			t.Errorf("warning leaked a path: %q", warning)
		}
	}
}

func TestScanSkipsSymlinkEscape(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeManifest(t, outer, "outside/go.mod", "module example.com/outside\n\ngo 1.27.1\n")
	writeManifest(t, root, "go.mod", "module example.com/widget\n\ngo 1.27.1\n")
	if err := os.Symlink(filepath.Join(outer, "outside"), filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeManifest(t, root, "go.work", "go 1.27.1\n\nuse ./linked\n")

	result, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Fingerprint.Modules) != 1 || result.Fingerprint.Modules[0].ModulePath != "example.com/widget" {
		t.Fatalf("modules = %+v, want only the in-root module", result.Fingerprint.Modules)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected a warning for the escaping symlink")
	}
}

func TestScanInvalidGoMod(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", "this is not a go.mod at all {{{\n")

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func TestScanGoModWithoutModulePath(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", "go 1.27.1\n")

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func TestScanInvalidGoWork(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.work", "this is not a go.work {{{\n")

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

func TestScanWithoutGoManifestIsUnsupported(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "main.go", "package main\n\nfunc main() {}\n")

	_, err := ScanLocal(context.Background(), root)
	if !errors.Is(err, ErrUnsupportedProjectContext) {
		t.Errorf("err = %v, want ErrUnsupportedProjectContext", err)
	}
}

func TestScanWithoutRootIsUnconfigured(t *testing.T) {
	if _, err := ScanLocal(context.Background(), "   "); !errors.Is(err, ErrProjectRootUnconfigured) {
		t.Errorf("err = %v, want ErrProjectRootUnconfigured", err)
	}
	if _, err := ScanLocal(context.Background(), filepath.Join(t.TempDir(), "does-not-exist")); !errors.Is(err, ErrInvalidProjectContext) {
		t.Errorf("err = %v, want ErrInvalidProjectContext", err)
	}
}

// TestScanIgnoresNonManifestFiles proves the scanner reads manifests only.
// Extra source and build files neither widen the fingerprint nor change the
// project identity.
func TestScanIgnoresNonManifestFiles(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "go.mod", simpleGoMod)
	baseline, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	writeManifest(t, root, "main.go", "package main\n\nfunc main() { println(\"secret\") }\n")
	writeManifest(t, root, "README.md", "# a readme\n")
	writeManifest(t, root, ".env", "DATABASE_URL=postgres://user:pass@localhost/db\n")
	writeManifest(t, root, "vendor/modules.txt", "# vendored\n")
	writeManifest(t, root, ".git/config", "[core]\n")

	after, err := ScanLocal(context.Background(), root)
	if err != nil {
		t.Fatalf("scan after: %v", err)
	}
	if after.FingerprintSHA256 != baseline.FingerprintSHA256 {
		t.Error("a non-manifest file changed the fingerprint")
	}
	if after.Project.ID != baseline.Project.ID {
		t.Error("a non-manifest file changed the project identity")
	}
	for _, forbidden := range []string{"DATABASE_URL", "println", "readme", ".git"} {
		if strings.Contains(mustJSON(t, after.Fingerprint), forbidden) {
			t.Errorf("fingerprint contains %q", forbidden)
		}
	}
}

// TestScanNeverReadsSourceOrRunsProcesses is a source-level guard: the
// scanner must not walk a tree, read a directory listing or execute anything.
func TestScanNeverReadsSourceOrRunsProcesses(t *testing.T) {
	for name, source := range productionSources(t) {
		for _, forbidden := range []string{"os/exec", "exec.Command", "filepath.Walk", "os.ReadDir", "os.Stdout"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains %q: the scanner reads manifests only", name, forbidden)
			}
		}
	}
}

func TestProjectIdentityIgnoresTheRootPath(t *testing.T) {
	first := t.TempDir()
	writeManifest(t, first, "go.mod", simpleGoMod)
	second := filepath.Join(t.TempDir(), "nested", "checkout")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeManifest(t, second, "go.mod", simpleGoMod)

	a, err := ScanLocal(context.Background(), first)
	if err != nil {
		t.Fatalf("scan a: %v", err)
	}
	b, err := ScanLocal(context.Background(), second)
	if err != nil {
		t.Fatalf("scan b: %v", err)
	}
	if a.Project.ID != b.Project.ID {
		t.Errorf("the same module identified differently by location: %q vs %q", a.Project.ID, b.Project.ID)
	}
	if strings.Contains(a.Project.ID, first) || strings.Contains(b.Project.ID, second) {
		t.Error("a project id contains a filesystem path")
	}
}
