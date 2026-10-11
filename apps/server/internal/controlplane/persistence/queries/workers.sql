-- name: CreateWorker :exec
INSERT INTO workers (id, kind, phase, created_at, updated_at)
VALUES (?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- name: GetWorker :one
SELECT * FROM workers WHERE id = ?;

-- name: ListWorkers :many
SELECT * FROM workers ORDER BY created_at;
