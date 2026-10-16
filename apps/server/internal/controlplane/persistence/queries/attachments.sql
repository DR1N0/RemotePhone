-- name: CreateAttachment :one
INSERT INTO attachments (id, session_id, client_id, user_id, role, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetAttachmentByID :one
SELECT * FROM attachments WHERE id = ?;

-- name: ListAttachmentsForSession :many
SELECT * FROM attachments WHERE session_id = ? ORDER BY created_at;

-- name: ListAttachmentsForUser :many
SELECT * FROM attachments WHERE user_id = ?;

-- name: UpdateAttachmentRole :exec
UPDATE attachments SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateAttachmentStatus :exec
UPDATE attachments SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetAttachmentPendingTargetRole :exec
UPDATE attachments SET pending_target_role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetAttachmentTransportTeardownPending :exec
UPDATE attachments SET transport_teardown_pending = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
