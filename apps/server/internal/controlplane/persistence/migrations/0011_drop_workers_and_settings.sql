DROP TABLE workers;
--> statement-breakpoint
DROP TABLE settings;
--> statement-breakpoint
CREATE UNIQUE INDEX instances_unique_owner ON instances(owner_id);
