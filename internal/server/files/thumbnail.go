package files

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/gallerylib"
	"go.local/sfpg/internal/thumbnail"
)

// UpsertThumbnail inserts or updates a thumbnail record and its blob data in a single transaction.
func UpsertThumbnail(ctx context.Context, cpcRw *dbconnpool.CpConn, fileID int64, thumb []byte) (int64, error) {
	var thumbnailID int64
	tx, err := cpcRw.Conn.BeginTx(ctx, nil)
	if err != nil {
		return thumbnailID, err
	}
	defer func() {
		err = tx.Rollback()
		if err != nil && err != sql.ErrTxDone {
			slog.Error("upsertThumbnail: transaction rollback failed", "err", err)
		}
	}()
	qtx := cpcRw.Queries.WithTx(tx)

	// Delegate the tx-scoped upsert logic to a helper so tests can exercise
	// the behavior using a fake ThumbnailTx implementation without touching
	// real transactions.
	thumbnailID, err = UpsertThumbnailTxOnly(qtx, ctx, fileID, thumb)
	if err != nil {
		return thumbnailID, err
	}

	err = tx.Commit()
	if err != nil {
		return thumbnailID, err
	}
	return thumbnailID, nil
}

// UpsertThumbnailTxOnly performs the tx-scoped thumbnail upsert operations
// using the provided ThumbnailTx. This helper does not manage transactions
// (Begin/Commit/Rollback); callers are responsible for that. Factoring this
// out makes it trivial to test the logic with a fake ThumbnailTx.
var UpsertThumbnailTxOnly = func(qtx ThumbnailTx, ctx context.Context, fileID int64, thumb []byte) (int64, error) {
	thumbnailID, err := qtx.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
		FileID:    fileID,
		SizeLabel: "m",
		Height:    0,
		Width:     0,
		Format:    "jpg",
		CreatedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		UpdatedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	})
	if err != nil {
		return 0, err
	}

	if err := qtx.UpsertThumbnailBlob(ctx, gallerydb.UpsertThumbnailBlobParams{
		ThumbnailID: thumbnailID,
		Data:        thumb,
	}); err != nil {
		return 0, err
	}
	return thumbnailID, nil
}

// NeedsThumbnail checks if a thumbnail for a given file ID already exists.
func NeedsThumbnail(ctx context.Context, cpcRo *dbconnpool.CpConn, fileID int64) (bool, error) {
	exists, err := cpcRo.Queries.GetThumbnailExistsViewByID(ctx, fileID)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil // Needs thumbnail
		}
		slog.Error("thumbnailExists check failed", "fileID", fileID, "err", err)
		return false, err
	}
	return !exists, nil // If it exists, it doesn't need one
}

// NeedsFolderTileUpdate checks if a tile image has already been assigned for a given folder path.
func NeedsFolderTileUpdate(ctx context.Context, cpcRo *dbconnpool.CpConn, folderPath string) (bool, error) {
	exists, err := cpcRo.Queries.GetFolderTileExistsViewByPath(ctx, folderPath)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil // Needs folder tile update
		}
		slog.Error("directoryTileExists check failed", "dir", folderPath, "err", err)
		return false, err
	}
	return !exists, nil // If it exists, it doesn't need an update
}

// WriteFileInTx performs all database writes for a single processed file within
// the provided transaction. It handles: UpsertPathChain, DeleteInvalidFileByPath,
// UpsertExif, UpsertThumbnail (if needed), and UpdateFolderTileChain.
//
// The caller (FlushFunc) manages BeginTx/Commit/Rollback. This function only
// executes SQL statements within the provided tx.
//
// After writing, thumbnail buffers are returned to the pool. f.Thumbnail will be
// nil on return.
func WriteFileInTx(ctx context.Context, tx *sql.Tx, f *File) error {
	q := gallerydb.NewCustomQueries(tx)
	imp := &gallerylib.Importer{Conn: nil, Q: q}

	var thumb []byte
	if f.Thumbnail != nil {
		thumb = f.Thumbnail.Bytes()
	}

	// 1. UpsertPathChain — creates folder chain + file record
	dbFile, err := imp.UpsertPathChain(ctx, f.Path,
		f.File.Mtime.Int64, f.File.SizeBytes.Int64,
		f.File.Md5.String, f.File.Phash.Int64,
		f.File.Width.Int64, f.File.Height.Int64,
		f.File.MimeType.String)
	if err != nil {
		return fmt.Errorf("upsert path chain %s: %w", f.Path, err)
	}
	f.File.ID = dbFile.ID
	f.File = dbFile

	// 2. Clear stale invalid_files entry
	if delErr := q.DeleteInvalidFileByPath(ctx, f.Path); delErr != nil {
		slog.Warn("delete invalid file on success", "path", f.Path, "err", delErr)
	}

	// 3. UpsertExif if available (non-fatal)
	if f.Exif.CameraMake.Valid {
		f.Exif.FileID = dbFile.ID
		if upsertErr := q.UpsertExif(ctx, f.Exif); upsertErr != nil {
			slog.Error("upsert exif", "path", f.Path, "err", upsertErr)
		}
	}

	// 4. Check if thumbnail needed
	// GetThumbnailExistsViewByID errors (other than sql.ErrNoRows) are treated
	// as non-fatal: we set needsThumb = true and log, so a transient view error
	// does not roll back the whole batch.
	needsThumb := true
	exists, err := q.GetThumbnailExistsViewByID(ctx, dbFile.ID)
	if err != nil && err != sql.ErrNoRows {
		slog.Warn("check thumbnail exists, assuming needed", "path", f.Path, "file_id", dbFile.ID, "err", err)
	} else if err == nil {
		needsThumb = !exists
	}

	// 5. Upsert thumbnail if needed
	if needsThumb && len(thumb) > 0 {
		if _, upsertErr := UpsertThumbnailTxOnly(q, ctx, dbFile.ID, thumb); upsertErr != nil {
			return fmt.Errorf("upsert thumbnail %s: %w", f.Path, err)
		}
	}

	// 6. Return thumbnail buffer to pool
	if f.Thumbnail != nil {
		thumbnail.PutBytesBuffer(f.Thumbnail)
		f.Thumbnail = nil
	}

	// 7. Folder tile update
	dir := path.Dir(f.Path)
	needsTile := true
	tileExists, err := q.GetFolderTileExistsViewByPath(ctx, dir)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("check folder tile", "path", dir, "err", err)
	} else if err == nil {
		needsTile = !tileExists
	}
	if needsTile && needsThumb && len(thumb) > 0 {
		if err := imp.UpdateFolderTileChain(ctx, dbFile.FolderID.Int64, dbFile.ID); err != nil {
			slog.Error("update folder tile chain", "path", f.Path, "err", err)
			// non-fatal: don't fail the whole file for a tile
		}
	}

	return nil
}

