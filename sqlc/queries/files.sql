-- name: GetFileFolderIndexByID :one
SELECT image_index, image_count
  FROM file_folder_index
 WHERE file_id = ?;

-- name: GetLightboxNavByFileID :one
-- image_index is 1-based in the table; callers expect 0-based CurrentIndex.
SELECT image_index - 1 AS current_index
     , image_count
     , first_id
     , last_id
     , prev_id
     , next_id
  FROM file_folder_index
 WHERE file_id = ?;

-- name: UpsertFileReturningFile :one
INSERT INTO files (folder_id, path_id, filename, size_bytes, mtime, md5, phash, mime_type, width, height, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(path_id) 
    DO UPDATE SET filename   = excluded.filename
                , size_bytes = excluded.size_bytes
                , mtime      = excluded.mtime
                , md5        = excluded.md5
                , phash      = excluded.phash
                , mime_type  = excluded.mime_type
                , width      = excluded.width
                , height     = excluded.height
                , updated_at = excluded.updated_at
RETURNING *; 

-- name: GetFileByPath :one
SELECT f.* 
  FROM file_paths fp
       INNER JOIN files f  
          ON fp.id = f.path_id 
  WHERE fp.path = ?; 

-- name: GetFileViewByID :one
SELECT *
  FROM file_view
 WHERE id = ?;

-- name: GetGalleryFileThumbRowsByFolderID :many
SELECT fv.id AS id, fv.filename AS filename
  FROM file_view fv
 WHERE fv.folder_id = ?
 ORDER BY fv.filename;

-- name: GetFolderCount :one
SELECT COUNT(*) AS ct FROM folders;

-- name: GetFileCountAndTimestamps :one
SELECT COUNT(*)                                  AS ct_files
     , CAST(COALESCE(MIN(created_at), 0) AS INTEGER) AS min_created_at
     , CAST(COALESCE(MAX(updated_at), 0) AS INTEGER) AS max_updated_at
  FROM files;

-- name: GetFileSizeSum :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS sz_files
  FROM files;

