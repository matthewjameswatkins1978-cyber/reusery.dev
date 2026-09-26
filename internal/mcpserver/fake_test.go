package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/app"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// Compile-time proof that the in-memory fixtures satisfy the application
// interfaces the MCP boundary depends on.
var (
	_ resolver.Repository = (*memoryStore)(nil)
	_ app.Catalog         = (*memoryStore)(nil)
	_ app.Inspector       = (*memoryStore)(nil)
	_ outcome.Repository  = (*memoryStore)(nil)
	_ app.Discoverer      = (*recordingDiscoverer)(nil)
	_ app.Enricher        = (*recordingEnricher)(nil)
)

// fixtureClock freezes every timestamp so payloads and cursors are stable.
func fixtureClock() time.Time {
	return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
}

// requirementIDs mirrors the real bounded-subprocess contract so payload
// budgets are measured against a representative shape.
var requirementIDs = []string{
	"starts-requested-program",
	"preserves-exit-status",
	"drains-stdout-stderr-concurrently",
	"bounds-stdout",
	"bounds-stderr",
	"reports-truncation",
	"supports-timeout",
	"supports-cancellation",
	"process-tree-semantics-explicit",
	"no-orphaned-pipe-readers",
	"result-distinguishes-failure-kinds",
}

const (
	fixturePrimitive = "process/bounded-subprocess"
	fixtureContract  = "process/bounded-subprocess/v1"
	fixtureEligible  = "fixture/eligible"
	fixtureMetadata  = "fixture/metadata-only"
	fixtureHeavy     = "fixture/heavy"
	fixtureLight     = "fixture/light"
	fixtureReference = "fixture/reference"
)

// memoryStore is an in-memory implementation of every read surface the MCP
// tools use. It lets the real Packet 7 quality service and the real outcome
// service run without a database.
type memoryStore struct {
	primitives  map[string]model.Primitive
	contracts   map[string]model.Contract
	specimens   map[string]model.Specimen
	evidence    map[string][]model.Evidence
	resolutions []model.Resolution
	feedback    []outcome.StoredFeedback
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string][]model.Evidence{},
	}
}

// seed installs the bounded-subprocess fixtures used across the suite.
func (m *memoryStore) seed() {
	m.primitives[fixturePrimitive] = model.Primitive{
		ID:          fixturePrimitive,
		Name:        "Bounded subprocess",
		Description: "Run child processes with bounded output and distinct termination outcomes.",
		Tags:        []string{"process"},
		ContractID:  fixtureContract,
	}
	requirements := make([]model.Requirement, 0, len(requirementIDs))
	for _, id := range requirementIDs {
		requirements = append(requirements, model.Requirement{
			ID: id, Description: id, Kind: "behavior", Required: true,
		})
	}
	m.contracts[fixtureContract] = model.Contract{
		ID:           fixtureContract,
		PrimitiveID:  fixturePrimitive,
		Version:      "v1",
		Summary:      "Bounded child-process execution with distinct failure kinds.",
		Requirements: requirements,
	}

	m.addSpecimen(fixtureEligible, model.ReuseDependency, "https://example.invalid/eligible", "MIT", true)
	m.addSpecimen(fixtureMetadata, model.ReuseDependency, "https://example.invalid/metadata", "MIT", false)
	m.addSpecimen(fixtureHeavy, model.ReuseDependency, "https://example.invalid/heavy", "MIT", true)
	m.addSpecimen(fixtureLight, model.ReuseDependency, "https://example.invalid/light", "MIT", true)
	m.addSpecimen(fixtureReference, model.ReuseReference, "https://example.invalid/reference", "MIT", false)
}

func (m *memoryStore) addSpecimen(id string, mode model.ReuseMode, url, licence string, behavioural bool) {
	specimen := model.Specimen{
		ID:          id,
		PrimitiveID: fixturePrimitive,
		Name:        id,
		ReuseMode:   []model.ReuseMode{mode},
		Source:      model.SourceRef{URL: url, Revision: "9ca7c88e3243264f", License: licence},
	}
	m.specimens[id] = specimen

	evidence := []model.Evidence{fact(id, "discovery_match", "surfaced by discovery")}
	evidence = append(evidence, fact(id, "source_license", licence))
	evidence = append(evidence, fact(id, "known_advisory_count", 0))
	evidence = append(evidence, fact(id, "repository_archived", false))
	evidence = append(evidence, fact(id, "package_deprecated", false))
	evidence = append(evidence, fact(id, "source_revision", "9ca7c88e3243264f"))

	if behavioural {
		for _, requirementID := range requirementIDs {
			evidence = append(evidence, model.Evidence{
				ID:         "ev/" + id + "/pass/" + requirementID,
				SubjectID:  id,
				Kind:       "test",
				Claim:      "behavioural test satisfied " + requirementID,
				Result:     model.EvidencePass,
				AppliesTo:  requirementID,
				ObservedAt: fixtureClock(),
			})
		}
	}
	if id == fixtureHeavy {
		evidence = append(evidence, fact(id, "direct_dependency_count", 8))
	}
	if id == fixtureLight {
		evidence = append(evidence, fact(id, "direct_dependency_count", 2))
	}
	m.evidence[id] = evidence
}

