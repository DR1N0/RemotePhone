-- name: CreateUser :one
INSERT INTO users (id, display_name, user_type, username, password_hash, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE lower(trim(username)) = lower(trim(?));

-- name: CountRegisteredOrAdminUsers :one
SELECT count(*) FROM users WHERE user_type != 'guest';
