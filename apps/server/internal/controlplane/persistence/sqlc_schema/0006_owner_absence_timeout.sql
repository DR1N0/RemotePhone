ALTER TABLE sessions RENAME COLUMN idle_expires_at TO owner_absence_deadline;
--> statement-breakpoint
ALTER TABLE sessions ADD COLUMN owner_absence_timeout_ms INTEGER;
