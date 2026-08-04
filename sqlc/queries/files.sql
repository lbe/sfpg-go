-- name: GetFileFolderIndexByID :one
WITH target AS (
  SELECT fv.folder_id AS folder_id, fv.id AS id
    FROM file_view fv
   WHERE fv.id = ?
),
ordered AS (
  SELECT fv.id AS id,
         CAST(ROW_NUMBER() OVER (ORDER BY fv.filename, fv.id) AS INTEGER) AS image_index,
         CAST(COUNT(*) OVER () AS INTEGER) AS image_count
    FROM file_view fv
         INNER JOIN target t ON fv.folder_id = t.folder_id
)
SELECT o.image_index, o.image_count
  FROM ordered o
       INNER JOIN target t ON o.id = t.id;

-- name: GetLightboxNavByFileID :one
WITH target AS (
  SELECT fv.folder_id AS folder_id, fv.id AS id
    FROM file_view fv
   WHERE fv.id = ?
),
ordered AS (
  SELECT fv.id AS id,
         CAST(ROW_NUMBER() OVER (ORDER BY fv.filename, fv.id) - 1 AS INTEGER) AS current_index,
         CAST(COUNT(*) OVER () AS INTEGER) AS image_count,
         CAST(LAG(fv.id) OVER (ORDER BY fv.filename, fv.id) AS INTEGER) AS prev_id,
         CAST(LEAD(fv.id) OVER (ORDER BY fv.filename, fv.id) AS INTEGER) AS next_id,
         CAST(FIRST_VALUE(fv.id) OVER (ORDER BY fv.filename, fv.id) AS INTEGER) AS first_id,
         CAST(LAST_VALUE(fv.id) OVER (
           ORDER BY fv.filename, fv.id
           ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
         ) AS INTEGER) AS last_id
    FROM file_view fv
         INNER JOIN target t ON fv.folder_id = t.folder_id
)
SELECT o.current_index, o.image_count, o.first_id, o.last_id, o.prev_id, o.next_id
  FROM ordered o
       INNER JOIN target t ON o.id = t.id;

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

-- name: GetFileViewsByFolderIDOrderByFileName :many
SELECT *
  FROM file_view
 WHERE folder_id = ?
 ORDER BY filename;

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

