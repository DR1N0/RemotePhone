-- Unified access codes.
--
-- target_user_id NULL: an admin-issued code that may create a new ordinary
-- user or add a device to an existing account (server decides at redemption).
-- target_user_id set: a user-issued code bound to that account.
--
-- Only a hash of the code is stored. Codes are single-use and expire.
CREATE TABLE IF NOT EXISTS access_codes (
  id TEXT PRIMARY KEY NOT NULL,
  code_hash TEXT NOT NULL UNIQUE,
  target_user_id TEXT REFERENCES users(id),
  created_by_user_id TEXT REFERENCES users(id),
  expires_at TEXT NOT NULL,
  used_at TEXT,
  failed_attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS access_codes_target ON access_codes(target_user_id);
CREATE INDEX IF NOT EXISTS access_codes_expiry ON access_codes(expires_at);
