package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres/sqlc"
)

// This file is the only place where database rows and domain values meet.
// Nothing here alters domain semantics: arrays stay ordered arrays, empty
// strings map to empty columns, and an empty SpecimenID becomes SQL NULL.

func primitiveToUpsert(p model.Primitive) sqlc.UpsertPrimitiveParams {
	return sqlc.UpsertPrimitiveParams{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Tags:        nonNilStrings(p.Tags),
		ContractID:  p.ContractID,
	}
}

func primitiveFromRow(row sqlc.Primitive) model.Primitive {
	return model.Primitive{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Tags:        nonNilStrings(row.Tags),
		ContractID:  row.ContractID,
	}
}

func contractToUpsert(c model.Contract) sqlc.UpsertContractParams {
	return sqlc.UpsertContractParams{
		ID:          c.ID,
		PrimitiveID: c.PrimitiveID,
		Version:     c.Version,
		Summary:     c.Summary,
	}
}

func requirementToInsert(contractID string, position int, r model.Requirement) sqlc.InsertRequirementParams {
	return sqlc.InsertRequirementParams{
		ContractID:    contractID,
		RequirementID: r.ID,
		Position:      int32(position),
		Description:   r.Description,
		Kind:          r.Kind,
		Required:      r.Required,
	}
}

func contractFromRows(row sqlc.Contract, requirements []sqlc.Requirement) model.Contract {
	reqs := make([]model.Requirement, 0, len(requirements))
	for _, r := range requirements {
		reqs = append(reqs, model.Requirement{
			ID:          r.RequirementID,
			Description: r.Description,
			Kind:        r.Kind,
			Required:    r.Required,
		})
	}
	return model.Contract{
		ID:           row.ID,
		PrimitiveID:  row.PrimitiveID,
		Version:      row.Version,
		Summary:      row.Summary,
		Requirements: reqs,
	}
}

func specimenToUpsert(s model.Specimen) sqlc.UpsertSpecimenParams {
	return sqlc.UpsertSpecimenParams{
		ID:             s.ID,
		PrimitiveID:    s.PrimitiveID,
		Name:           s.Name,
		SourceUrl:      s.Source.URL,
		SourceRevision: s.Source.Revision,
		SourcePath:     s.Source.Path,
		SourceLicense:  s.Source.License,
		ReuseModes:     reuseModesToStrings(s.ReuseMode),
	}
}

func specimenFromRow(row sqlc.Specimen) model.Specimen {
	return model.Specimen{
		ID:          row.ID,
		PrimitiveID: row.PrimitiveID,
		Name:        row.Name,
		Source: model.SourceRef{
			URL:      row.SourceUrl,
			Revision: row.SourceRevision,
			Path:     row.SourcePath,
			License:  row.SourceLicense,
		},
		ReuseMode: stringsToReuseModes(row.ReuseModes),
	}
}

func evidenceToInsert(e model.Evidence) sqlc.InsertEvidenceParams {
	return sqlc.InsertEvidenceParams{
		ID:             e.ID,
		SubjectID:      e.SubjectID,
		Kind:           e.Kind,
		Claim:          e.Claim,
		Result:         string(e.Result),
		SourceUrl:      e.Source.URL,
		SourceRevision: e.Source.Revision,
		SourcePath:     e.Source.Path,
		SourceLicense:  e.Source.License,
		ObservedAt:     timeToPg(e.ObservedAt),
		AppliesTo:      e.AppliesTo,
		Methodology:    e.Methodology,
		Artifact:       e.Artifact,
	}
}

func evidenceFromRow(row sqlc.Evidence) model.Evidence {
	return model.Evidence{
		ID:        row.ID,
		SubjectID: row.SubjectID,
		Kind:      row.Kind,
		Claim:     row.Claim,
		Result:    model.EvidenceResult(row.Result),
		Source: model.SourceRef{
			URL:      row.SourceUrl,
			Revision: row.SourceRevision,
			Path:     row.SourcePath,
			License:  row.SourceLicense,
		},
		ObservedAt:  row.ObservedAt.Time,
		AppliesTo:   row.AppliesTo,
		Methodology: row.Methodology,
		Artifact:    row.Artifact,
	}
}

