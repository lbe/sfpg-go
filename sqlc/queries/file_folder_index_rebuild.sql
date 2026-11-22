-- name: QueryFilesForFolderIndexRebuild :many
-- STREAMING: consumed via QueryContext/*sql.Rows in chunks. Each chunk resumes after the last complete folder (folder_id > ?). Do not sqlc generate (:many would OOM).
SELECT id, folder_id
  FROM files
 WHERE folder_id IS NOT NULL
   AND folder_id > ?
 ORDER BY folder_id, filename, id
-- statement-break
-- name: InsertFileFolderIndexNew :exec
-- Prepared on the writebatcher transaction, not at pool Prepare (dest table is runtime CloneEmpty).
INSERT INTO file_folder_index_new
  (file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
