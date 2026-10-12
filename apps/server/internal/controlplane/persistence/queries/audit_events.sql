-- name: CreateAuditEvent :exec
INSERT INTO audit_events (id, actor_user_id, actor_credential_id, action, target_type, target_id, request_id, outcome, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
