ALTER TABLE instances ADD COLUMN owner_id TEXT NOT NULL DEFAULT '' REFERENCES principals(id);
--> statement-breakpoint
ALTER TABLE attachments ADD COLUMN principal_id TEXT REFERENCES principals(id);
