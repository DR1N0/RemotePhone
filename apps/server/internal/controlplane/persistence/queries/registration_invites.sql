-- name: CreateRegistrationInvite :one
INSERT INTO registration_invites (id, token_hash, created_by, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetRegistrationInviteByTokenHash :one
SELECT * FROM registration_invites WHERE token_hash = ?;

-- name: ConsumeRegistrationInvite :execrows
UPDATE registration_invites
SET consumed_at = ?, consumed_by_user_id = ?
WHERE id = ? AND consumed_at IS NULL AND expires_at > ?;