func resolutionToInsert(r model.Resolution) sqlc.InsertResolutionParams {
	return sqlc.InsertResolutionParams{
		PrimitiveID: r.PrimitiveID,
		ContractID:  r.ContractID,
		Outcome:     string(r.Outcome),
		SpecimenID:  optionalString(r.SpecimenID),
		Reasons:     nonNilStrings(r.Reasons),
		Unknowns:    nonNilStrings(r.Unknowns),
		EvidenceIds: nonNilStrings(r.EvidenceIDs),
		PolicyID:    r.PolicyID,
		// Empty means the decision carried no project context, which is every
		// Packet 1-9 decision. NULL rather than '' because both columns are
		// foreign keys.
		ProjectID:          optionalString(r.ProjectID),
		ProjectContextHash: optionalString(r.ProjectContextHash),
		ResolvedAt:         timeToPg(r.ResolvedAt),
	}
}

func rejectionToInsert(resolutionID int64, position int, r model.Rejection) sqlc.InsertRejectionParams {
	return sqlc.InsertRejectionParams{
		ResolutionID: resolutionID,
		Position:     int32(position),
		SpecimenID:   r.SpecimenID,
		Reasons:      nonNilStrings(r.Reasons),
	}
}

func resolutionFromRows(row sqlc.Resolution, rejections []sqlc.Rejection) model.Resolution {
	rejected := make([]model.Rejection, 0, len(rejections))
	for _, r := range rejections {
		rejected = append(rejected, model.Rejection{
			SpecimenID: r.SpecimenID,
			Reasons:    nonNilStrings(r.Reasons),
		})
	}
	return model.Resolution{
		PrimitiveID:        row.PrimitiveID,
		ContractID:         row.ContractID,
		Outcome:            model.Outcome(row.Outcome),
		SpecimenID:         derefString(row.SpecimenID),
		Reasons:            nonNilStrings(row.Reasons),
		Rejected:           rejected,
		Unknowns:           nonNilStrings(row.Unknowns),
		EvidenceIDs:        nonNilStrings(row.EvidenceIds),
		PolicyID:           row.PolicyID,
		ProjectID:          derefString(row.ProjectID),
		ProjectContextHash: derefString(row.ProjectContextHash),
		ResolvedAt:         row.ResolvedAt.Time,
	}
}

