package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

const (
	repoRoot    = "../.."
	manifestRel = "catalogue/dev/bounded-subprocess/manifest.yaml"
	profileRel  = "discovery/process/bounded-subprocess-v1.yaml"
)

// fakeDiscoverer is the discovery double: CLI tests never touch the network.
type fakeDiscoverer struct {
	result  discovery.Result
	err     error
	calls   int
	profile discovery.Profile
}

func (f *fakeDiscoverer) Discover(_ context.Context, profile discovery.Profile) (discovery.Result, error) {
	f.calls++
	f.profile = profile
	return f.result, f.err
}

// sampleDiscoveryResult is what a successful fake discovery run reports.
func sampleDiscoveryResult() discovery.Result {
	observedAt := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	specimen := model.Specimen{
		ID:          "public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "github.com/example/subproc",
		Source:      model.SourceRef{URL: "https://pkg.go.dev/github.com/example/subproc", Revision: "v1.0.0", Path: "github.com/example/subproc"},
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
	}
	return discovery.Result{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		ObservedAt:  observedAt,
		Candidates: []discovery.Candidate{{
			ProviderID: discovery.ProviderPkgGoDev,
			Specimen:   specimen,
			Evidence: []model.Evidence{discovery.NewObservation(discovery.ObservationSpec{
				ProviderID:  discovery.ProviderPkgGoDev,
				SubjectID:   specimen.ID,
				Kind:        "discovery_match",
				Claim:       `pkg.go.dev matched package "github.com/example/subproc"`,
				Result:      model.EvidenceInfo,
				Source:      model.SourceRef{URL: specimen.Source.URL, Revision: specimen.Source.Revision, Path: specimen.Source.Path},
				ObservedAt:  observedAt,
				Methodology: discovery.MethodologyPkgGoDev,
				Artifact:    "query=subprocess cancellation",
			}), discovery.NewObservation(discovery.ObservationSpec{
				ProviderID:  discovery.ProviderPkgGoDev,
				SubjectID:   specimen.ID,
				Kind:        "source_license",
				Claim:       `pkg.go.dev returned no unambiguous licence for package "github.com/example/subproc"`,
				Result:      model.EvidenceUnknown,
				Source:      model.SourceRef{URL: specimen.Source.URL, Revision: specimen.Source.Revision, Path: specimen.Source.Path},
				ObservedAt:  observedAt,
				Methodology: discovery.MethodologyPkgGoDev,
				Artifact:    "endpoint=/v1/package/github.com/example/subproc",
			})},
		}},
		Providers: []discovery.ProviderReport{
			{ID: discovery.ProviderPkgGoDev, Succeeded: true, Requests: 4, CandidateCount: 1},
			{ID: discovery.ProviderGitHubRepositories, Succeeded: false, Requests: 3, Issues: []discovery.ProviderIssue{
				{Kind: discovery.IssueRateLimited, Provider: discovery.ProviderGitHubRepositories, StatusCode: 429, Message: "GitHub responded HTTP 429"},
			}},
		},
	}
}

// fakeStore is an in-memory Store for CLI tests: no PostgreSQL required.
type fakeStore struct {
	primitives  map[string]model.Primitive
	contracts   map[string]model.Contract
	specimens   map[string]model.Specimen
	evidence    map[string]model.Evidence
	resolutions []model.Resolution
	feedback    []outcome.StoredFeedback
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string]model.Evidence{},
	}
}

func (f *fakeStore) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	value, ok := f.primitives[id]
	if !ok {
		return model.Primitive{}, fmt.Errorf("primitive %q not found: %w", id, store.ErrNotFound)
	}
	return value, nil
}

func (f *fakeStore) GetContract(_ context.Context, id string) (model.Contract, error) {
	value, ok := f.contracts[id]
	if !ok {
		return model.Contract{}, fmt.Errorf("contract %q not found: %w", id, store.ErrNotFound)
	}
	return value, nil
}

func (f *fakeStore) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	value, ok := f.specimens[id]
	if !ok {
		return model.Specimen{}, fmt.Errorf("specimen %q not found: %w", id, store.ErrNotFound)
	}
	return value, nil
}

func (f *fakeStore) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	items := []model.Evidence{}
	for _, evidence := range f.evidence {
		if evidence.SubjectID == id {
			items = append(items, evidence)
		}
	}
	// Deterministic order for assertions.
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].ID < items[i].ID {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	return items, nil
}

func (f *fakeStore) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	f.resolutions = append(f.resolutions, resolution)
	return int64(len(f.resolutions)), nil
}

