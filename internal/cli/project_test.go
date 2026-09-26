package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRequiresASubcommand(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	if code := app.Run(context.Background(), []string{"project"}); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "reusery project") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestProjectRejectsUnknownSubcommand(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	if code := app.Run(context.Background(), []string{"project", "invent"}); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown sub-command") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestProjectScanSourcesAreMutuallyExclusive keeps the flag design honest: a
// default value must never silently choose the other source.
func TestProjectScanSourcesAreMutuallyExclusive(t *testing.T) {
	cases := map[string][]string{
		"unknown source":        {"project", "scan", "--source", "svn"},
		"missing github target": {"project", "scan", "--source", "github_public"},
		"github with root":      {"project", "scan", "--source", "github_public", "--github", "acme/widget", "--root", "."},
		"local with github":     {"project", "scan", "--source", "local", "--github", "acme/widget"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			app, _, stderr, _ := testApp(t)
			if code := app.Run(context.Background(), args); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			if stderr.Len() == 0 {
				t.Error("usage failures must explain themselves on stderr")
			}
		})
	}
}

func TestProjectRememberRejectsUnsupportedReason(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	code := app.Run(context.Background(), []string{
		"project", "remember",
		"--project-id", "project/go/x",
		"--primitive-id", "process/bounded-subprocess",
		"--candidate-id", "fixture/candidate",
		"--reason", "prefer_stdlib",
	})
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unsupported --reason") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestProjectCommandsValidateRequiredFlags(t *testing.T) {
	cases := map[string][]string{
		"show":        {"project", "show"},
		"remember":    {"project", "remember", "--project-id", "project/go/x"},
		"forget":      {"project", "forget", "--project-id", "project/go/x"},
		"history":     {"project", "history", "--project-id", "project/go/x", "--limit", "0"},
		"history big": {"project", "history", "--project-id", "project/go/x", "--limit", "101"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			app, _, _, _ := testApp(t)
			if code := app.Run(context.Background(), args); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
		})
	}
}

// TestMCPRejectsAMissingProjectRoot happens before any configuration or store
// work, so a typo fails fast with a usage error.
func TestMCPRejectsAMissingProjectRoot(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	code := app.Run(context.Background(), []string{
		"mcp", "--project-root", filepath.Join(t.TempDir(), "nope"),
	})
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout must stay empty, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--project-root") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestUsageListsTheProjectCommands(t *testing.T) {
	app, stdout, _, _ := testApp(t)
	if code := app.Run(context.Background(), []string{"help"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	usage := stdout.String()
	for _, want := range []string{
		"reusery mcp", "reusery project scan", "reusery project show",
		"reusery project remember", "reusery project forget", "reusery project history",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage is missing %q", want)
		}
	}
}
