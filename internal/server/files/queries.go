package files

import (
	"bytes"
	"context"
	"database/sql"

	"go.local/sfpg/internal/gallerydb"
)

// File represents a file being processed, including its metadata and status.
type File struct {
	Ok                  bool
	Exists              bool
	ImagesDir           string
	Path                string
	File                gallerydb.File
	Thumbnail           *bytes.Buffer
	Exif                gallerydb.UpsertExifParams
	Itpc                gallerydb.UpsertIPTCParams
	XmpProp             gallerydb.UpsertXMPPropertyParams
	XmpRaw              gallerydb.UpsertXMPRawParams
	HasValidJpegMarkers bool // Set by DetectMimeType for JPEG files
}

// QueriesForFiles is an interface containing the database methods needed by
// the file processing logic. This abstraction allows for test fakes.
type QueriesForFiles interface {
	GetFileByPath(ctx context.Context, path string) (gallerydb.File, error)
	GetInvalidFileByPath(ctx context.Context, path string) (gallerydb.InvalidFile, error)
	UpsertExif(ctx context.Context, p gallerydb.UpsertExifParams) error
	GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error)
	GetFolderTileExistsViewByPath(ctx context.Context, path string) (bool, error)
	WithTx(tx *sql.Tx) ThumbnailTx
}

// ThumbnailTx contains the minimal transaction-scoped methods used by
// the thumbnail upsert logic. This interface is used within database transactions
// to perform thumbnail-related operations atomically.
type ThumbnailTx interface {
	UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error)
	UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error
}

// Importer is the minimal interface used by application code that needs to interact
// with the gallery importer. Defining it here allows tests to provide lightweight
// fakes without depending on the concrete type in internal/gallerylib.
type Importer interface {
	UpsertPathChain(ctx context.Context, path string, mtime, size int64, md5 string, phash, width, height int64, mimeType string) (gallerydb.File, error)
	UpdateFolderTileChain(ctx context.Context, folderID, tileFileID int64) error
}
