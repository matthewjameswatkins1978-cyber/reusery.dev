package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// --------------------------------------------------------- in-memory store

// memoryRepository is an in-memory resolver.Repository plus the read surface
// the inspection API needs. It exists so the real Packet 7 quality service can
// run over HTTP without a database.
type memoryRepository struct {
	primitives  map[string]model.Primitive
	contracts   map[string]model.Contract
	specimens   map[string]model.Specimen
	evidence    map[string][]model.Evidence
	resolutions []model.Resolution
	nextID      int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string][]model.Evidence{},
	}
}

func (r *memoryRepository) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	value, ok := r.primitives[id]
	if !ok {
		return model.Primitive{}, store.ErrNotFound
	}
	return value, nil
}

func (r *memoryRepository) GetContract(_ context.Context, id string) (model.Contract, error) {
	value, ok := r.contracts[id]
	if !ok {
		return model.Contract{}, store.ErrNotFound
	}
	return value, nil
}

func (r *memoryRepository) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	value, ok := r.specimens[id]
	if !ok {
		return model.Specimen{}, store.ErrNotFound
	}
	return value, nil
}

func (r *memoryRepository) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	return append([]model.Evidence(nil), r.evidence[id]...), nil
}

func (r *memoryRepository) ListEvidenceAfter(_ context.Context, id string, after time.Time, afterID string, limit int) ([]model.Evidence, error) {
	ordered := orderedEvidence(r.evidence[id])
	page := make([]model.Evidence, 0, limit)
	for _, item := range ordered {
		if !after.IsZero() && (item.ObservedAt.Before(after) ||
			(item.ObservedAt.Equal(after) && item.ID <= afterID)) {
			continue
		}
		if len(page) == limit {
			break
		}
		page = append(page, item)
	}
	return page, nil
}

func (r *memoryRepository) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	r.nextID++
	r.resolutions = append(r.resolutions, resolution)
	return r.nextID, nil
}

func (r *memoryRepository) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if id <= 0 || id > int64(len(r.resolutions)) {
		return model.Resolution{}, store.ErrNotFound
	}
	return r.resolutions[id-1], nil
}

func (r *memoryRepository) count() int { return len(r.resolutions) }

// ---------------------------------------------------------------- fixtures

const (
	primitiveID = "process/bounded-subprocess"
	contractID  = "process/bounded-subprocess/v1"
)

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

func artifact(value any) string {
	encoded, err := json.Marshal(map[string]any{"schema_version": 1, "value": value})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func fact(subject, kind string, value any) model.Evidence {
	return model.Evidence{
		ID:         "ev/" + subject + "/" + kind,
		SubjectID:  subject,
		Kind:       kind,
		Claim:      kind,
		Result:     model.EvidenceInfo,
		Artifact:   artifact(value),
		ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}
}

func behaviouralEvidence(subject string) []model.Evidence {
	out := make([]model.Evidence, 0, len(requirementIDs))
	for _, requirementID := range requirementIDs {
		out = append(out, model.Evidence{
			ID:         "ev/" + subject + "/pass/" + requirementID,
			SubjectID:  subject,
			Kind:       "test",
			Claim:      "behavioural test satisfied " + requirementID,
			Result:     model.EvidencePass,
			AppliesTo:  requirementID,
			ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		})
	}
	return out
}

func relevanceEvidence(subject string) model.Evidence {
	return model.Evidence{
		ID:         "ev/" + subject + "/match",
		SubjectID:  subject,
		Kind:       policy.KindDiscoveryMatch,
		Claim:      "surfaced by discovery",
		Result:     model.EvidenceInfo,
		ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}
}

// metadataEvidence is exactly what discovery plus enrichment can produce: no
// behavioural claim anywhere.
func metadataEvidence(subject, licence string, archived bool) []model.Evidence {
	return []model.Evidence{
		relevanceEvidence(subject),
		fact(subject, policy.KindSourceLicense, licence),
		fact(subject, policy.KindKnownAdvisoryCount, 0),
		fact(subject, policy.KindRepositoryArchived, archived),
		fact(subject, policy.KindPackageDeprecated, false),
		fact(subject, policy.KindSourceRevision, "9ca7c88e3243264f"),
	}
}

func seedRepository() *memoryRepository {
	repo := newMemoryRepository()
	repo.primitives[primitiveID] = model.Primitive{
		ID:          primitiveID,
		Name:        "Bounded subprocess",
		Description: "Run child processes with bounded output and distinct termination outcomes.",
		ContractID:  contractID,
		Tags:        []string{"process"},
	}
	repo.contracts[contractID] = model.Contract{
		ID:          contractID,
		PrimitiveID: primitiveID,
		Version:     "v1",
		Summary:     "Bounded child-process execution.",
		Requirements: func() []model.Requirement {
			out := make([]model.Requirement, 0, len(requirementIDs))
			for _, id := range requirementIDs {
				out = append(out, model.Requirement{ID: id, Description: id, Kind: "behavior", Required: true})
			}
			return out
		}(),
	}

	// Eligible: full behavioural evidence plus attributable metadata.
	eligible := model.Specimen{
		ID:          "fixture/eligible",
		PrimitiveID: primitiveID,
		Name:        "eligible implementation",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source: model.SourceRef{
			URL:      "https://example.invalid/eligible",
			Revision: "9ca7c88e3243264f",
			License:  "MIT",
		},
	}
	repo.specimens[eligible.ID] = eligible
	repo.evidence[eligible.ID] = append(behaviouralEvidence(eligible.ID), metadataEvidence(eligible.ID, "MIT", false)...)

	// Metadata only: plausible but unverified.
	metadata := model.Specimen{
		ID:          "fixture/metadata-only",
		PrimitiveID: primitiveID,
		Name:        "metadata only candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source: model.SourceRef{
			URL:      "https://example.invalid/metadata",
			Revision: "1111111111111111",
			License:  "MIT",
		},
	}
	repo.specimens[metadata.ID] = metadata
	repo.evidence[metadata.ID] = metadataEvidence(metadata.ID, "MIT", false)

	// Reference-shaped: same metadata, examined as knowledge not as code.
	reference := model.Specimen{
		ID:          "fixture/reference",
		PrimitiveID: primitiveID,
		Name:        "reference candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseReference, model.ReuseDependency},
		Source: model.SourceRef{
			URL:      "https://example.invalid/reference",
			Revision: "2222222222222222",
			License:  "MIT",
		},
	}
	repo.specimens[reference.ID] = reference
	repo.evidence[reference.ID] = metadataEvidence(reference.ID, "MIT", false)

	// Heavy: dependency count above any realistic ceiling.
	heavy := model.Specimen{
		ID:          "fixture/heavy",
		PrimitiveID: primitiveID,
		Name:        "heavy candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source:      model.SourceRef{URL: "https://example.invalid/heavy", Revision: "3333333333333333", License: "MIT"},
	}
	repo.specimens[heavy.ID] = heavy
	heavyEvidence := append(behaviouralEvidence(heavy.ID), metadataEvidence(heavy.ID, "MIT", false)...)
	heavyEvidence = append(heavyEvidence, fact(heavy.ID, policy.KindDirectDependencyCount, 8))
	repo.evidence[heavy.ID] = heavyEvidence

	// Light: small dependency count.
	light := model.Specimen{
		ID:          "fixture/light",
		PrimitiveID: primitiveID,
		Name:        "light candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source:      model.SourceRef{URL: "https://example.invalid/light", Revision: "4444444444444444", License: "MIT"},
	}
	repo.specimens[light.ID] = light
	lightEvidence := append(behaviouralEvidence(light.ID), metadataEvidence(light.ID, "MIT", false)...)
	lightEvidence = append(lightEvidence, fact(light.ID, policy.KindDirectDependencyCount, 2))
	repo.evidence[light.ID] = lightEvidence

	// GPL: behaviourally perfect but under a denied licence.
	gpl := model.Specimen{
		ID:          "fixture/gpl",
		PrimitiveID: primitiveID,
		Name:        "gpl candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source:      model.SourceRef{URL: "https://example.invalid/gpl", Revision: "5555555555555555", License: "GPL-3.0"},
	}
	repo.specimens[gpl.ID] = gpl
	repo.evidence[gpl.ID] = append(behaviouralEvidence(gpl.ID), metadataEvidence(gpl.ID, "GPL-3.0", false)...)

	// Archived: behaviourally perfect but archived.
	archived := model.Specimen{
		ID:          "fixture/archived",
		PrimitiveID: primitiveID,
		Name:        "archived candidate",
		ReuseMode:   []model.ReuseMode{model.ReuseDependency},
		Source:      model.SourceRef{URL: "https://example.invalid/archived", Revision: "6666666666666666", License: "MIT"},
	}
	repo.specimens[archived.ID] = archived
	repo.evidence[archived.ID] = append(behaviouralEvidence(archived.ID), metadataEvidence(archived.ID, "MIT", true)...)

	return repo
}