func (f *fakeStore) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if id < 1 || id > int64(len(f.resolutions)) {
		return model.Resolution{}, fmt.Errorf("resolution %d not found: %w", id, store.ErrNotFound)
	}
	return f.resolutions[id-1], nil
}

func (f *fakeStore) UpsertPrimitive(_ context.Context, value model.Primitive) error {
	f.primitives[value.ID] = value
	return nil
}

func (f *fakeStore) UpsertContract(_ context.Context, value model.Contract) error {
	f.contracts[value.ID] = value
	return nil
}

func (f *fakeStore) UpsertSpecimen(_ context.Context, value model.Specimen) error {
	f.specimens[value.ID] = value
	return nil
}

func (f *fakeStore) InsertEvidence(_ context.Context, value model.Evidence) error {
	if _, exists := f.evidence[value.ID]; exists {
		return fmt.Errorf("evidence %q already exists", value.ID)
	}
	f.evidence[value.ID] = value
	return nil
}

func (f *fakeStore) FindEvidence(_ context.Context, id string) (model.Evidence, bool, error) {
	value, ok := f.evidence[id]
	return value, ok, nil
}

// ListEvidenceAfter returns a bounded page in (observed_at, id) order.
func (f *fakeStore) ListEvidenceAfter(_ context.Context, subjectID string, after time.Time, afterID string, limit int) ([]model.Evidence, error) {
	all, err := f.ListEvidenceBySubject(context.Background(), subjectID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Evidence, 0, limit)
	for _, item := range all {
		if !after.IsZero() && (item.ObservedAt.Before(after) ||
			(item.ObservedAt.Equal(after) && item.ID <= afterID)) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, item)
	}
	return out, nil
}

// ListPrimitives returns seeded primitives in id order.
func (f *fakeStore) ListPrimitives(_ context.Context, limit int) ([]model.Primitive, error) {
	if limit <= 0 {
		return []model.Primitive{}, nil
	}
	ids := make([]string, 0, len(f.primitives))
	for id := range f.primitives {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Primitive, 0, len(ids))
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		out = append(out, f.primitives[id])
	}
	return out, nil
}

// InsertFeedback appends one post-resolution event.
func (f *fakeStore) InsertFeedback(_ context.Context, value outcome.Feedback) (int64, error) {
	f.feedback = append(f.feedback, outcome.StoredFeedback{
		ID:       int64(len(f.feedback) + 1),
		Feedback: value,
	})
	return int64(len(f.feedback)), nil
}

