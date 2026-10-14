-- name: CreateSessionInvite :one
INSERT INTO session_invites (id, session_id, created_by_user_id, token_hash, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetSessionInviteByTokenHash :one
SELECT * FROM session_invites WHERE token_hash = ?;

-- name: ConsumeSessionInvite :execrows
UPDATE session_invites
SET consumed_at = ?
WHERE id = ? AND consumed_at IS NULL AND expires_at > ?;
