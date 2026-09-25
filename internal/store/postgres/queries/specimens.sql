-- name: UpsertSpecimen :one
INSERT INTO specimens (id, primitive_id, name, source_url, source_revision, source_path, source_license, reuse_modes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE
SET primitive_id    = EXCLUDED.primitive_id,
    name            = EXCLUDED.name,
    source_url      = EXCLUDED.source_url,
    source_revision = EXCLUDED.source_revision,
    source_path     = EXCLUDED.source_path,
    source_license  = EXCLUDED.source_license,
    reuse_modes     = EXCLUDED.reuse_modes
RETURNING *;

-- name: GetSpecimen :one
SELECT *
FROM specimens
WHERE id = $1;
