-- name: CreateSession :one
INSERT INTO sessions (id, instance_id, status, owner_absence_timeout_ms, created_at, updated_at)
VALUES (?, ?, 'active', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessionsForInstance :many
SELECT * FROM sessions WHERE instance_id = ? ORDER BY created_at;

-- name: SetOwnerAbsenceDeadline :exec
UPDATE sessions SET owner_absence_deadline = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: ClearOwnerAbsenceDeadline :exec
UPDATE sessions SET owner_absence_deadline = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: CloseSession :exec
UPDATE sessions SET status = 'closed', owner_absence_deadline = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
