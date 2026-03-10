-- name: UpsertThumbnailReturningID :one
INSERT INTO thumbnails (file_id, size_label, width, height, format, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) 
    DO UPDATE 
          SET file_id    = excluded.file_id
            , size_label = excluded.size_label
            , width      = excluded.width
            , height     = excluded.height
            , format     = excluded.format
            , updated_at = excluded.updated_at
        WHERE file_id    IS NOT excluded.file_id
           OR size_label IS NOT excluded.size_label
           OR width      IS NOT excluded.width
           OR height     IS NOT excluded.height
           OR format     IS NOT excluded.format
RETURNING id; 

-- name: GetThumbnailsByFileID :one
SELECT * 
  FROM thumbnails 
 WHERE file_id = ?;

-- name: GetThumbnailExistsViewByID :one 
SELECT found 
  FROM thumbnail_exists_view
 WHERE id = ?;
