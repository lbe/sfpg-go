-- name: QueryFilesForFolderIndexRebuild :many
-- STREAMING: consumed via QueryContext/*sql.Rows. Do not sqlc generate (:many would OOM).
SELECT id, folder_id
  FROM files
 WHERE folder_id IS NOT NULL
 ORDER BY folder_id, filename, id
-- statement-break
-- name: InsertFileFolderIndexNew :exec
-- Prepared on the writebatcher transaction, not at pool Prepare (dest table is runtime CloneEmpty).
INSERT INTO file_folder_index_new
  (file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
-- statement-break
-- name: CountFilesForFolderIndexRebuild :one
-- Same WHERE as QueryFilesForFolderIndexRebuild. Pool-prepared (files exists at init). Embed-only; not sqlc generate.
SELECT COUNT(*)
  FROM files
 WHERE folder_id IS NOT NULL
