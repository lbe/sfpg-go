CREATE TABLE IF NOT EXISTS module_state (
  name TEXT PRIMARY KEY,
  is_active INTEGER NOT NULL,
  last_started_at INTEGER,
  last_finished_at INTEGER
);