// GenerateThumbnailAndUpdateDbIfNeeded orchestrates the thumbnail generation and database update process.
// It ensures the file and its parent folders are recorded in the database, checks if a thumbnail
// already exists, and if not, generates and stores a new one. It then updates the parent
// folder's tile image if necessary.
//
// Deprecated: Prefer the WriteFileInTx path (used by the file write batcher), which batches
// file and thumbnail writes in a single transaction per batch.
func GenerateThumbnailAndUpdateDbIfNeeded(
	ctx context.Context,
	cpcRw *dbconnpool.CpConn,
	cpcRo *dbconnpool.CpConn,
	f *File,
	importerFactory func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer,
) error {
	var (
		err   error
		thumb []byte
	)
	if f.Thumbnail != nil {
		thumb = f.Thumbnail.Bytes()
	}

	fn := filepath.Join(f.ImagesDir, filepath.FromSlash(f.Path))
	// Use the RW DB connection and queries for upserts so writes go to the writeable DB.
	imp := importerFactory(cpcRw.Conn, cpcRw.Queries)

	dbFile, err := imp.UpsertPathChain(ctx, f.Path, f.File.Mtime.Int64, f.File.SizeBytes.Int64, f.File.Md5.String, f.File.Phash.Int64, f.File.Width.Int64, f.File.Height.Int64, f.File.MimeType.String)
	if err != nil {
		slog.Error("failed to upsert path chain", "f.Path", f.Path, "err", err)
		return err
	}
	f.File.ID = dbFile.ID
	f.File = dbFile

	// Clear any stale invalid_files entry now that this path succeeded
	if delErr := cpcRw.Queries.DeleteInvalidFileByPath(ctx, f.Path); delErr != nil {
		slog.Warn("delete invalid file on success", "path", f.Path, "err", delErr)
	}

	doGen, err := NeedsThumbnail(ctx, cpcRo, f.File.ID)
	if err != nil {
		return err
	}

	// Save EXIF data if it exists
	if f.Exif.CameraMake.Valid { // Check a field to see if there's any exif data
		// Ensure we set the FileID before upserting so the EXIF row is associated
		// with the correct file in the database.
		f.Exif.FileID = dbFile.ID
		if err2 := cpcRw.Queries.UpsertExif(ctx, f.Exif); err2 != nil {
			slog.Error("failed to upsert exif data", "file", fn, "err", err2)
			// Do not return error, as this is not critical to the main flow
		}
	}

	if !doGen {
		return nil
	}

	if len(thumb) == 0 {
		slog.Error("generateThumbnail returned empty thumbnail", "file", fn)
		return err
	}

	thumbnailID, err := UpsertThumbnail(ctx, cpcRw, f.File.ID, thumb)
	if err != nil {
		slog.Error("failed to store thumbnail", "file", fn, "err", err)
		return err
	}
	if f.Thumbnail != nil {
		thumbnail.PutBytesBuffer(f.Thumbnail)
	}

	_ = thumbnailID

	// For DB queries we need the canonical forward-slash path. Use path.Dir
	// on the DB-style f.Path (which is stored with forward slashes).
	doTileUpdate, err := NeedsFolderTileUpdate(ctx, cpcRo, path.Dir(f.Path))
	if err != nil {
		return err
	}

	// Use RW queries for the tile update (it's a write operation)
	imp = importerFactory(nil, cpcRw.Queries)

	if doTileUpdate {
		err = imp.UpdateFolderTileChain(ctx, f.File.FolderID.Int64, f.File.ID)
		if err != nil {
			slog.Error("failed to set directory tile after thumbnail generation", "file", fn, "err", err)
			return err
		}
	}
	return nil
}
