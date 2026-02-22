-- Migration: Initial Schema (Flattened)
-- This migration combines all previous migrations into a single initial schema.
-- Created 2025-02-11 by flattening migrations 001-010.

-- File paths table
CREATE TABLE file_paths (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL
);

-- Folder paths table
CREATE TABLE folder_paths (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL
);

-- Folders table
CREATE TABLE "folders" (
  id         INTEGER PRIMARY KEY,
  parent_id  INTEGER REFERENCES folders(id) ON DELETE CASCADE,
  path_id    INTEGER NOT NULL UNIQUE REFERENCES folder_paths(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  mtime      INTEGER,
  tile_id    INTEGER REFERENCES files(id) ON DELETE SET NULL,
  created_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now')),
  updated_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now'))
);

-- Files table
CREATE TABLE files (
  id INTEGER PRIMARY KEY,
  folder_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
  path_id INTEGER NOT NULL UNIQUE REFERENCES file_paths(id) ON DELETE CASCADE,
  filename TEXT NOT NULL,
  size_bytes INTEGER,
	mtime INTEGER,
  md5 TEXT,
	phash INTEGER,
  mime_type TEXT,
  width INTEGER,
  height INTEGER,
  created_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now')),
  updated_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now'))
);

-- Index for files lookups
CREATE INDEX idx_files_folder_id_filename ON files(folder_id, filename);

-- Thumbnails table
CREATE TABLE thumbnails (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  size_label TEXT NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL,
  format TEXT NOT NULL,
  created_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now')),
  updated_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now')),
  UNIQUE(file_id,size_label)
);

-- Thumbnail blobs table
CREATE TABLE thumbnail_blobs (
  thumbnail_id INTEGER PRIMARY KEY REFERENCES thumbnails(id) ON DELETE CASCADE,
  data BLOB NOT NULL
);

-- EXIF metadata table
CREATE TABLE exif_metadata (
       file_id INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE
    ,  camera_make TEXT
    ,  camera_model TEXT
    ,  lens_model TEXT
    ,  focal_length TEXT
    ,  aperture TEXT
    ,  shutter_speed TEXT
    ,  iso INTEGER
    ,  orientation INTEGER
    ,  latitude REAL
    ,  longitude REAL
    ,  altitude REAL
    ,  capture_date INTEGER
    ,  software TEXT
);

-- IPTC metadata table
CREATE TABLE iptc_metadata (
  file_id INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  title TEXT, description TEXT, keywords TEXT, creator TEXT,
  copyright TEXT, credit TEXT, source TEXT, created_date INTEGER
);

-- IPTC keywords table
CREATE TABLE iptc_keywords (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  keyword TEXT NOT NULL
);

-- XMP properties table
CREATE TABLE xmp_properties (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  namespace TEXT NOT NULL, property TEXT NOT NULL, value TEXT
);

-- XMP raw table
CREATE TABLE xmp_raw (
  file_id INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  raw_xml TEXT
);

-- Config table
CREATE TABLE config (
	key TEXT NOT NULL PRIMARY KEY,
	value TEXT NOT NULL,
  created_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now')),
  updated_at INT64 NOT NULL DEFAULT (UNIXEPOCH('now'))
, type TEXT, category TEXT, requires_restart INTEGER NOT NULL DEFAULT 0, description TEXT, default_value TEXT, help_text TEXT, example_value TEXT);

-- HTTP cache table
CREATE TABLE http_cache (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  key                 TEXT NOT NULL UNIQUE,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,
  query_string        TEXT,
  encoding            TEXT NOT NULL,
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
, content_encoding TEXT);

-- Indexes for HTTP cache
CREATE INDEX idx_http_cache_key ON http_cache(key);
CREATE INDEX idx_http_cache_path ON http_cache(path);
CREATE INDEX idx_http_cache_encoding ON http_cache(encoding);
CREATE INDEX idx_http_cache_created ON http_cache(created_at);
CREATE INDEX idx_http_cache_expires ON http_cache(expires_at);
CREATE INDEX idx_http_cache_content_length ON http_cache(content_length);

-- Login attempts table
CREATE TABLE login_attempts (
  username TEXT NOT NULL PRIMARY KEY,
  failed_attempts INTEGER NOT NULL DEFAULT 0,
  locked_until INTEGER,
  last_attempt_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now'))
);

-- Invalid files table
CREATE TABLE invalid_files (
    path TEXT NOT NULL PRIMARY KEY,
    mtime INTEGER NOT NULL,
    size INTEGER NOT NULL,
    reason TEXT,
    created_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now')),
    updated_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now'))
);

-- Views

-- Folder view
CREATE VIEW folder_view AS
  SELECT f.id, f.parent_id, p.path, f.name, f.mtime, f.created_at, f.updated_at
    FROM folders f
         INNER JOIN folder_paths p
                 ON f.path_id = p.id;

-- File view
CREATE VIEW file_view AS
  SELECT f.id, f.folder_id, fp.path AS folder_path, p.path, f.filename
       , f.size_bytes, f.mtime, f.md5, f.phash, f.mime_type
       , f.width, f.height, f.created_at, f.updated_at
    FROM files f
         INNER JOIN file_paths p ON f.path_id = p.id
         LEFT JOIN folders fld ON f.folder_id = fld.id
         LEFT JOIN folder_paths fp ON fld.path_id = fp.id;

-- Thumbnail exists view
CREATE VIEW thumbnail_exists_view AS
  SELECT fp.path, f.id, 1 AS found
    FROM file_paths fp
         INNER JOIN files f
            ON fp.id = f.path_id
         INNER JOIN thumbnails t
            ON f.id = t.file_id;

-- Folder tile exists view
CREATE VIEW folder_tile_exists_view AS
  SELECT fp.path, fp.id, 1 AS found
    FROM folder_paths fp
         INNER JOIN folders f
            ON fp.id = f.path_id
         INNER JOIN thumbnails t
            ON f.tile_id = t.id;

-- Quality control views
CREATE VIEW qc_file_path_subset_file_name AS
SELECT f.filename, fp.path
  FROM file_paths fp
       INNER JOIN files f
               ON fp.id = f.path_id
 WHERE SUBSTRING(fp.path, -LENGTH(f.filename)) != f.filename;

CREATE VIEW qc_folder_path_subset_file_path AS
SELECT fop.path as folder_path, fp.path as file_path
  FROM file_paths fp
       INNER JOIN files f
               ON f.id = f.path_id
       INNER JOIN folders fo
               ON f.folder_id = fo.id
       INNER JOIN folder_paths fop
               ON fo.path_id = fop.id
 WHERE SUBSTRING(fp.path, 1, LENGTH(fop.path)) != fop.path;

-- Insert default configuration values
INSERT INTO config (key, value, type, category, requires_restart, description, default_value)
VALUES
    ('etag_version', strftime('%Y%m%d', 'now') || '-01', 'string', 'server', 0, 'Application-wide cache version for ETags and asset URLs', NULL)
ON CONFLICT(key) DO NOTHING;
