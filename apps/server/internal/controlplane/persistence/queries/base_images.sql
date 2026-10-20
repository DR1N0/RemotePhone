-- name: CreateBaseImage :one
INSERT INTO base_images (id, template_id, source_instance_id, artifact_path, created_by_user_id, created_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetBaseImageByID :one
SELECT * FROM base_images WHERE id = ?;

-- name: ListBaseImagesByTemplate :many
SELECT * FROM base_images WHERE template_id = ? ORDER BY created_at DESC;
