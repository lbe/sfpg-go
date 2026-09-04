-- Migration rollback: drop payload from module_state.
-- Recreate the table without payload (CREATE -> INSERT -> DROP -> RENAME)
-- to avoid ALTER TABLE DROP COLUMN (not supported in older SQLite versions).

CREATE TABLE module_state_new (
  name TEXT PRIMARY KEY,
  is_active INTEGER NOT NULL,
  last_started_at INTEGER,
  last_finished_at INTEGER
);

INSERT INTO module_state_new (name, is_active, last_started_at, last_finished_at)
  SELECT name, is_active, last_started_at, last_finished_at FROM module_state;

DROP TABLE module_state;

ALTER TABLE module_state_new RENAME TO module_state;