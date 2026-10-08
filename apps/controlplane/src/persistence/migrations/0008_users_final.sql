ALTER TABLE principals RENAME TO users;
--> statement-breakpoint
ALTER TABLE credentials RENAME COLUMN principal_id TO user_id;
--> statement-breakpoint
ALTER TABLE attachments RENAME COLUMN principal_id TO user_id;
--> statement-breakpoint
ALTER TABLE pairing_invites RENAME COLUMN consumed_by_principal_id TO consumed_by_user_id;
