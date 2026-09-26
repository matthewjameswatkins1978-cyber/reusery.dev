package mcpserver

import (
	"context"
	"sort"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// projectRepo is an in-memory project.Repository so the real project service
// and the real Packet 7 quality service can run in MCP unit tests.
type projectRepo struct {
	primitives   map[string]model.Primitive
	contracts    map[string]model.Contract
	specimens    map[string]model.Specimen
	evidence     map[string][]model.Evidence
	resolutions  []model.Resolution
	projects     map[string]project.Project
	fingerprints []project.StoredFingerprint
	preferences  []project.Preference
	contexts     map[string]project.StoredContext
	feedback     []outcome.StoredFeedback
}

func newProjectRepo() *projectRepo {
	return &projectRepo{
		primitives: map[string]model.Primitive{},
		contracts:  map[string]model.Contract{},
		specimens:  map[string]model.Specimen{},
		evidence:   map[string][]model.Evidence{},
		projects:   map[string]project.Project{},
		contexts:   map[string]project.StoredContext{},
	}
}

func (r *projectRepo) GetPrimitive(_ context.Context, id string) (model.Primitive, error) {
	if value, ok := r.primitives[id]; ok {
		return value, nil
	}
	return model.Primitive{}, store.ErrNotFound
}

func (r *projectRepo) GetContract(_ context.Context, id string) (model.Contract, error) {
	if value, ok := r.contracts[id]; ok {
		return value, nil
	}
	return model.Contract{}, store.ErrNotFound
}

func (r *projectRepo) GetSpecimen(_ context.Context, id string) (model.Specimen, error) {
	if value, ok := r.specimens[id]; ok {
		return value, nil
	}
	return model.Specimen{}, store.ErrNotFound
}

// orderedEvidence sorts observations the way the store does: observed_at
// ascending then evidence id ascending.
func orderedEvidence(values []model.Evidence) []model.Evidence {
	out := append([]model.Evidence(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ObservedAt.Before(out[j].ObservedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (r *projectRepo) ListEvidenceBySubject(_ context.Context, id string) ([]model.Evidence, error) {
	return append([]model.Evidence(nil), r.evidence[id]...), nil
}

func (r *projectRepo) ListPrimitives(_ context.Context, limit int) ([]model.Primitive, error) {
	ids := make([]string, 0, len(r.primitives))
	for id := range r.primitives {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Primitive, 0, len(ids))
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		out = append(out, r.primitives[id])
	}
	return out, nil
}

func (r *projectRepo) ListEvidenceAfter(_ context.Context, id string, after time.Time, afterID string, limit int) ([]model.Evidence, error) {
	rows := orderedEvidence(r.evidence[id])
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

func (r *projectRepo) InsertResolution(_ context.Context, resolution model.Resolution) (int64, error) {
	r.resolutions = append(r.resolutions, resolution)
	return int64(len(r.resolutions)), nil
}

func (r *projectRepo) GetResolution(_ context.Context, id int64) (model.Resolution, error) {
	if id < 1 || id > int64(len(r.resolutions)) {
		return model.Resolution{}, store.ErrNotFound
	}
	return r.resolutions[id-1], nil
}

func (r *projectRepo) UpsertProject(_ context.Context, value project.Project) error {
	if existing, ok := r.projects[value.ID]; ok {
		value.CreatedAt = existing.CreatedAt
	}
	r.projects[value.ID] = value
	return nil
}

func (r *projectRepo) GetProject(_ context.Context, id string) (project.Project, error) {
	if value, ok := r.projects[id]; ok {
		return value, nil
	}
	return project.Project{}, store.ErrNotFound
}

func (r *projectRepo) InsertFingerprint(_ context.Context, value project.StoredFingerprint) (int64, error) {
	value.ID = int64(len(r.fingerprints) + 1)
	r.fingerprints = append(r.fingerprints, value)
	return value.ID, nil
}

func (r *projectRepo) GetFingerprintByHash(_ context.Context, projectID, sha string) (project.StoredFingerprint, error) {
	for _, value := range r.fingerprints {
		if value.ProjectID == projectID && value.SHA256 == sha {
			return value, nil
		}
	}
	return project.StoredFingerprint{}, store.ErrNotFound
}

func (r *projectRepo) GetLatestFingerprint(_ context.Context, projectID string) (project.StoredFingerprint, error) {
	for index := len(r.fingerprints) - 1; index >= 0; index-- {
		if r.fingerprints[index].ProjectID == projectID {
			return r.fingerprints[index], nil
		}
	}
	return project.StoredFingerprint{}, store.ErrNotFound
}

func (r *projectRepo) InsertPreference(_ context.Context, value project.Preference) (int64, error) {
	value.ID = int64(len(r.preferences) + 1)
	r.preferences = append(r.preferences, value)
	return value.ID, nil
}

func (r *projectRepo) GetPreference(_ context.Context, projectID string, id int64) (project.Preference, error) {
	for _, value := range r.preferences {
		if value.ProjectID == projectID && value.ID == id {
			return value, nil
		}
	}
	return project.Preference{}, store.ErrNotFound
}

func (r *projectRepo) ForgetPreference(_ context.Context, projectID string, id int64, at time.Time) error {
	for index := range r.preferences {
		if r.preferences[index].ProjectID == projectID && r.preferences[index].ID == id {
			r.preferences[index].ForgottenAt = at
			return nil
		}
	}
	return store.ErrNotFound
}

func (r *projectRepo) ListPreferences(_ context.Context, projectID string, limit int) ([]project.Preference, error) {
	out := make([]project.Preference, 0, limit)
	for _, value := range r.preferences {
		if value.ProjectID == projectID {
			out = append(out, value)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *projectRepo) ListActivePreferences(_ context.Context, projectID string, limit int) ([]project.Preference, error) {
	out := make([]project.Preference, 0, limit)
	for _, value := range r.preferences {
		if value.ProjectID == projectID && value.Active() {
			out = append(out, value)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *projectRepo) UpsertContext(_ context.Context, snapshot project.StoredContext) error {
	r.contexts[snapshot.Hash] = snapshot
	return nil
}

func (r *projectRepo) GetContext(_ context.Context, hash string) (project.StoredContext, error) {
	if value, ok := r.contexts[hash]; ok {
		return value, nil
	}
	return project.StoredContext{}, store.ErrNotFound
}

func (r *projectRepo) ListResolutions(_ context.Context, projectID string, limit int) ([]project.ProjectResolution, error) {
	matched := make([]project.ProjectResolution, 0, len(r.resolutions))
	for index, resolution := range r.resolutions {
		if resolution.ProjectID == projectID {
			matched = append(matched, project.ProjectResolution{ID: int64(index + 1), Resolution: resolution})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if !matched[i].Resolution.ResolvedAt.Equal(matched[j].Resolution.ResolvedAt) {
			return matched[i].Resolution.ResolvedAt.After(matched[j].Resolution.ResolvedAt)
		}
		return matched[i].ID > matched[j].ID
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

var _ project.Repository = (*projectRepo)(nil)
var _ resolver.Repository = (*projectRepo)(nil)

// InsertFeedback appends one post-resolution event.
func (r *projectRepo) InsertFeedback(_ context.Context, value outcome.Feedback) (int64, error) {
	r.feedback = append(r.feedback, outcome.StoredFeedback{
		ID:       int64(len(r.feedback) + 1),
		Feedback: value,
	})
	return int64(len(r.feedback)), nil
}

// ListFeedback returns stored events for one resolution.
func (r *projectRepo) ListFeedback(_ context.Context, resolutionID int64, limit int) ([]outcome.StoredFeedback, error) {
	out := make([]outcome.StoredFeedback, 0, limit)
	for _, value := range r.feedback {
		if value.ResolutionID == resolutionID {
			out = append(out, value)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
