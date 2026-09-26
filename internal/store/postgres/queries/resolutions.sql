-- name: InsertResolution :one
INSERT INTO resolutions
    (primitive_id, contract_id, outcome, specimen_id, reasons, unknowns,
     evidence_ids, policy_id, project_id, project_context_hash, resolved_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetResolution :one
SELECT *
FROM resolutions
WHERE id = $1;

-- name: CountResolutions :one
SELECT COUNT(*)
FROM resolutions;

-- name: InsertRejection :exec
INSERT INTO rejections (resolution_id, position, specimen_id, reasons)
VALUES ($1, $2, $3, $4);

-- name: ListRejectionsByResolution :many
SELECT *
FROM rejections
WHERE resolution_id = $1
ORDER BY position;
