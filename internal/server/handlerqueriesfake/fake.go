// Package handlerqueriesfake provides a shared fake implementation of
// interfaces.HandlerQueries for handler and cachepreload tests.
package handlerqueriesfake

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// Fake implements interfaces.HandlerQueries with configurable responses.
type Fake struct {
	FolderView                gallerydb.FolderView
	GetFolderViewByIDErr      error
	Folder                    gallerydb.Folder
	GetFolderByIDErr          error
	FileView                  gallerydb.FileView
	GetFileViewByIDErr        error
	ThumbByFileErr            error
	ThumbBlobErr              error
	GetFileIndexErr           error
	GetNavErr                 error
	GetFolderInfoCountsErr    error
	FolderInfoCounts          gallerydb.GetFolderInfoCountsByIDRow
	GetGalleryFileThumbsErr   error
	GetGalleryFolderThumbsErr error
	PreloadRoutes             []string
	GetPreloadRoutesErr       error
}

func (f *Fake) GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error) {
	if f.GetFolderViewByIDErr != nil {
		return gallerydb.FolderView{}, f.GetFolderViewByIDErr
	}
	if f.FolderView.ID != 0 {
		return f.FolderView, nil
	}
	return gallerydb.FolderView{ID: id, Name: "Test", ParentID: sql.NullInt64{}}, nil
}

func (f *Fake) GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error) {
	if f.GetFileViewByIDErr != nil {
		return gallerydb.FileView{}, f.GetFileViewByIDErr
	}
	if f.FileView.ID != 0 {
		return f.FileView, nil
	}
	return gallerydb.FileView{ID: id, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}}, nil
}

func (f *Fake) GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error) {
	if f.GetFolderByIDErr != nil {
		return gallerydb.Folder{}, f.GetFolderByIDErr
	}
	if f.Folder.ID != 0 {
		return f.Folder, nil
	}
	return gallerydb.Folder{ID: id, TileID: sql.NullInt64{}}, nil
}

func (f *Fake) GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error) {
	if f.ThumbByFileErr != nil {
		return gallerydb.Thumbnail{}, f.ThumbByFileErr
	}
	return gallerydb.Thumbnail{ID: 10}, nil
}

func (f *Fake) GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error) {
	if f.ThumbBlobErr != nil {
		return nil, f.ThumbBlobErr
	}
	return []byte("thumb"), nil
}

func (f *Fake) GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) ([]string, error) {
	if f.GetPreloadRoutesErr != nil {
		return nil, f.GetPreloadRoutesErr
	}
	return f.PreloadRoutes, nil
}

func (f *Fake) GetFileFolderIndexByID(ctx context.Context, id int64) (gallerydb.GetFileFolderIndexByIDRow, error) {
	if f.GetFileIndexErr != nil {
		return gallerydb.GetFileFolderIndexByIDRow{}, f.GetFileIndexErr
	}
	return gallerydb.GetFileFolderIndexByIDRow{ImageIndex: 1, ImageCount: 1}, nil
}

func (f *Fake) GetLightboxNavByFileID(ctx context.Context, id int64) (gallerydb.GetLightboxNavByFileIDRow, error) {
	if f.GetNavErr != nil {
		return gallerydb.GetLightboxNavByFileIDRow{}, f.GetNavErr
	}
	return gallerydb.GetLightboxNavByFileIDRow{
		CurrentIndex: 0, ImageCount: 1,
		FirstID: 1, LastID: 1,
		PrevID: sql.NullInt64{}, NextID: sql.NullInt64{},
	}, nil
}

func (f *Fake) GetFolderInfoCountsByID(ctx context.Context, id int64) (gallerydb.GetFolderInfoCountsByIDRow, error) {
	if f.GetFolderInfoCountsErr != nil {
		return gallerydb.GetFolderInfoCountsByIDRow{}, f.GetFolderInfoCountsErr
	}
	return f.FolderInfoCounts, nil
}

func (f *Fake) GetGalleryFileThumbRowsByFolderID(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.GetGalleryFileThumbRowsByFolderIDRow, error) {
	if f.GetGalleryFileThumbsErr != nil {
		return nil, f.GetGalleryFileThumbsErr
	}
	return []gallerydb.GetGalleryFileThumbRowsByFolderIDRow{{ID: 1, Filename: "test.jpg"}}, nil
}

func (f *Fake) GetGalleryFolderThumbRowsByParentID(ctx context.Context, parentID sql.NullInt64) ([]gallerydb.GetGalleryFolderThumbRowsByParentIDRow, error) {
	if f.GetGalleryFolderThumbsErr != nil {
		return nil, f.GetGalleryFolderThumbsErr
	}
	return []gallerydb.GetGalleryFolderThumbRowsByParentIDRow{}, nil
}

// PreloadRoutesForChildren builds gallery/info/lightbox preload routes for mock children.
func PreloadRoutesForChildren(subfolders []gallerydb.FolderView, images []gallerydb.FileView) []string {
	var routes []string
	for _, sf := range subfolders {
		routes = append(routes,
			fmt.Sprintf("/gallery/%d", sf.ID),
			fmt.Sprintf("/info/folder/%d", sf.ID),
		)
	}
	for _, img := range images {
		routes = append(routes,
			fmt.Sprintf("/info/image/%d", img.ID),
			fmt.Sprintf("/lightbox/%d", img.ID),
		)
	}
	return routes
}
