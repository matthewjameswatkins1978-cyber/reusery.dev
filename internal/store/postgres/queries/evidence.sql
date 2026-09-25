-- name: InsertEvidence :one
INSERT INTO evidence (id, subject_id, kind, claim, result, source_url, source_revision, source_path, source_license, observed_at, applies_to, methodology, artifact)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListEvidenceBySubject :many
SELECT *
FROM evidence
WHERE subject_id = $1
ORDER BY observed_at, id;
