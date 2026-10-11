CREATE TABLE workers (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  listen_port INTEGER,
  base_url TEXT,
  pid INTEGER,
  phase TEXT NOT NULL DEFAULT 'pending',
  installed_runtimes_json TEXT,
  resource_capacity_json TEXT,
  last_health_check_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
