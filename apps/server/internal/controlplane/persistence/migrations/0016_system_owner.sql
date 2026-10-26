-- Reserved local-owner principal for actions performed through the loopback
-- admin console. It has no username/password and cannot authenticate; the
-- audit actor and credential FKs stay valid. User listings and setup/admin
-- counts exclude this row.
INSERT OR IGNORE INTO users
  (id, display_name, user_type, username, password_hash, status, created_at, updated_at)
VALUES
  ('_SYSTEM', 'Local owner', 'system', NULL, NULL, 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

INSERT OR IGNORE INTO credentials
  (id, user_id, token_hash, device_label, scope, created_at, updated_at)
VALUES
  ('_SYSTEM', '_SYSTEM', '_SYSTEM', 'local owner', 'account', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
