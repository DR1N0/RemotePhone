-- name: CreateAuditEvent :exec
INSERT INTO audit_events (id, actor_user_id, actor_credential_id, action, target_type, target_id, request_id, outcome, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);

-- name: GetAuditEventByID :one
SELECT * FROM audit_events WHERE id = ?;

-- name: ListAuditEvents :many
SELECT * FROM audit_events
WHERE sqlc.narg('cursor_created_at') IS NULL
   OR created_at < sqlc.narg('cursor_created_at')
   OR (created_at = sqlc.narg('cursor_created_at') AND id < sqlc.narg('cursor_id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit');
