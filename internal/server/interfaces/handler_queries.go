// Package interfaces holds shared contracts consumed by both the server
// orchestrator (App) and the handlers package. These interfaces live here
// to avoid circular dependencies while keeping the contracts in a neutral,
// stable location.
package interfaces

import (
	"context"
	"database/sql"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// HandlerQueries abstracts the subset of DB queries used by HTTP handlers.
// Shared by server and handlers packages.
type HandlerQueries interface {
	GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error)
	GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error)
	GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error)
	GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error)
	GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error)
	GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error)
	GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error)
	// GetPreloadRoutesByFolderID returns routes to preload for a folder (source of truth: direct children only).
	// Returns *sql.Rows; each row contains a route string to scan.
	GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) (*sql.Rows, error)
}
