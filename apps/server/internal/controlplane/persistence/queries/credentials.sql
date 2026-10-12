-- name: CreateCredential :one
INSERT INTO credentials (id, user_id, token_hash, device_label, scope, session_id, attachment_id, not_before, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetCredentialByTokenHash :one
SELECT * FROM credentials WHERE token_hash = ? AND revoked_at IS NULL;

-- name: GetCredentialByID :one
SELECT * FROM credentials WHERE id = ?;

-- name: RevokeCredential :exec
UPDATE credentials SET revoked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
