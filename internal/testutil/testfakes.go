// Package testutil provides helper functions and fake implementations of interfaces
// used throughout the application, primarily for facilitating isolated unit testing.
package testutil

import (
	"context"
	"database/sql"

	"go.local/sfpg/internal/gallerydb"
)

// FakeQueries is a minimal fake implementing the QueriesForFiles contract for tests.
type FakeQueries struct{}

func (f *FakeQueries) GetFileByPath(ctx context.Context, path string) (gallerydb.File, error) {
	return gallerydb.File{}, sql.ErrNoRows
}
func (f *FakeQueries) UpsertExif(ctx context.Context, p gallerydb.UpsertExifParams) error {
	return nil
}
func (f *FakeQueries) GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *FakeQueries) GetFolderTileExistsViewByPath(ctx context.Context, path string) (bool, error) {
	return false, sql.ErrNoRows
}

// ThumbnailTx is a local, narrow interface defining the transaction-scoped methods
// required for thumbnail operations. It is redefined here to avoid a direct
// dependency on internal interfaces from other packages in test fakes.
type ThumbnailTx interface {
	UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error)
	UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error
}

func (f *FakeQueries) WithTx(tx *sql.Tx) ThumbnailTx { return nil }

// FakeImporter is a fake implementation of the Importer interface.
type FakeImporter struct {
	UpsertPathChainFunc       func(ctx context.Context, path string, mtime, size int64, md5 string, phash, width, height int64, mimeType string) (gallerydb.File, error)
	UpdateFolderTileChainFunc func(ctx context.Context, folderID, tileFileID int64) error
}

func (f *FakeImporter) UpsertPathChain(ctx context.Context, path string, mtime, size int64, md5 string, phash, width, height int64, mimeType string) (gallerydb.File, error) {
	if f.UpsertPathChainFunc != nil {
		return f.UpsertPathChainFunc(ctx, path, mtime, size, md5, phash, width, height, mimeType)
	}
	return gallerydb.File{}, nil
}

func (f *FakeImporter) UpdateFolderTileChain(ctx context.Context, folderID, tileFileID int64) error {
	if f.UpdateFolderTileChainFunc != nil {
		return f.UpdateFolderTileChainFunc(ctx, folderID, tileFileID)
	}
	return nil
}

// FakeQueriesForCheck is a fake implementation of the QueriesForFiles interface
// specifically designed to allow controlled returns for the GetFileByPath method.
type FakeQueriesForCheck struct {
	Ret gallerydb.File
	Err error
}

func (f *FakeQueriesForCheck) GetFileByPath(ctx context.Context, path string) (gallerydb.File, error) {
	return f.Ret, f.Err
}
func (f *FakeQueriesForCheck) UpsertExif(ctx context.Context, p gallerydb.UpsertExifParams) error {
	return nil
}
func (f *FakeQueriesForCheck) GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *FakeQueriesForCheck) GetFolderTileExistsViewByPath(ctx context.Context, path string) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *FakeQueriesForCheck) WithTx(tx *sql.Tx) ThumbnailTx { return nil }

// FakeThumbnailTxSuccess is a fake implementation of ThumbnailTx that simulates
// a successful transaction, recording whether its methods were called.
type FakeThumbnailTxSuccess struct {
	CalledReturnID bool
	CalledBlob     bool
	ReturnID       int64
}

func (f *FakeThumbnailTxSuccess) UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error) {
	f.CalledReturnID = true
	return f.ReturnID, nil
}
func (f *FakeThumbnailTxSuccess) UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error {
	f.CalledBlob = true
	return nil
}

// FakeThumbnailTxFailBlob is a fake implementation of ThumbnailTx that simulates
// a transaction where the UpsertThumbnailBlob operation fails.
type FakeThumbnailTxFailBlob struct{}

func (f *FakeThumbnailTxFailBlob) UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error) {
	return 11, nil
}
func (f *FakeThumbnailTxFailBlob) UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error {
	return sql.ErrConnDone
}
