-- name: CreateTemplate :one
INSERT INTO templates (id, runtime_class, display_name, created_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetTemplateByID :one
SELECT * FROM templates WHERE id = ?;

-- name: UpdateTemplateCurrentBaseImage :exec
UPDATE templates SET current_base_image_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
