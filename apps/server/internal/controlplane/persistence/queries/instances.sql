-- name: CreateInstance :one
INSERT INTO instances (
  id, name, runtime_class, template_id, placement_policy_json,
  resource_profile_json, launch_config_json, desired_state, observed_state,
  runtime_handle_json, state_data_ref, last_error, owner_id, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, 'stopped', 'stopped', NULL, NULL, NULL, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetInstanceByID :one
SELECT * FROM instances WHERE id = ?;

-- name: ListInstancesByOwner :many
SELECT * FROM instances WHERE owner_id = ? ORDER BY created_at;

-- name: ListInstances :many
SELECT * FROM instances ORDER BY created_at;

-- name: ListInstancesByDesiredState :many
SELECT * FROM instances WHERE desired_state = ? ORDER BY created_at;

-- name: UpdateInstanceLifecycle :exec
UPDATE instances
SET desired_state = ?, observed_state = ?, runtime_handle_json = ?, state_data_ref = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateInstanceRuntime :exec
UPDATE instances
SET observed_state = ?, runtime_handle_json = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteInstanceByID :exec
DELETE FROM instances WHERE id = ?;
