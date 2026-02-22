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

-- name: GetFileViewsByFolderIDOrderByFileName :many
SELECT *
  FROM file_view
 WHERE folder_id = ?
 ORDER BY filename;

-- name: GetGalleryStatistics :one
WITH fo AS (
    SELECT COUNT(*) AS ct_folders
      FROM folders
)
, fi AS (
    SELECT COUNT(*) AS ct_files
         , SUM(size_bytes) AS sz_files
         , MIN(created_at) AS min_created_at
         , MAX(updated_at) AS max_updated_at
      FROM files
)
SELECT (SELECT ct_folders FROM fo) AS ct_folders
     , *
  FROM fi;

