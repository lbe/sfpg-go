package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

// --- Test Connection Pools ---

// errConnPool is a connection pool that returns an error on Get().
type errConnPool struct {
	getErr error
}

func (p errConnPool) Get() (*dbconnpool.CpConn, error) { return nil, p.getErr }
func (p errConnPool) Put(*dbconnpool.CpConn)           {}
func (p errConnPool) Close() error                     { return nil }
func (p errConnPool) DB() *sql.DB                      { return nil }
func (p errConnPool) NumIdleConnections() int          { return 0 }
func (p errConnPool) NumConnections() int64            { return 0 }

// testConnPool is a simple successful connection pool for tests.
type testConnPool struct{}

func (p *testConnPool) Get() (*dbconnpool.CpConn, error) {
	return &dbconnpool.CpConn{Queries: nil}, nil
}

func (p *testConnPool) Put(*dbconnpool.CpConn)  {}
func (p *testConnPool) Close() error            { return nil }
func (p *testConnPool) DB() *sql.DB             { return nil }
func (p *testConnPool) NumIdleConnections() int { return 0 }
func (p *testConnPool) NumConnections() int64   { return 0 }

// --- Fake HandlerQueries ---

// fakeHandlerQueries is a fake implementation of interfaces.HandlerQueries for testing.
type fakeHandlerQueries struct {
	folderView           gallerydb.FolderView
	getFolderViewByIDErr error
	getSubFoldersErr     error
	getImagesErr         error
	folder               gallerydb.Folder
	getFolderByIDErr     error
	fileView             gallerydb.FileView
	getFileViewByIDErr   error
	thumbByFileErr       error
	thumbBlobErr         error
}

func (f *fakeHandlerQueries) GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error) {
	if f.getFolderViewByIDErr != nil {
		return gallerydb.FolderView{}, f.getFolderViewByIDErr
	}
	if f.folderView.ID != 0 {
		return f.folderView, nil
	}
	return gallerydb.FolderView{ID: id, Name: "Test", ParentID: sql.NullInt64{}}, nil
}

func (f *fakeHandlerQueries) GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error) {
	if f.getSubFoldersErr != nil {
		return nil, f.getSubFoldersErr
	}
	return []gallerydb.FolderView{}, nil
}

func (f *fakeHandlerQueries) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	if f.getImagesErr != nil {
		return nil, f.getImagesErr
	}
	return []gallerydb.FileView{{ID: 1, Path: "test.jpg", FolderID: folderID}}, nil
}

func (f *fakeHandlerQueries) GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error) {
	if f.getFileViewByIDErr != nil {
		return gallerydb.FileView{}, f.getFileViewByIDErr
	}
	if f.fileView.ID != 0 {
		return f.fileView, nil
	}
	return gallerydb.FileView{ID: id, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}}, nil
}

func (f *fakeHandlerQueries) GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error) {
	if f.getFolderByIDErr != nil {
		return gallerydb.Folder{}, f.getFolderByIDErr
	}
	if f.folder.ID != 0 {
		return f.folder, nil
	}
	return gallerydb.Folder{ID: id, TileID: sql.NullInt64{}}, nil
}

func (f *fakeHandlerQueries) GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error) {
	if f.thumbByFileErr != nil {
		return gallerydb.Thumbnail{}, f.thumbByFileErr
	}
	return gallerydb.Thumbnail{ID: 10}, nil
}

func (f *fakeHandlerQueries) GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error) {
	if f.thumbBlobErr != nil {
		return nil, f.thumbBlobErr
	}
	return []byte("thumb"), nil
}

func (f *fakeHandlerQueries) GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) (*sql.Rows, error) {
	return nil, nil
}

func (f *fakeHandlerQueries) GetGalleryStatistics(ctx context.Context) (gallerydb.GetGalleryStatisticsRow, error) {
	return gallerydb.GetGalleryStatisticsRow{
		CtFolders:    0,
		CtFiles:      0,
		SzFiles:      sql.NullFloat64{},
		MinCreatedAt: nil,
		MaxUpdatedAt: nil,
	}, nil
}

// --- Lightbox Test Helpers ---

// lightboxEmptyList is a fakeHandlerQueries that returns an empty image list.
type lightboxEmptyList struct {
	fakeHandlerQueries
}

func (l lightboxEmptyList) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return []gallerydb.FileView{}, nil
}

// lightboxList is a fakeHandlerQueries that returns a specific image list.
type lightboxList struct {
	fakeHandlerQueries
	images []gallerydb.FileView
}

func (l lightboxList) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return l.images, nil
}

// --- Metadata Queries Mocks ---

// mockMetadataQueries returns sql.ErrNoRows for all metadata queries.
type mockMetadataQueries struct{}

func (mockMetadataQueries) GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error) {
	return gallerydb.ExifMetadatum{}, sql.ErrNoRows
}

func (mockMetadataQueries) GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error) {
	return gallerydb.IptcMetadatum{}, sql.ErrNoRows
}

// mockMetadataQueriesWithError returns specific errors for metadata queries.
type mockMetadataQueriesWithError struct {
	exifErr error
	iptcErr error
}

func (m mockMetadataQueriesWithError) GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error) {
	if m.exifErr != nil {
		return gallerydb.ExifMetadatum{}, m.exifErr
	}
	return gallerydb.ExifMetadatum{}, sql.ErrNoRows
}

func (m mockMetadataQueriesWithError) GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error) {
	if m.iptcErr != nil {
		return gallerydb.IptcMetadatum{}, m.iptcErr
	}
	return gallerydb.IptcMetadatum{}, sql.ErrNoRows
}

// --- Preload Service Mock ---

// mockPreloadService is a mock implementation of PreloadService for testing.
type mockPreloadService struct {
	called chan struct{}
	lastID int64
}

func (m *mockPreloadService) ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
	m.lastID = folderID
	select {
	case m.called <- struct{}{}:
	default:
	}
}

func (m *mockPreloadService) SetEnabled(enabled bool) {}

func (m *mockPreloadService) IsEnabled() bool { return true }

// --- Test Setup Helpers ---

// setupTestGalleryHandlers creates a GalleryHandlers instance for testing.
func setupTestGalleryHandlers(t *testing.T, hq interfaces.HandlerQueries) *GalleryHandlers {
	t.Helper()

	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	imagesDir := t.TempDir()
	gh := NewGalleryHandlers(
		errConnPool{getErr: errors.New("no db")},
		context.Background(),
		imagesDir,
		func() string { return imagesDir },
		func(*dbconnpool.CpConn) interfaces.HandlerQueries { return hq },
		func(*dbconnpool.CpConn) MetadataQueries { return mockMetadataQueries{} },
		func() string { return "test-etag" },
		func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
			if data == nil {
				data = make(map[string]any)
			}
			data["CSRFToken"] = "test-csrf-token"
			return data
		},
		func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		},
	)

	gh.DBRoPool = &testConnPool{}

	return gh
}
