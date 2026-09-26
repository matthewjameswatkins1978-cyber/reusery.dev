package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// fakeEnricher is the enrichment double: CLI tests never touch the network.
type fakeEnricher struct {
	result enrichment.Result
	err    error
	calls  int
	ids    []string
}

func (f *fakeEnricher) Enrich(_ context.Context, specimenIDs []string) (enrichment.Result, error) {
	f.calls++
	f.ids = specimenIDs
	return f.result, f.err
}

func sampleEnrichmentResult() enrichment.Result {
	observedAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)
	specimenID := "public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0"
	return enrichment.Result{
		ObservedAt: observedAt,
		Evidence: []model.Evidence{{
			ID:          "enrichment/deps.dev/" + strings.Repeat("a", 64),
			SubjectID:   specimenID,
			Kind:        enrichment.KindSourceLicense,
			Claim:       `deps.dev returned licence expression "MIT" for Go module "github.com/example/subproc" version "v1.0.0"`,
			Result:      model.EvidenceInfo,
			Source:      model.SourceRef{URL: "https://api.deps.dev/v3/x", Revision: "v1.0.0"},
			ObservedAt:  observedAt,
			Methodology: enrichment.MethodologyDepsDev,
		}},
		Specimens: []enrichment.SpecimenReport{{
			SpecimenID:    specimenID,
			Supported:     true,
			EvidenceCount: 1,
			Providers: []enrichment.ProviderReport{{
				ID:            enrichment.ProviderDepsDev,
				Succeeded:     true,
				Requests:      2,
				EvidenceCount: 1,
				Issues:        []enrichment.ProviderIssue{},
			}},
		}},
	}
}

func writeEnrichRequest(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return name
}

func enrichApp(t *testing.T, store Store, enricher Enricher) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, _, _, _ := testApp(t)
	app.Stdout = stdout
	app.Stderr = stderr
	app.OpenStore = func(context.Context, config.Config) (Store, func(), error) {
		return store, func() {}, nil
	}
	app.NewEnricher = func(Store, config.Config, enrichment.Clock) Enricher { return enricher }
	return app, stdout, stderr
}

func TestEnrichRequiresARequest(t *testing.T) {
	app, _, stderr := enrichApp(t, newFakeStore(), &fakeEnricher{})
	if code := app.Run(context.Background(), []string{"enrich"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--request is required") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestEnrichRejectsBadFormatAndUnreadableRequests(t *testing.T) {
	root := t.TempDir()
	request := writeEnrichRequest(t, root, "req.json", `{"specimen_ids":["a"]}`)

	cases := map[string][]string{
		"bad format":     {"enrich", "--root", root, "--request", request, "--format", "yaml"},
		"missing file":   {"enrich", "--root", root, "--request", "nope.json"},
		"path escape":    {"enrich", "--root", root, "--request", "../escape.json"},
		"malformed json": {"enrich", "--root", root, "--request", writeEnrichRequest(t, root, "bad-json.json", `{`)},
		"unknown field":  {"enrich", "--root", root, "--request", writeEnrichRequest(t, root, "unknown-field.json", `{"specimen_ids":["a"],"rank":1}`)},
		"empty list":     {"enrich", "--root", root, "--request", writeEnrichRequest(t, root, "empty.json", `{"specimen_ids":[]}`)},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			app, _, stderr := enrichApp(t, newFakeStore(), &fakeEnricher{})
			if code := app.Run(context.Background(), args); code != ExitUsage {
				t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
			}
		})
	}
}

func TestEnrichRunsAndReportsProviders(t *testing.T) {
	root := t.TempDir()
	request := writeEnrichRequest(t, root, "req.json", `{"specimen_ids":["public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0"]}`)
	enricher := &fakeEnricher{result: sampleEnrichmentResult()}
	app, stdout, stderr := enrichApp(t, newFakeStore(), enricher)

	code := app.Run(context.Background(), []string{"enrich", "--root", root, "--request", request, "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	if enricher.calls != 1 {
		t.Errorf("calls = %d", enricher.calls)
	}
	if len(enricher.ids) != 1 || !strings.Contains(enricher.ids[0], "github.com%2Fexample%2Fsubproc") {
		t.Errorf("ids = %v", enricher.ids)
	}

	output := stdout.String()
	for _, want := range []string{
		"observed_at: 2026-09-26T10:00:00Z",
		"public/pkg.go.dev/github.com%2Fexample%2Fsubproc@v1.0.0 (supported, observations=1)",
		"provider deps.dev (ok, requests=2, observations=1)",
		`info source_license: deps.dev returned licence expression "MIT"`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(stderr.String(), "recorded 1 observation(s) across 1 specimen(s)") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestEnrichJSONOutputIsTheCanonicalResult(t *testing.T) {
	root := t.TempDir()
	request := writeEnrichRequest(t, root, "req.json", `{"specimen_ids":["a"]}`)
	app, stdout, stderr := enrichApp(t, newFakeStore(), &fakeEnricher{result: sampleEnrichmentResult()})

	code := app.Run(context.Background(), []string{"enrich", "--root", root, "--request", request, "--format", "json"})
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}

	var decoded enrichment.Result
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(decoded.Evidence) != 1 || decoded.Specimens[0].EvidenceCount != 1 {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestEnrichReportsANonNilErrorWithInspectableReports(t *testing.T) {
	root := t.TempDir()
	request := writeEnrichRequest(t, root, "req.json", `{"specimen_ids":["a"]}`)
	enricher := &fakeEnricher{result: sampleEnrichmentResult(), err: &enrichment.ProvidersFailedError{Providers: []string{"deps.dev"}}}
	app, stdout, stderr := enrichApp(t, newFakeStore(), enricher)

	code := app.Run(context.Background(), []string{"enrich", "--root", root, "--request", request})
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "provider deps.dev") {
		t.Errorf("provider reports must stay inspectable:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "every applicable provider call failed operationally") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestEnrichUsageIsListed(t *testing.T) {
	app, _, _, _ := testApp(t)
	stdout := &bytes.Buffer{}
	app.Stdout = stdout
	if code := app.Run(context.Background(), []string{"help"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "reusery enrich --request FILE") {
		t.Errorf("help does not document enrich")
	}
	if !strings.Contains(stdout.String(), "reusery choose --request FILE") {
		t.Errorf("help does not document choose")
	}
}
