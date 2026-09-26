-- name: InsertEvidence :one
INSERT INTO evidence (id, subject_id, kind, claim, result, source_url, source_revision, source_path, source_license, observed_at, applies_to, methodology, artifact)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetEvidence :one
SELECT *
FROM evidence
WHERE id = $1;

-- name: ListEvidenceBySubject :many
SELECT *
FROM evidence
WHERE subject_id = $1
ORDER BY observed_at, id;

-- name: ListEvidencePage :many
SELECT *
FROM evidence
WHERE subject_id = sqlc.arg(subject_id)
  AND (observed_at, id) > (sqlc.arg(after_observed_at)::timestamptz, sqlc.arg(after_id)::text)
ORDER BY observed_at, id
LIMIT sqlc.arg(page_limit)::int;
