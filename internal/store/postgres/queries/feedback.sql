-- name: InsertResolutionFeedback :one
INSERT INTO resolution_feedback (resolution_id, kind, note, recorded_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListResolutionFeedback :many
SELECT *
FROM resolution_feedback
WHERE resolution_id = $1
ORDER BY recorded_at, id
LIMIT $2;
