ALTER TABLE principals ADD COLUMN user_type TEXT NOT NULL DEFAULT 'registered';
--> statement-breakpoint
ALTER TABLE principals ADD COLUMN username TEXT;
--> statement-breakpoint
ALTER TABLE principals ADD COLUMN password_hash TEXT;
--> statement-breakpoint
ALTER TABLE principals ADD COLUMN enrollment_required INTEGER NOT NULL DEFAULT 1;
--> statement-breakpoint
UPDATE principals SET user_type = 'admin' WHERE is_server_owner = 1;
--> statement-breakpoint
CREATE UNIQUE INDEX principals_username_ci ON principals(username COLLATE NOCASE) WHERE username IS NOT NULL;
--> statement-breakpoint
ALTER TABLE credentials ADD COLUMN scope TEXT NOT NULL DEFAULT 'account';
--> statement-breakpoint
ALTER TABLE credentials ADD COLUMN session_id TEXT;
--> statement-breakpoint
ALTER TABLE credentials ADD COLUMN attachment_id TEXT;
--> statement-breakpoint
ALTER TABLE credentials ADD COLUMN not_before TEXT;
--> statement-breakpoint
ALTER TABLE credentials ADD COLUMN expires_at TEXT;
--> statement-breakpoint
ALTER TABLE attachments ADD COLUMN transport_teardown_pending INTEGER NOT NULL DEFAULT 0;
--> statement-breakpoint
UPDATE attachments SET invite_token = NULL WHERE invite_token IS NOT NULL;
--> statement-breakpoint
CREATE TABLE session_invites (
  id TEXT PRIMARY KEY NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  created_by_user_id TEXT NOT NULL REFERENCES principals(id),
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  consumed_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
--> statement-breakpoint
CREATE TABLE audit_events (
  id TEXT PRIMARY KEY NOT NULL,
  actor_user_id TEXT REFERENCES principals(id),
  actor_credential_id TEXT REFERENCES credentials(id),
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  outcome TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
