-- name: GetInvalidFileByPath :one
SELECT * FROM invalid_files WHERE path = ?;

-- name: UpsertInvalidFile :exec
INSERT INTO invalid_files (path, mtime, size, reason, updated_at)
VALUES (?, ?, ?, ?, UNIXEPOCH('now'))
ON CONFLICT(path) DO UPDATE SET
    mtime = excluded.mtime,
    size = excluded.size,
    reason = excluded.reason,
    updated_at = UNIXEPOCH('now');

-- name: DeleteInvalidFileByPath :exec
DELETE FROM invalid_files WHERE path = ?;