func timeToPg(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func reuseModesToStrings(modes []model.ReuseMode) []string {
	if len(modes) == 0 {
		return []string{}
	}
	out := make([]string, len(modes))
	for i, mode := range modes {
		out[i] = string(mode)
	}
	return out
}

func stringsToReuseModes(values []string) []model.ReuseMode {
	if len(values) == 0 {
		return nil
	}
	out := make([]model.ReuseMode, len(values))
	for i, value := range values {
		out[i] = model.ReuseMode(value)
	}
	return out
}

// --------------------------------------------------------------- projects

func projectToUpsert(p project.Project) sqlc.UpsertProjectParams {
	return sqlc.UpsertProjectParams{
		ID:            p.ID,
		Name:          p.Name,
		SourceKind:    string(p.SourceKind),
		SourceLocator: p.SourceLocator,
		CreatedAt:     timeToPg(p.CreatedAt),
	}
}

func projectFromRow(row sqlc.Project) project.Project {
	return project.Project{
		ID:            row.ID,
		Name:          row.Name,
		SourceKind:    project.SourceKind(row.SourceKind),
		SourceLocator: row.SourceLocator,
		CreatedAt:     row.CreatedAt.Time,
		UpdatedAt:     row.UpdatedAt.Time,
	}
}

func fingerprintToInsert(f project.StoredFingerprint) (sqlc.InsertProjectFingerprintParams, error) {
	encoded, err := json.Marshal(f.Fingerprint)
	if err != nil {
		return sqlc.InsertProjectFingerprintParams{}, fmt.Errorf("encode fingerprint: %w", err)
	}
	return sqlc.InsertProjectFingerprintParams{
		ProjectID:         f.ProjectID,
		SchemaVersion:     int32(f.SchemaVersion),
		FingerprintSha256: f.SHA256,
		FingerprintJson:   encoded,
		SourceRevision:    f.SourceRevision,
		ObservedAt:        timeToPg(f.ObservedAt),
	}, nil
}

// fingerprintFromRow reloads a stored fingerprint and refuses to hand back
// anything that does not validate against the supported schema. Guessing at a
// corrupt project fingerprint would silently misattribute every decision that
// cites it.
func fingerprintFromRow(row sqlc.ProjectFingerprint) (project.StoredFingerprint, error) {
	var fingerprint project.Fingerprint
	if err := json.Unmarshal(row.FingerprintJson, &fingerprint); err != nil {
		return project.StoredFingerprint{}, fmt.Errorf("%w: stored fingerprint is not valid JSON: %v",
			project.ErrInvalidProjectContext, err)
	}
	if fingerprint.SchemaVersion != project.SchemaVersion ||
		fingerprint.Language == "" || len(fingerprint.Modules) == 0 {
		return project.StoredFingerprint{}, fmt.Errorf(
			"%w: stored fingerprint does not match schema version %d",
			project.ErrInvalidProjectContext, project.SchemaVersion)
	}
	return project.StoredFingerprint{
		ID:             row.ID,
		ProjectID:      row.ProjectID,
		SchemaVersion:  int(row.SchemaVersion),
		SHA256:         row.FingerprintSha256,
		Fingerprint:    fingerprint,
		SourceRevision: row.SourceRevision,
		ObservedAt:     row.ObservedAt.Time,
	}, nil
}

func preferenceToInsert(p project.Preference) sqlc.InsertProjectPreferenceParams {
	return sqlc.InsertProjectPreferenceParams{
		ProjectID:          p.ProjectID,
		Kind:               string(p.Kind),
		PrimitiveID:        p.PrimitiveID,
		CandidateID:        p.CandidateID,
		TextValue:          p.TextValue,
		IntValue:           intPointer(p.IntValue),
		SourceReason:       string(p.SourceReason),
		SourceResolutionID: int64Pointer(p.SourceResolutionID),
		RecordedAt:         timeToPg(p.RecordedAt),
	}
}

func preferenceFromRow(row sqlc.ProjectPreference) project.Preference {
	return project.Preference{
		ID:                 row.ID,
		ProjectID:          row.ProjectID,
		Kind:               project.PreferenceKind(row.Kind),
		PrimitiveID:        row.PrimitiveID,
		CandidateID:        row.CandidateID,
		TextValue:          row.TextValue,
		IntValue:           int32Pointer(row.IntValue),
		SourceReason:       policy.FeedbackReason(row.SourceReason),
		SourceResolutionID: derefInt64(row.SourceResolutionID),
		RecordedAt:         row.RecordedAt.Time,
		ForgottenAt:        forgottenTime(row.ForgottenAt),
	}
}

func contextToUpsert(c project.StoredContext) (sqlc.UpsertProjectContextParams, error) {
	encoded, err := json.Marshal(c.Context)
	if err != nil {
		return sqlc.UpsertProjectContextParams{}, fmt.Errorf("encode project context: %w", err)
	}
	return sqlc.UpsertProjectContextParams{
		Hash:          c.Hash,
		ProjectID:     c.ProjectID,
		FingerprintID: c.FingerprintID,
		ContextJson:   encoded,
		CreatedAt:     timeToPg(c.CreatedAt),
	}, nil
}

// contextFromRow reloads a stored snapshot and refuses anything that does not
// validate, so a decision can never be reinterpreted through corrupt context.
func contextFromRow(row sqlc.ProjectContext) (project.StoredContext, error) {
	var snapshot project.Context
	if err := json.Unmarshal(row.ContextJson, &snapshot); err != nil {
		return project.StoredContext{}, fmt.Errorf("%w: stored project context is not valid JSON: %v",
			project.ErrInvalidProjectContext, err)
	}
	if snapshot.SchemaVersion != project.SchemaVersion || snapshot.ProjectID == "" ||
		snapshot.FingerprintSHA256 == "" {
		return project.StoredContext{}, fmt.Errorf(
			"%w: stored project context does not match schema version %d",
			project.ErrInvalidProjectContext, project.SchemaVersion)
	}
	return project.StoredContext{
		Hash:          row.Hash,
		ProjectID:     row.ProjectID,
		FingerprintID: row.FingerprintID,
		Context:       snapshot,
		CreatedAt:     row.CreatedAt.Time,
	}, nil
}

// forgottenTime converts a nullable timestamp into a domain time, treating
// NULL as "still active".
func forgottenTime(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func intPointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	converted := int32(*value)
	return &converted
}

func int32Pointer(value *int32) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}

func int64Pointer(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
