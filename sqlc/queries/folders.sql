-- name: UpsertFolderReturningFolder :one
INSERT INTO folders (parent_id, path_id, name, mtime, tile_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (path_id) 
    DO UPDATE 
          SET name = EXCLUDED.name
            , parent_id = EXCLUDED.parent_id
            , mtime = EXCLUDED.mtime
            , tile_id = EXCLUDED.tile_id
            , updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: UpdateFolderTileId :exec
UPDATE folders 
   SET tile_id = ?
 WHERE id = ?;

-- -- name: PopulateMissingTileID :exec  
-- UPDATE folders 
--    SET tile_id = (
--      SELECT tile_id 
--        FROM (
--          SELECT f.id, p.id AS tile_id  
--               , DENSE_RANK() OVER (PARTITION BY f.id ORDER BY p.path) AS rnk 
--            FROM files f 
--                 INNER JOIN file_paths p 
--                   ON f.path_id = p.id
--           WHERE f.tile_id IS NULL
--        ) ranked
--       WHERE ranked.id = folders.id 
--         AND ranked.rnk = 1
--    )
--  WHERE EXISTS (
--      SELECT 1 FROM files f 
--      WHERE f.tile_id IS NULL AND folders.id = f.id
--    );

-- name: GetFolderByID :one
SELECT id, parent_id, path_id, name, mtime, tile_id, created_at, updated_at
  FROM folders
 WHERE id = ?;

-- name: GetFolderIDByPath :one
SELECT id 
  FROM folders
 WHERE path_id = (SELECT id FROM folder_paths WHERE path = ?);

-- name: GetFolderByPath :one
SELECT f.id, f.parent_id, f.path_id, f.name, f.mtime, f.tile_id, f.created_at, f.updated_at
  FROM folders f 
       INNER JOIN folder_paths p 
          ON f.path_id = p.id
 WHERE p.path = ?;

-- name: GetFolderViewByID :one
SELECT * 
  FROM folder_view
 WHERE id = ?;

-- name: GetFoldersViewsByParentIDOrderByName :many
SELECT * 
  FROM folder_view
 WHERE parent_id = ?
 ORDER BY name;

-- name: GetFolderTileExistsViewByPath :one 
SELECT found 
  FROM folder_tile_exists_view
 WHERE path = ?;