// apiPolicy is the deterministic profile the API tests evaluate under.
func apiPolicy() Policy {
	return Policy{
		SchemaVersion: 1,
		ID:            "api/test-v1",
		Reuse: PolicyReuse{
			Allowed:   []model.ReuseMode{model.ReuseCopy, model.ReuseDependency, model.ReuseReference},
			Preferred: []model.ReuseMode{model.ReuseDependency},
		},
		Licence: PolicyLicence{
			Allow:    []string{"MIT"},
			Deny:     []string{"GPL-3.0"},
			Unknown:  policy.ActionReview,
			Multiple: policy.ActionReview,
			Unlisted: policy.ActionReview,
		},
		Security:     PolicySecurity{KnownAdvisory: policy.ActionReview, Unknown: policy.ActionReview},
		Dependencies: PolicyDeps{Unknown: policy.ActionReview},
		Maintenance: PolicyMaint{
			Archived:   policy.ActionDeny,
			Deprecated: policy.ActionReview,
			Stale:      policy.ActionReview,
			Unknown:    policy.ActionReview,
		},
		Source:    PolicySource{RequireRevisionFor: []model.ReuseMode{model.ReuseDependency, model.ReuseCopy}, MissingRevision: policy.ActionReview},
		Selection: PolicySelection{MaxOptions: 3},
	}
}

func resolveHandler(t *testing.T, repo *memoryRepository) *Handler {
	t.Helper()
	deps := testDependencies()
	deps.Resolver = resolver.NewQualityService(repo, func() time.Time {
		return time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	})
	deps.Inspector = repo
	return newTestHandler(t, deps)
}

// resolveRequest builds a resolve body with the test policy.
func resolveRequest(policyBody Policy, candidates ...CandidateRef) string {
	encoded, err := json.Marshal(map[string]any{
		"primitive_id": primitiveID,
		"contract_id":  contractID,
		"candidates":   candidates,
		"policy":       policyBody,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func refineRequest(policyBody Policy, feedback []Feedback, candidates ...CandidateRef) string {
	encoded, err := json.Marshal(map[string]any{
		"primitive_id": primitiveID,
		"contract_id":  contractID,
		"candidates":   candidates,
		"policy":       policyBody,
		"feedback":     feedback,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func candidate(id string, mode model.ReuseMode) CandidateRef {
	return CandidateRef{SpecimenID: id, ReuseMode: mode}
}
