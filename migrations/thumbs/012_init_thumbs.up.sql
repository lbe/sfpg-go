-- Migration 012: Initialize thumbnail_blobs in thumbs.db
-- No foreign key constraint: SQLite does not enforce cross-database FK references.
CREATE TABLE IF NOT EXISTS thumbnail_blobs (
  thumbnail_id INTEGER PRIMARY KEY,
  data         BLOB NOT NULL
);
