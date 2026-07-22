-- Migration: Drop encoding and content_encoding from http_cache
-- These columns are no longer needed since compression middleware was removed
-- and cache keys are encoding-independent.

-- Use SQLite-safe table recreation: CREATE -> INSERT -> DROP -> RENAME
-- to avoid ALTER TABLE DROP COLUMN (not supported in older SQLite versions).

DELETE FROM http_cache;

CREATE TABLE IF NOT EXISTS http_cache_new (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  key                 TEXT NOT NULL UNIQUE,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,
  query_string        TEXT,
  status              INTEGER NOT NULL,
  content_type        TEXT,
  cache_control       TEXT,
  etag                TEXT,
  last_modified       TEXT,
  vary                TEXT,
  body                BLOB NOT NULL,
  content_length      INTEGER,
  created_at          INTEGER NOT NULL,
  expires_at          INTEGER
);

INSERT INTO http_cache_new (id, key, method, path, query_string, status, content_type, cache_control, etag, last_modified, vary, body, content_length, created_at, expires_at)
  SELECT id, key, method, path, query_string, status, content_type, cache_control, etag, last_modified, vary, body, content_length, created_at, expires_at FROM http_cache;

DROP TABLE http_cache;

ALTER TABLE http_cache_new RENAME TO http_cache;

CREATE INDEX IF NOT EXISTS idx_http_cache_key ON http_cache(key);
CREATE INDEX IF NOT EXISTS idx_http_cache_path ON http_cache(path);
CREATE INDEX IF NOT EXISTS idx_http_cache_created ON http_cache(created_at);
CREATE INDEX IF NOT EXISTS idx_http_cache_expires ON http_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_http_cache_content_length ON http_cache(content_length);
