CREATE TABLE templates (
  id TEXT PRIMARY KEY,
  runtime_class TEXT NOT NULL,
  display_name TEXT NOT NULL,
  current_base_image_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
--> statement-breakpoint
CREATE TABLE base_images (
  id TEXT PRIMARY KEY,
  template_id TEXT NOT NULL REFERENCES templates(id),
  source_instance_id TEXT NOT NULL REFERENCES instances(id),
  artifact_path TEXT NOT NULL,
  created_by_user_id TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