// fact builds one informational observation carrying the machine-readable
// artifact Packet 7's fact extractor reads. The claim repeats the value in
// prose so a human reader never needs the artifact.
func fact(subject, kind string, value any) model.Evidence {
	encoded, err := json.Marshal(map[string]any{"schema_version": 1, "value": value})
	if err != nil {
		panic(err)
	}
	var claim string
	switch typed := value.(type) {
	case string:
		claim = kind + "=" + typed
	default:
		claim = fmt.Sprintf("%s=%v", kind, typed)
	}
	return model.Evidence{
		ID:         "ev/" + subject + "/" + kind,
		SubjectID:  subject,
		Kind:       kind,
		Claim:      claim,
		Result:     model.EvidenceInfo,
		Artifact:   string(encoded),
		ObservedAt: fixtureClock(),
	}
}

// --- resolver.Repository

func (m *memoryStore) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	if value, ok := m.primitives[id]; ok {
		return value, nil
	}
	return model.Primitive{}, store.ErrNotFound
}

func (m *memoryStore) GetContract(_ context.Context, id string) (model.Contract, error) {
	if value, ok := m.contracts[id]; ok {
		return value, nil
	}
	return model.Contract{}, store.ErrNotFound
}

func (m *memoryStore) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	if value, ok := m.specimens[id]; ok {
		return value, nil
	}
	return model.Specimen{}, store.ErrNotFound
}

func (m *memoryStore) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	return append([]model.Evidence(nil), m.evidence[id]...), nil
}

func (m *memoryStore) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	m.resolutions = append(m.resolutions, resolution)
	return int64(len(m.resolutions)), nil
}

func (m *memoryStore) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if id < 1 || id > int64(len(m.resolutions)) {
		return model.Resolution{}, store.ErrNotFound
	}
	return m.resolutions[id-1], nil
}

// --- app.Catalog

func (m *memoryStore) ListPrimitives(_ context.Context, limit int) ([]model.Primitive, error) {
	ids := make([]string, 0, len(m.primitives))
	for id := range m.primitives {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Primitive, 0, len(ids))
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		out = append(out, m.primitives[id])
	}
	return out, nil
}

// --- app.Inspector

func (m *memoryStore) ListEvidenceAfter(_ context.Context, id string, after time.Time, afterID string, limit int) ([]model.Evidence, error) {
	rows := append([]model.Evidence(nil), m.evidence[id]...)
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].ObservedAt.Equal(rows[j].ObservedAt) {
			return rows[i].ObservedAt.Before(rows[j].ObservedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	out := make([]model.Evidence, 0, limit)
	for _, row := range rows {
		if !after.IsZero() && (row.ObservedAt.Before(after) ||
			(row.ObservedAt.Equal(after) && row.ID <= afterID)) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, row)
	}
	return out, nil
}

// --- outcome.Repository

func (m *memoryStore) InsertFeedback(_ context.Context, f outcome.Feedback) (int64, error) {
	m.feedback = append(m.feedback, outcome.StoredFeedback{
		ID:       int64(len(m.feedback) + 1),
		Feedback: f,
	})
	return int64(len(m.feedback)), nil
}

func (m *memoryStore) ListFeedback(_ context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error) {
	out := make([]outcome.StoredFeedback, 0, limit)
	for _, event := range m.feedback {
		if event.ResolutionID == resolutionID {
			out = append(out, event)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- provider fakes

// recordingDiscoverer stands in for Packet 5 and records every call.
type recordingDiscoverer struct {
	result discovery.Result
	err    error
	calls  int
}

func (f *recordingDiscoverer) Discover(_ context.Context, profile discovery.Profile) (discovery.Result, error) {
	f.calls++
	f.result.PrimitiveID = profile.PrimitiveID
	f.result.ContractID = profile.ContractID
	return f.result, f.err
}

// recordingEnricher stands in for Packet 7 enrichment and records every call.
type recordingEnricher struct {
	result enrichment.Result
	err    error
	calls  int
	ids    []string
}

func (f *recordingEnricher) Enrich(_ context.Context, ids []string) (enrichment.Result, error) {
	f.calls++
	f.ids = append([]string(nil), ids...)
	return f.result, f.err
}
