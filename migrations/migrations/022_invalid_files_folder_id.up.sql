-- Migration: add invalid_files.folder_id (NOT NULL, FK to folders, indexed) so discovery
-- can load the per-directory catalog with folder-scoped queries.
-- Orphan policy: invalid_files.path that cannot resolve to folder_id are stale (wrong path
-- shape, removed tree, legacy NAS prefixes). DELETE those rows; do not abort migration.

-- Resolve folder_id once into a staging table: exact file_paths match first, otherwise the
-- parent gallery_dir (same rules as catalog_paths in files.sql). Both lookups are equality
-- matches against the UNIQUE path indexes, so each row costs two seeks rather than a scan
-- of folder_paths. rtrim(path, replace(path, '/', '')) strips the final component, leaving
-- the trailing separator; the substr removes it. Paths with no separator resolve to ''.
CREATE TEMP TABLE _022_resolved AS
SELECT inv.path       AS path
     , inv.mtime      AS mtime
     , inv.size       AS size
     , inv.reason     AS reason
     , inv.created_at AS created_at
     , inv.updated_at AS updated_at
     , COALESCE(
         (SELECT f.folder_id
            FROM file_paths AS p
                 INNER JOIN files AS f ON f.path_id = p.id
           WHERE p.path = inv.path),
         (SELECT fo.id
            FROM folder_paths AS fp
                 INNER JOIN folders AS fo ON fo.path_id = fp.id
           WHERE fp.path = CASE
                   WHEN instr(inv.path, '/') = 0 THEN ''
                   ELSE substr(
                          rtrim(inv.path, replace(inv.path, '/', '')),
                          1,
                          length(rtrim(inv.path, replace(inv.path, '/', ''))) - 1
                        )
                 END)
       ) AS folder_id
  FROM invalid_files AS inv;

DELETE FROM invalid_files
 WHERE path IN (SELECT path FROM _022_resolved WHERE folder_id IS NULL);

CREATE TABLE invalid_files_new (
    path TEXT NOT NULL PRIMARY KEY,
    mtime INTEGER NOT NULL,
    size INTEGER NOT NULL,
    reason TEXT,
    folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now')),
    updated_at INTEGER NOT NULL DEFAULT (UNIXEPOCH('now'))
);

INSERT INTO invalid_files_new (path, mtime, size, reason, folder_id, created_at, updated_at)
  SELECT path, mtime, size, reason, folder_id, created_at, updated_at
    FROM _022_resolved
   WHERE folder_id IS NOT NULL;

DROP TABLE _022_resolved;

DROP TABLE invalid_files;

ALTER TABLE invalid_files_new RENAME TO invalid_files;

CREATE INDEX idx_invalid_files_folder_id ON invalid_files(folder_id);
