ALTER TABLE attachments ADD COLUMN pending_target_role TEXT;
--> statement-breakpoint
UPDATE attachments
SET pending_target_role = 'observer'
WHERE restrictive_pending_role IS NOT NULL;
