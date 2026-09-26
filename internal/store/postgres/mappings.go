package postgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
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
		ResolvedAt:  timeToPg(r.ResolvedAt),
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
		PrimitiveID: row.PrimitiveID,
		ContractID:  row.ContractID,
		Outcome:     model.Outcome(row.Outcome),
		SpecimenID:  derefString(row.SpecimenID),
		Reasons:     nonNilStrings(row.Reasons),
		Rejected:    rejected,
		Unknowns:    nonNilStrings(row.Unknowns),
		EvidenceIDs: nonNilStrings(row.EvidenceIds),
		PolicyID:    row.PolicyID,
		ResolvedAt:  row.ResolvedAt.Time,
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
