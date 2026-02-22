-- name: UpsertFolderPathReturningID :one
INSERT INTO folder_paths (path)
VALUES (?)
ON CONFLICT (path) DO UPDATE SET path=excluded.path -- This ensures an ID is returned on conflict
RETURNING id;

