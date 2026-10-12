-- name: GetSessionStatusForAuth :one
SELECT status FROM sessions WHERE id = ?;

-- name: GetAttachmentForAuth :one
SELECT session_id, user_id FROM attachments WHERE id = ?;
