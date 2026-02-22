/*
-- name: GetPreloadRoutesByFolderID :many
SELECT '/gallery/' || fv.id AS route FROM folder_view fv WHERE fv.parent_id = ?
UNION ALL
SELECT '/info/folder/' || fv.id AS route FROM folder_view fv WHERE fv.parent_id = ?
UNION ALL
SELECT '/info/image/' || fiv.id AS route FROM file_view fiv WHERE fiv.folder_id = ?
UNION ALL
SELECT '/lightbox/' || fiv.id AS route FROM file_view fiv WHERE fiv.folder_id = ?;
*/

/*
-- name: GetPreloadRoutesByFolderID :many
WITH cte_cache_map AS (
	SELECT 'folder' AS cache_type, '/gallery/' AS cache_entry
	UNION ALL 
	SELECT 'folder' AS cache_type, '/info/folder/' AS cache_entry
	UNION ALL 
	SELECT 'file' AS cache_type, '/info/image/' AS cache_entry
	UNION ALL 
	SELECT 'file' AS cache_type, '/lightbox/' AS cache_entry
)
, cte_folders as (
	SELECT CASE WHEN f.parent_id IS NULL THEN 1 ELSE f.parent_id END AS parent_id, f.id AS folder_id, b.cache_type, f.id, b.cache_entry || f.id as route 
	  FROM folders f 
	       INNER JOIN folders a 
	               ON f.parent_id = a.id 
	       INNER JOIN cte_cache_map b 
	               ON b.cache_type = 'folder'
	UNION ALL 
	SELECT 1 AS parent_id, f.id AS folder_id, b.cache_type, f.id, b.cache_entry || f.id as route 
	  FROM folders f 
	       CROSS JOIN cte_cache_map b 
	               ON b.cache_type = 'folder'
	 WHERE f.parent_id is null 
)
--select * from cte_folders where parent_id = 1 order by 1,2,4;
, cte_files AS (
    SELECT CASE WHEN a.parent_id IS NULL THEN 1 ELSE a.parent_id END AS parent_id, f.folder_id, b.cache_type, f.id, b.cache_entry || f.id as route
	  FROM files f 
	       INNER JOIN folders a 
	               ON f.folder_id = a.id 
	       INNER JOIN cte_cache_map b 
	               ON b.cache_type = 'file'
)
, u AS (
	SELECT cte_folders.parent_id AS pid, cte_folders.folder_id AS fid, route
	  FROM cte_folders
	UNION ALL
	SELECT cte_files.parent_id AS pid, cte_files.folder_id AS fid, route
	  FROM cte_files
)
SELECT route 
  FROM u 
 WHERE ? IN (u.pid, u.fid); 
*/