ALTER TABLE users DROP COLUMN is_server_owner;
--> statement-breakpoint
ALTER TABLE users DROP COLUMN enrollment_required;
--> statement-breakpoint
ALTER TABLE pairing_invites RENAME TO registration_invites;
