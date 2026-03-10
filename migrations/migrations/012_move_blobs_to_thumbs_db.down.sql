-- Migration 012 down: Restore thumbnail_blobs to main DB
CREATE TABLE IF NOT EXISTS thumbnail_blobs (
  thumbnail_id INTEGER PRIMARY KEY REFERENCES thumbnails(id) ON DELETE CASCADE,
  data         BLOB NOT NULL
);
