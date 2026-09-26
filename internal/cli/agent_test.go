package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
)

// firstSubjectWithEvidence returns a seeded specimen that has observations.
func firstSubjectWithEvidence(store *fakeStore) string {
	for _, item := range store.evidence {
		return item.SubjectID
	}
	return ""
}

// assertJSONOnly fails when stdout carries anything other than one JSON
// document. Human and log output must stay on stderr.
func assertJSONOnly(t *testing.T, stdout *bytes.Buffer, stderr *bytes.Buffer) map[string]any {
	t.Helper()
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout=%q\nstderr=%q", err, stdout.String(), stderr.String())
	}
	if decoder.More() {
		t.Errorf("stdout contains more than one JSON document: %q", stdout.String())
	}
	return decoded
}

func TestVersionTextAndJSON(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{"version"}); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "reusery ") {
		t.Errorf("version text = %q", text)
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"version", "--format", "json"}); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	payload := assertJSONOnly(t, stdout, stderr)
	for _, key := range []string{"name", "version", "commit", "build_time"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("json output is missing %q", key)
		}
	}
}

func TestVersionRejectsUnknownFormat(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	if code := app.Run(context.Background(), []string{"version", "--format", "yaml"}); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "invalid --format") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestCatalogListAndDetail(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{"catalog", "--format", "json"}); code != ExitOK {
		t.Fatalf("list exit = %d; stderr=%q", code, stderr.String())
	}
	list := assertJSONOnly(t, stdout, stderr)
	primitives := list["primitives"].([]any)
	if len(primitives) == 0 {
		t.Fatal("catalog returned no capabilities")
	}
	first := primitives[0].(map[string]any)
	for _, key := range []string{"primitive_id", "name", "description", "contract_id", "contract_summary", "tags"} {
		if _, ok := first[key]; !ok {
			t.Errorf("capability entry is missing %q", key)
		}
	}

	stdout.Reset()
	stderr.Reset()
	id := first["primitive_id"].(string)
	if code := app.Run(context.Background(), []string{"catalog", "--primitive-id", id, "--format", "json"}); code != ExitOK {
		t.Fatalf("detail exit = %d; stderr=%q", code, stderr.String())
	}
	detail := assertJSONOnly(t, stdout, stderr)
	requirements := detail["contract"].(map[string]any)["requirements"].([]any)
	if len(requirements) == 0 {
		t.Error("detail returned no requirements")
	}
}

func TestCatalogUnknownPrimitiveFails(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	code := app.Run(context.Background(), []string{"catalog", "--primitive-id", "does/not/exist"})
	if code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "does/not/exist") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestEvidencePagingIsBoundedAndPure(t *testing.T) {
	app, stdout, stderr, store := testApp(t)
	subject := firstSubjectWithEvidence(store)
	if subject == "" {
		t.Fatal("the seeded catalogue has no evidence")
	}

	if code := app.Run(context.Background(), []string{
		"evidence", "--subject-id", subject, "--limit", "1", "--format", "json",
	}); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	page := assertJSONOnly(t, stdout, stderr)
	items := page["evidence"].([]any)
	if len(items) != 1 {
		t.Fatalf("evidence = %d, want 1", len(items))
	}
	cursor, _ := page["next_cursor"].(map[string]any)
	if cursor == nil {
		t.Fatal("expected a continuation cursor")
	}
	for _, key := range []string{"observed_at", "evidence_id"} {
		if _, ok := cursor[key]; !ok {
			t.Errorf("cursor is missing %q", key)
		}
	}

	// Follow the cursor to the end: no duplicates, no gaps.
	seen := map[string]bool{}
	for _, raw := range items {
		seen[raw.(map[string]any)["id"].(string)] = true
	}
	for page := 0; page < 20 && cursor != nil; page++ {
		stdout.Reset()
		stderr.Reset()
		code := app.Run(context.Background(), []string{
			"evidence", "--subject-id", subject, "--limit", "1",
			"--after-observed-at", cursor["observed_at"].(string),
			"--after-id", cursor["evidence_id"].(string),
			"--format", "json",
		})
		if code != ExitOK {
			t.Fatalf("page exit = %d; stderr=%q", code, stderr.String())
		}
		next := assertJSONOnly(t, stdout, stderr)
		for _, raw := range next["evidence"].([]any) {
			id := raw.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate observation %q across pages", id)
			}
			seen[id] = true
		}
		cursor, _ = next["next_cursor"].(map[string]any)
	}
	if len(seen) == 0 {
		t.Fatal("paged no observations")
	}
}

