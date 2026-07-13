CREATE TABLE `attachments` (
	`id` text PRIMARY KEY NOT NULL,
	`session_id` text NOT NULL,
	`client_id` text NOT NULL,
	`role` text NOT NULL,
	`status` text NOT NULL,
	`reconnect_token` text,
	`created_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	FOREIGN KEY (`session_id`) REFERENCES `sessions`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE TABLE `instances` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`runtime_class` text NOT NULL,
	`template_id` text NOT NULL,
	`placement_policy_json` text NOT NULL,
	`resource_profile_json` text NOT NULL,
	`launch_config_json` text NOT NULL,
	`desired_state` text NOT NULL,
	`observed_state` text NOT NULL,
	`runtime_handle_json` text,
	`state_data_ref` text,
	`last_error` text,
	`created_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
--> statement-breakpoint
CREATE TABLE `sessions` (
	`id` text PRIMARY KEY NOT NULL,
	`instance_id` text NOT NULL,
	`status` text NOT NULL,
	`idle_expires_at` text,
	`reconnect_token` text,
	`created_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	FOREIGN KEY (`instance_id`) REFERENCES `instances`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE TABLE `settings` (
	`key` text PRIMARY KEY NOT NULL,
	`value_json` text NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
