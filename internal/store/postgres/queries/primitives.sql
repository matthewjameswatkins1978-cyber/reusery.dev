-- name: UpsertPrimitive :one
INSERT INTO primitives (id, name, description, tags, contract_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
SET name        = EXCLUDED.name,
    description = EXCLUDED.description,
    tags        = EXCLUDED.tags,
    contract_id = EXCLUDED.contract_id
RETURNING *;

-- name: GetPrimitive :one
SELECT *
FROM primitives
WHERE id = $1;