// ListFeedback returns stored post-resolution events in chronological order.
func (f *fakeStore) ListFeedback(_ context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error) {
	out := make([]outcome.StoredFeedback, 0, limit)
	for _, event := range f.feedback {
		if event.ResolutionID == resolutionID {
			out = append(out, event)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// testApp wires a CLI against the real repository catalogue and a fake store.
func testApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer, *fakeStore) {
	t.Helper()

	bundle, err := catalog.Load(repoRoot, filepath.FromSlash(manifestRel))
	if err != nil {
		t.Fatalf("load repository bundle: %v", err)
	}
	store := newFakeStore()
	if err := catalog.Seed(context.Background(), bundle, store); err != nil {
		t.Fatalf("seed fake store: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	app := &App{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  strings.NewReader(""),
		LoadConfig: func() (config.Config, error) {
			return config.Config{
				HTTPAddr:    ":0",
				LogLevel:    slog.LevelInfo,
				DatabaseURL: "postgres://test",
			}, nil
		},
		OpenStore: func(context.Context, config.Config) (Store, func(), error) {
			return store, func() {}, nil
		},
		LoadBundle:  catalog.Load,
		LoadProfile: discovery.LoadProfile,
		NewDiscoverer: func(Store, config.Config, discovery.Clock) Discoverer {
			return &fakeDiscoverer{result: sampleDiscoveryResult()}
		},
		Serve: func(context.Context, config.Config, *slog.Logger) error {
			return nil
		},
		Clock: func() time.Time {
			return time.Date(2026, time.September, 25, 18, 0, 0, 0, time.UTC)
		},
	}
	return app, stdout, stderr, store
}

func TestHelp(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{"help"}); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{"frobnicate"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want an unknown-command message", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestMissingRequiredFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"resolve without request", []string{"resolve"}, "--request is required"},
		{"seed without manifest", []string{"seed"}, "--manifest is required"},
		{"resolution without id", []string{"resolution"}, "--id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _, stderr, _ := testApp(t)

			if code := app.Run(context.Background(), tt.args); code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestInvalidFormat(t *testing.T) {
	app, _, stderr, _ := testApp(t)

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json", "--format", "yaml"}
	if code := app.Run(context.Background(), args); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "invalid --format") {
		t.Errorf("stderr = %q, want an invalid-format message", stderr.String())
	}
}

func TestMalformedRequestJSON(t *testing.T) {
	dir := t.TempDir()

	t.Run("not json", func(t *testing.T) {
		path := filepath.Join(dir, "broken.json")
		if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		app, _, stderr, _ := testApp(t)

		if code := app.Run(context.Background(), []string{"resolve", "--request", path}); code != ExitError {
			t.Fatalf("exit = %d, want %d", code, ExitError)
		}
		if !strings.Contains(stderr.String(), "decode request") {
			t.Errorf("stderr = %q, want a decode failure", stderr.String())
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		path := filepath.Join(dir, "unknown-field.json")
		body := `{"primitive_id":"x","contract_id":"y","candidates":[],"typo_field":true}`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		app, _, stderr, _ := testApp(t)

		if code := app.Run(context.Background(), []string{"resolve", "--request", path}); code != ExitError {
			t.Fatalf("exit = %d, want %d", code, ExitError)
		}
		if !strings.Contains(stderr.String(), "decode request") {
			t.Errorf("stderr = %q, want a decode failure", stderr.String())
		}
	})
}

func TestResolveJSONOutput(t *testing.T) {
	app, stdout, stderr, store := testApp(t)

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json", "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitOK, stderr.String())
	}

	var out struct {
		ResolutionID int64                        `json:"resolution_id"`
		Resolution   model.Resolution             `json:"resolution"`
		Considered   []resolver.CandidateDecision `json:"considered"`
	}
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}

	if out.ResolutionID != 1 {
		t.Errorf("resolution_id = %d, want 1", out.ResolutionID)
	}
	if out.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("outcome = %q, want depend", out.Resolution.Outcome)
	}
	wantSpecimen := "fixture/process/bounded-subprocess/complete-dependency"
	if out.Resolution.SpecimenID != wantSpecimen {
		t.Errorf("specimen = %q, want %q", out.Resolution.SpecimenID, wantSpecimen)
	}
	if len(out.Considered) != 2 {
		t.Fatalf("considered = %d, want 2", len(out.Considered))
	}
	if out.Considered[0].SpecimenID != "fixture/process/bounded-subprocess/partial-adapt" || out.Considered[0].Selected {
		t.Errorf("first considered = %#v, want rejected partial-adapt", out.Considered[0])
	}
	if len(out.Considered[0].RejectionReasons) == 0 {
		t.Error("rejected candidate has no rejection reasons")
	}
	if !out.Considered[1].Selected {
		t.Error("second candidate was not selected")
	}
	if len(store.resolutions) != 1 {
		t.Errorf("stored %d resolutions, want 1", len(store.resolutions))
	}
}

func TestResolveTextOutput(t *testing.T) {
	app, stdout, _, _ := testApp(t)

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json", "--format", "text"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	out := stdout.String()
	for _, want := range []string{"resolution_id: 1", "outcome: depend", "rejected:", "considered:", "fixture/process/bounded-subprocess/complete-dependency"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestResolveBuildLocalExample(t *testing.T) {
	app, stdout, _, store := testApp(t)

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess-build-local.json", "--format", "text"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "outcome: build_locally") {
		t.Errorf("output = %q, want build_locally", stdout.String())
	}
	if len(store.resolutions) != 1 || store.resolutions[0].Outcome != model.OutcomeBuildLocally {
		t.Errorf("stored resolutions = %#v, want one build_locally", store.resolutions)
	}
}

func TestStdoutStderrSeparation(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json", "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	var probe any
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		t.Errorf("stdout must contain only JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stderr.String(), "stored") {
		t.Errorf("stderr = %q, want progress messages", stderr.String())
	}
}

func TestResolutionInspection(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	resolveArgs := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json", "--format", "json"}
	if code := app.Run(context.Background(), resolveArgs); code != ExitOK {
		t.Fatalf("resolve exit = %d; stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()

	args := []string{"resolution", "--id", "1", "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitOK, stderr.String())
	}

	var out resolutionJSON
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if out.ResolutionID != 1 || out.Resolution.Outcome != model.OutcomeDepend {
		t.Errorf("inspect output = %#v, want id 1 outcome depend", out)
	}

	stdout.Reset()
	if code := app.Run(context.Background(), []string{"resolution", "--id", "99"}); code != ExitError {
		t.Errorf("unknown id exit = %d, want %d", code, ExitError)
	}
}

func TestSeedCommandIsRepeatable(t *testing.T) {
	app, _, stderr, store := testApp(t)

	args := []string{"seed", "--root", repoRoot, "--manifest", filepath.FromSlash(manifestRel)}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("first seed exit = %d; stderr=%q", code, stderr.String())
	}
	firstEvidence := len(store.evidence)

	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("second seed exit = %d; stderr=%q", code, stderr.String())
	}
	if len(store.evidence) != firstEvidence {
		t.Errorf("evidence grew from %d to %d on repeat seed", firstEvidence, len(store.evidence))
	}
	if !strings.Contains(stderr.String(), "seeded primitive") {
		t.Errorf("stderr = %q, want a seed summary", stderr.String())
	}
}

func TestConfigurationFailureIsExitError(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	app.LoadConfig = func() (config.Config, error) {
		return config.Config{}, errors.New("REUSERY_DATABASE_URL is required")
	}

	args := []string{"resolve", "--request", "../../examples/resolve-bounded-subprocess.json"}
	if code := app.Run(context.Background(), args); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Errorf("stderr = %q, want a configuration failure", stderr.String())
	}
}

func TestNoArgumentsStartsServer(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	served := false
	app.Serve = func(context.Context, config.Config, *slog.Logger) error {
		served = true
		return nil
	}

	if code := app.Run(context.Background(), nil); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !served {
		t.Error("server was not started for the no-argument invocation")
	}
	if !strings.Contains(stderr.String(), "starting reusery") {
		t.Errorf("stderr = %q, want a startup log", stderr.String())
	}
}

func TestServeCommand(t *testing.T) {
	app, _, _, _ := testApp(t)
	served := false
	app.Serve = func(context.Context, config.Config, *slog.Logger) error {
		served = true
		return nil
	}

	if code := app.Run(context.Background(), []string{"serve"}); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !served {
		t.Error("serve command did not start the server")
	}
}

func TestDiscoverRequiresProfile(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)

	if code := app.Run(context.Background(), []string{"discover"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--profile is required") {
		t.Errorf("stderr = %q, want a missing-flag message", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestDiscoverRejectsInvalidFormat(t *testing.T) {
	app, _, stderr, _ := testApp(t)

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "yaml"}
	if code := app.Run(context.Background(), args); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "invalid --format") {
		t.Errorf("stderr = %q, want an invalid-format message", stderr.String())
	}
}

func TestDiscoverRejectsStructuralProfileProblems(t *testing.T) {
	root := t.TempDir()
	badProfile := filepath.Join(root, "profile.yaml")
	body := strings.Join([]string{
		"schema_version: 1",
		"primitive_id: process/bounded-subprocess",
		"contract_id: process/bounded-subprocess/v1",
		"providers:",
		"  - id: sourcegraph",
		"    queries:",
		"      - text: q",
		"        limit: 1",
		"",
	}, "\n")
	if err := os.WriteFile(badProfile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	app, stdout, stderr, _ := testApp(t)
	args := []string{"discover", "--root", root, "--profile", "profile.yaml"}
	if code := app.Run(context.Background(), args); code != ExitUsage {
		t.Fatalf("exit = %d, want %d (a profile problem is a usage problem)", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown provider") {
		t.Errorf("stderr = %q, want the structural reason", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestDiscoverJSONOutput(t *testing.T) {
	app, stdout, stderr, store := testApp(t)
	runner := &fakeDiscoverer{result: sampleDiscoveryResult()}
	app.NewDiscoverer = func(Store, config.Config, discovery.Clock) Discoverer { return runner }

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitOK, stderr.String())
	}

	var out discovery.Result
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if out.PrimitiveID != "process/bounded-subprocess" || out.ContractID != "process/bounded-subprocess/v1" {
		t.Errorf("result = %#v", out)
	}
	if len(out.Candidates) != 1 || len(out.Providers) != 2 {
		t.Fatalf("candidates = %d providers = %d", len(out.Candidates), len(out.Providers))
	}
	if out.Providers[0].ID != discovery.ProviderPkgGoDev || !out.Providers[0].Succeeded {
		t.Errorf("provider report = %#v", out.Providers[0])
	}
	if out.Providers[1].Succeeded || out.Providers[1].Issues[0].Kind != discovery.IssueRateLimited {
		t.Errorf("partial failure report = %#v", out.Providers[1])
	}
	if runner.calls != 1 {
		t.Errorf("discoverer calls = %d, want 1", runner.calls)
	}
	if runner.profile.PrimitiveID != "process/bounded-subprocess" {
		t.Errorf("profile passed to discoverer = %#v", runner.profile)
	}
	if !strings.Contains(stderr.String(), "discovered 1 candidate") {
		t.Errorf("stderr = %q, want a progress summary", stderr.String())
	}
	// Discovery never resolves: no Resolution may exist.
	if len(store.resolutions) != 0 {
		t.Errorf("discovery stored %d resolutions, want 0", len(store.resolutions))
	}
}

func TestDiscoverTextOutput(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.NewDiscoverer = func(Store, config.Config, discovery.Clock) Discoverer {
		return &fakeDiscoverer{result: sampleDiscoveryResult()}
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "text"}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"primitive: process/bounded-subprocess",
		"contract: process/bounded-subprocess/v1",
		"observed_at: 2026-09-25T12:00:00Z",
		"pkg.go.dev (ok, requests=4, candidates=1, incomplete=false)",
		"github-repositories (failed, requests=3, candidates=0, incomplete=false)",
		"rate_limited: GitHub responded HTTP 429",
		"public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0",
		"reuse_modes: dependency",
		"url: https://pkg.go.dev/github.com/example/subproc",
		"revision: v1.0.0",
		"info discovery_match:",
		"unknown source_license:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"score", "confidence", "winner", "best candidate", "rank"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("text output must not mention %q:\n%s", forbidden, out)
		}
	}
}

func TestDiscoverEmptyResultIsSuccess(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.NewDiscoverer = func(Store, config.Config, discovery.Clock) Discoverer {
		return &fakeDiscoverer{result: discovery.Result{
			PrimitiveID: "process/bounded-subprocess",
			ContractID:  "process/bounded-subprocess/v1",
			Candidates:  []discovery.Candidate{},
			Providers:   []discovery.ProviderReport{{ID: discovery.ProviderPkgGoDev, Succeeded: true}},
		}}
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel)}
	if code := app.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit = %d, want %d (empty discovery is a success)", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "candidates: (none)") {
		t.Errorf("stdout = %q, want an empty candidate list", stdout.String())
	}
	if !strings.Contains(stderr.String(), "discovered 0 candidate") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDiscoverAllProvidersFailedIsExecutionFailure(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	result := sampleDiscoveryResult()
	result.Candidates = []discovery.Candidate{}
	for i := range result.Providers {
		result.Providers[i].Succeeded = false
		result.Providers[i].Issues = []discovery.ProviderIssue{
			{Kind: discovery.IssueUnavailable, Provider: result.Providers[i].ID, Message: "network is down"},
		}
	}
	app.NewDiscoverer = func(Store, config.Config, discovery.Clock) Discoverer {
		return &fakeDiscoverer{result: result, err: &discovery.ProvidersFailedError{Providers: []string{
			discovery.ProviderPkgGoDev, discovery.ProviderGitHubRepositories,
		}}}
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	// The reports must stay inspectable even though the run failed.
	var out discovery.Result
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(out.Providers) != 2 {
		t.Errorf("provider reports = %d, want both inspectable", len(out.Providers))
	}
	if !strings.Contains(stderr.String(), "all providers failed operationally") {
		t.Errorf("stderr = %q, want the execution failure", stderr.String())
	}
}

func TestDiscoverExecutionFailureProducesNoOutput(t *testing.T) {
	app, stdout, stderr, _ := testApp(t)
	app.NewDiscoverer = func(Store, config.Config, discovery.Clock) Discoverer {
		return &fakeDiscoverer{err: errors.New("insert evidence: database is down")}
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel), "--format", "json"}
	if code := app.Run(context.Background(), args); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no success output on failure", stdout.String())
	}
	if !strings.Contains(stderr.String(), "database is down") {
		t.Errorf("stderr = %q, want the failure", stderr.String())
	}
}

func TestDiscoverReportsConfigurationFailures(t *testing.T) {
	app, _, stderr, _ := testApp(t)
	app.LoadConfig = func() (config.Config, error) {
		return config.Config{}, errors.New("REUSERY_DATABASE_URL is required")
	}

	args := []string{"discover", "--root", repoRoot, "--profile", filepath.FromSlash(profileRel)}
	if code := app.Run(context.Background(), args); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Errorf("stderr = %q, want a configuration failure", stderr.String())
	}
}
