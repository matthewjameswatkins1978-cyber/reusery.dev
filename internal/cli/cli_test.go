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
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

const (
	repoRoot    = "../.."
	manifestRel = "catalogue/dev/bounded-subprocess/manifest.yaml"
)

// fakeStore is an in-memory Store for CLI tests: no PostgreSQL required.
type fakeStore struct {
	primitives  map[string]model.Primitive
	contracts   map[string]model.Contract
	specimens   map[string]model.Specimen
	evidence    map[string]model.Evidence
	resolutions []model.Resolution
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
		return model.Primitive{}, fmt.Errorf("primitive %q not found", id)
	}
	return value, nil
}

func (f *fakeStore) GetContract(_ context.Context, id string) (model.Contract, error) {
	value, ok := f.contracts[id]
	if !ok {
		return model.Contract{}, fmt.Errorf("contract %q not found", id)
	}
	return value, nil
}

func (f *fakeStore) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	value, ok := f.specimens[id]
	if !ok {
		return model.Specimen{}, fmt.Errorf("specimen %q not found", id)
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
		return model.Resolution{}, fmt.Errorf("resolution %d not found", id)
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
		LoadBundle: catalog.Load,
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
