-- Rollback: drop folder_id index and column (nullable only; no FK on 022).

DROP INDEX IF EXISTS idx_invalid_files_folder_id;

CREATE TABLE invalid_files_new (
    path TEXT NOT NULL PRIMARY KEY,
    mtime INTEGER NOT NULL,
    size INTEGER NOT NULL,
    reason TEXT,
    created_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now')),
    updated_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now'))
);

INSERT INTO invalid_files_new (path, mtime, size, reason, created_at, updated_at)
  SELECT path, mtime, size, reason, created_at, updated_at FROM invalid_files;

DROP TABLE invalid_files;

ALTER TABLE invalid_files_new RENAME TO invalid_files;
