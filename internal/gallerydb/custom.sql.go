// Package gallerydb provides the database access layer for the application.
// It uses sqlc to generate type-safe Go code from SQL queries. This package
// includes the generated code, custom queries, and data models.
package gallerydb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// CustomQueries embeds the sqlc-generated Queries struct and adds methods
// for custom, hand-written queries.
type CustomQueries struct {
	*Queries
	getFileViewRowsByFolderPathStmt          *sql.Stmt
	getFolderViewThumbnailBlobDataByPathStmt *sql.Stmt
	getFileViewRowsByFolderIDStmt            *sql.Stmt
	getPreloadRoutesByFolderIDStmt           *sql.Stmt
}

// NewCustomQueries creates a new CustomQueries instance.
func NewCustomQueries(db DBTX) *CustomQueries {
	return &CustomQueries{
		Queries: New(db),
	}
}

var ct_prepares atomic.Int64

// PrepareCustomQueries prepares both the sqlc-generated statements and any custom
// statements, returning a fully initialized CustomQueries object.
func PrepareCustomQueries(ctx context.Context, db DBTX) (*CustomQueries, error) {
	cq := &CustomQueries{
		Queries: &Queries{db: db},
	}

	// Prepare sqlc's statements
	queries, err := Prepare(ctx, db)
	if err != nil {
		return nil, err
	}
	cq.Queries = queries

	// Prepare your custom statement
	if cq.getFileViewRowsByFolderPathStmt, err = db.PrepareContext(ctx, getFileViewRowsByFolderPath); err != nil {
		return nil, fmt.Errorf("error preparing query GetFileViewsByFolderPath: %w", err)
	}
	if cq.getFolderViewThumbnailBlobDataByPathStmt, err = db.PrepareContext(ctx, getFolderViewThumbnailBlobDataByPath); err != nil {
		return nil, fmt.Errorf("error preparing query GetFileViewsByFolderPath: %w", err)
	}

	ct_prepares.Add(1)
	slog.Debug("Prepared CustomQueries", "total_prepares", ct_prepares.Load())
	return cq, nil
}

const getFileViewRowsByFolderID = `-- name: GetFileViewsByFolderID :many
SELECT id, folder_id, folder_path, path, filename, size_bytes, md5, mime_type
     , width, height, created_at, updated_at 
  FROM file_view
 WHERE folder_id = ?
 ORDER BY filename`

func (cq *CustomQueries) GetFileViewRowsByFolderID(ctx context.Context, folderID int64) (*sql.Rows, error) {
	return cq.query(ctx, cq.getFileViewRowsByFolderIDStmt, getFileViewRowsByFolderID, folderID)
}

const getFileViewRowsByFolderPath = `-- name: GetFileViewsByFolderPath :many
SELECT id, folder_id, folder_path, path, filename, size_bytes, md5, mime_type
     , width, height, created_at, updated_at 
  FROM file_view
 WHERE folder_path = ?
 ORDER BY filename`

func (cq *CustomQueries) GetFileViewRowsByFolderPath(ctx context.Context, folderPath string) (*sql.Rows, error) {
	return cq.query(ctx, cq.getFileViewRowsByFolderPathStmt, getFileViewRowsByFolderPath, folderPath)
}

const getFolderViewThumbnailBlobDataByPath = `-- name: GetFolderViewThumbnailBlobDataByPath :one 
SELECT tb.data
  FROM folder_paths fp
       INNER JOIN folders f
	           ON fp.id = f.path_id
	   INNER JOIN thumbnail_blobs tb 
	           ON f.tile_id = tb.thumbnail_id
 WHERE fp.path = ?;`

// GetFolderViewThumbnailBlobDataByPath retrieves the thumbnail blob for a folder's
// tile image directly via the folder path.
func (q *CustomQueries) GetFolderViewThumbnailBlobDataByPath(ctx context.Context, path string) ([]byte, error) {
	var data []byte
	err := q.db.QueryRowContext(ctx, getFolderViewThumbnailBlobDataByPath, path).Scan(&data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

const getPreloadRoutesByFolderID = `-- name: GetPreloadRoutesByFolderID :many
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
	SELECT CASE WHEN f.parent_id IS NULL THEN 1 ELSE f.parent_id END AS parent_id, f.id AS folder_id, /*b.cache_type, f.id,*/ b.cache_entry || f.id as route 
	  FROM folders f 
	       INNER JOIN folders a 
	               ON f.parent_id = a.id 
	       INNER JOIN cte_cache_map b 
	               ON b.cache_type = 'folder'
	UNION ALL 
	SELECT 1 AS parent_id, f.id AS folder_id, /*b.cache_type, f.id,*/ b.cache_entry || f.id as route 
	  FROM folders f 
	       CROSS JOIN cte_cache_map b 
	               ON b.cache_type = 'folder'
	 WHERE f.parent_id is null 
)
--select * from cte_folders where parent_id = 1 order by 1,2,4;
, cte_files AS (
    SELECT CASE WHEN a.parent_id IS NULL THEN 1 ELSE a.parent_id END AS parent_id, f.folder_id, /*b.cache_type, f.id,*/ b.cache_entry || f.id as route
	  FROM files f 
	       INNER JOIN folders a 
	               ON f.folder_id = a.id 
	       INNER JOIN cte_cache_map b 
	               ON b.cache_type = 'file'
)
, u AS (
	SELECT *
	  FROM cte_folders
	UNION ALL
	SELECT *
	  FROM cte_files
)
SELECT route 
  FROM u 
 WHERE ? IN (parent_id, folder_id);`

// GetPreloadRoutesByFolderID retrieves all preload routes for subfolders and files
// under the specified folder ID.
func (q *CustomQueries) GetPreloadRoutesByFolderID(ctx context.Context, parent_id sql.NullInt64) (*sql.Rows, error) {
	return q.db.QueryContext(ctx, getPreloadRoutesByFolderID, parent_id)
}

// WithTx returns a new CustomQueries instance with the provided transaction, allowing
// custom queries to be executed within a transaction.
func (q *CustomQueries) WithTx(tx *sql.Tx) *CustomQueries {
	return &CustomQueries{
		Queries:                                  q.Queries.WithTx(tx), // Use sqlc's WithTx
		getFileViewRowsByFolderPathStmt:          q.getFileViewRowsByFolderPathStmt,
		getFolderViewThumbnailBlobDataByPathStmt: q.getFolderViewThumbnailBlobDataByPathStmt,
		getPreloadRoutesByFolderIDStmt:           q.getPreloadRoutesByFolderIDStmt,
	}
}
