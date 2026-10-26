-- name: CreateUser :one
INSERT INTO users (id, display_name, user_type, username, password_hash, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE lower(trim(username)) = lower(trim(?));

-- name: CountRegisteredOrAdminUsers :one
SELECT count(*) FROM users WHERE user_type NOT IN ('guest', 'system');

-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE user_type = 'admin' AND status = 'active';

-- name: ListAccountUsers :many
SELECT * FROM users
WHERE user_type NOT IN ('guest', 'system')
  AND (sqlc.narg('cursor_id') IS NULL OR id > sqlc.narg('cursor_id'))
ORDER BY id ASC
LIMIT sqlc.arg('limit');

-- name: UpdateUserType :exec
UPDATE users SET user_type = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetUserStatus :exec
UPDATE users SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