func TestEvidenceRejectsBadLimitsAndHalfCursors(t *testing.T) {
	app, _, stderr, store := testApp(t)
	subject := firstSubjectWithEvidence(store)

	cases := [][]string{
		{"evidence", "--subject-id", subject, "--limit", "0"},
		{"evidence", "--subject-id", subject, "--limit", "101"},
		{"evidence", "--subject-id", subject, "--after-id", "ev/x"},
		{"evidence", "--limit", "10"},
	}
	for _, args := range cases {
		if code := app.Run(context.Background(), args); code != ExitUsage {
			t.Errorf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
	}
	if stderr.Len() == 0 {
		t.Error("usage failures must explain themselves on stderr")
	}
}

func TestOutcomeAndOutcomesRoundTrip(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	// Produce one stored resolution first.
	if code := app.Run(context.Background(), []string{
		"resolve", "--request", filepath.Join(repoRoot, "examples", "resolve-bounded-subprocess-build-local.json"),
	}); code != ExitOK {
		t.Fatalf("resolve exit = %d; stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if code := app.Run(context.Background(), []string{
		"outcome", "--resolution-id", "1", "--kind", "adopted", "--note", "used for real",
		"--format", "json",
	}); code != ExitOK {
		t.Fatalf("outcome exit = %d; stderr=%q", code, stderr.String())
	}
	recorded := assertJSONOnly(t, stdout, stderr)
	if recorded["kind"] != "adopted" {
		t.Errorf("kind = %v", recorded["kind"])
	}
	if recorded["resolution_id"] != float64(1) {
		t.Errorf("resolution_id = %v", recorded["resolution_id"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{
		"outcomes", "--resolution-id", "1", "--format", "json",
	}); code != ExitOK {
		t.Fatalf("outcomes exit = %d; stderr=%q", code, stderr.String())
	}
	list := assertJSONOnly(t, stdout, stderr)
	events := list["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestOutcomeRejectsUnknownKindAndUnknownResolution(t *testing.T) {
	app, _, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{
		"outcome", "--resolution-id", "1", "--kind", "worked_great",
	}); code != ExitUsage {
		t.Errorf("unknown kind: exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unsupported --kind") {
		t.Errorf("stderr = %q", stderr.String())
	}

	if code := app.Run(context.Background(), []string{
		"outcome", "--resolution-id", "99", "--kind", string(outcome.KindAdopted),
	}); code != ExitError {
		t.Errorf("unknown resolution: exit = %d, want %d", code, ExitError)
	}
}

func TestResolveReadsTheRequestFromStdin(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.Stdin = strings.NewReader(`{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": []
	}`)

	if code := app.Run(context.Background(), []string{"resolve", "--request", "-", "--format", "json"}); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	assertJSONOnly(t, stdout, stderr)
}

func TestEnrichReadsTheRequestFromStdin(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.NewEnricher = func(Store, config.Config, enrichment.Clock) Enricher {
		return &fakeEnricher{}
	}
	app.Stdin = strings.NewReader(`{"specimen_ids":["fixture/process/bounded-subprocess/partial-adapt"]}`)

	if code := app.Run(context.Background(), []string{"enrich", "--request", "-", "--format", "json"}); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	assertJSONOnly(t, stdout, stderr)
}

func TestChooseRejectsTwoStandardInputs(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	app.Stdin = strings.NewReader(`{}`)

	code := app.Run(context.Background(), []string{
		"choose", "--request", "-", "--policy", "-",
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "standard input") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestChooseAcceptsOneStandardInput(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.LoadPolicy = policy.Load
	app.Stdin = strings.NewReader(`{
		"primitive_id": "process/bounded-subprocess",
		"contract_id": "process/bounded-subprocess/v1",
		"candidates": []
	}`)

	code := app.Run(context.Background(), []string{
		"choose", "--request", "-", "--policy", "policies/public-go-baseline-v1.yaml",
		"--root", repoRoot, "--format", "json",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	assertJSONOnly(t, stdout, stderr)
}

// TestMCPFailsCleanlyWithoutAStore proves a startup failure reaches stderr,
// exits non-zero and writes nothing to stdout: stdout carries MCP frames only.
func TestMCPFailsCleanlyWithoutAStore(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &App{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  strings.NewReader(""),
		LoadConfig: func() (config.Config, error) {
			return config.Config{DatabaseURL: "postgres://user:secret@localhost/reusery"}, nil
		},
		OpenStore: func(context.Context, config.Config) (Store, func(), error) {
			return nil, nil, errors.New("connection refused for postgres://user:secret@localhost/reusery")
		},
	}

	code := app.Run(context.Background(), []string{"mcp"})

	if code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout must stay empty on failure, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "cannot open store") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "user:secret") {
		t.Errorf("stderr leaked the database URL: %q", stderr.String())
	}
}

// TestMCPRejectsUnexpectedArguments keeps the stdio command free of flags that
// could be mistaken for a transport option.
func TestMCPRejectsUnexpectedArguments(t *testing.T) {
	app, stdout, _, _ := testApp(t)
	if code := app.Run(context.Background(), []string{"mcp", "--http"}); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout must stay empty, got %q", stdout.String())
	}
}
