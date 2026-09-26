-- name: UpsertProject :one
INSERT INTO projects (id, name, source_kind, source_locator, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
ON CONFLICT (id) DO UPDATE
SET name           = EXCLUDED.name,
    source_kind    = EXCLUDED.source_kind,
    source_locator = EXCLUDED.source_locator,
    updated_at     = EXCLUDED.updated_at
RETURNING *;

-- name: GetProject :one
SELECT *
FROM projects
WHERE id = $1;

-- name: InsertProjectFingerprint :one
INSERT INTO project_fingerprints
    (project_id, schema_version, fingerprint_sha256, fingerprint_json, source_revision, observed_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetProjectFingerprintByHash :one
SELECT *
FROM project_fingerprints
WHERE project_id = $1
  AND fingerprint_sha256 = $2;

-- name: GetLatestProjectFingerprint :one
SELECT *
FROM project_fingerprints
WHERE project_id = $1
ORDER BY observed_at DESC, id DESC
LIMIT 1;

-- name: InsertProjectPreference :one
INSERT INTO project_preferences
    (project_id, kind, primitive_id, candidate_id, text_value, int_value,
     source_reason, source_resolution_id, recorded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetProjectPreference :one
SELECT *
FROM project_preferences
WHERE id = $1
  AND project_id = $2;

-- name: ForgetProjectPreference :exec
UPDATE project_preferences
SET forgotten_at = $3
WHERE id = $1
  AND project_id = $2
  AND forgotten_at IS NULL;

-- name: ListProjectPreferences :many
SELECT *
FROM project_preferences
WHERE project_id = $1
ORDER BY id
LIMIT $2;

-- name: ListActiveProjectPreferences :many
SELECT *
FROM project_preferences
WHERE project_id = $1
  AND forgotten_at IS NULL
ORDER BY id
LIMIT $2;

-- name: UpsertProjectContext :one
INSERT INTO project_contexts (hash, project_id, fingerprint_id, context_json, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (hash) DO UPDATE
SET project_id     = EXCLUDED.project_id,
    fingerprint_id = EXCLUDED.fingerprint_id,
    context_json   = EXCLUDED.context_json
RETURNING *;

-- name: GetProjectContext :one
SELECT *
FROM project_contexts
WHERE hash = $1;

-- name: ListResolutionsByProject :many
SELECT *
FROM resolutions
WHERE project_id = $1
ORDER BY resolved_at DESC, id DESC
LIMIT $2;
