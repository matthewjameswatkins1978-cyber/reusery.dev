-- name: UpsertContract :one
INSERT INTO contracts (id, primitive_id, version, summary)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET primitive_id = EXCLUDED.primitive_id,
    version      = EXCLUDED.version,
    summary      = EXCLUDED.summary
RETURNING *;

-- name: GetContract :one
SELECT *
FROM contracts
WHERE id = $1;

-- name: DeleteContractRequirements :exec
DELETE FROM requirements
WHERE contract_id = $1;

-- name: InsertRequirement :exec
INSERT INTO requirements (contract_id, requirement_id, position, description, kind, required)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListRequirementsByContract :many
SELECT *
FROM requirements
WHERE contract_id = $1
ORDER BY position;
