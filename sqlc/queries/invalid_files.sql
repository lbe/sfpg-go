-- name: GetInvalidFileByPath :one
SELECT * FROM invalid_files WHERE path = ?;

-- name: ListDiscoveryInvalidByFolderID :many
SELECT inv.path   AS invalid_path
     , inv.mtime  AS invalid_mtime
     , inv.size   AS invalid_size
     , inv.reason AS invalid_reason
  FROM invalid_files AS inv
 WHERE inv.folder_id = ?;

-- name: UpsertInvalidFile :exec
INSERT INTO invalid_files (path, mtime, size, reason, folder_id, updated_at)
VALUES (?, ?, ?, ?, ?, UNIXEPOCH('now'))
ON CONFLICT(path) DO UPDATE SET
    mtime = excluded.mtime,
    size = excluded.size,
    reason = excluded.reason,
    folder_id = excluded.folder_id,
    updated_at = UNIXEPOCH('now');

-- name: DeleteInvalidFileByPath :exec
DELETE FROM invalid_files WHERE path = ?;

