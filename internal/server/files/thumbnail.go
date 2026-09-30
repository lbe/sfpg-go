package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// invalidFileDeleter is the minimal interface needed to clear a stale
// invalid_files row. Both *gallerydb.CustomQueries and the query object
// embedded in gallerylib.Importer satisfy it.
type invalidFileDeleter interface {
	DeleteInvalidFileByPath(ctx context.Context, path string) error
}

// clearStaleInvalidFile removes any invalid_files row for the given path.
// A previously-invalid file that has since become valid must be importable on
// subsequent runs, so WriteFileInTx always deletes the stale entry after a
// successful import.
// fileExistedBeforeUpsert reports whether UpsertPathChain updated an existing
// files row (ON CONFLICT) rather than inserting a new one, using returned
// created_at vs updated_at from the upsert.
func fileExistedBeforeUpsert(dbFile gallerydb.File) bool {
	created, okC := unixTimeFromDBValue(dbFile.CreatedAt)
	updated, okU := unixTimeFromDBValue(dbFile.UpdatedAt)
	if !okC || !okU {
		return false
	}
	return updated > created
}

func unixTimeFromDBValue(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	default:
		return 0, false
	}
}

func clearStaleInvalidFile(ctx context.Context, q invalidFileDeleter, path string) {
	if err := q.DeleteInvalidFileByPath(ctx, path); err != nil {
		slog.Warn("delete invalid file on success", "path", path, "err", err)
	}
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

// WriteFileInTx performs all database writes for a single processed file within
// the provided transaction. It handles: UpsertPathChain, DeleteInvalidFileByPath,
// UpsertExif, UpsertXMP (raw + properties, if captured), UpsertThumbnail (if
// needed), and UpdateFolderTileChain.
//
// The caller (FlushFunc) manages BeginTx/Commit/Rollback. This function only
// executes SQL statements within the provided tx.
//
// imp must be backed by the same *gallerydb.CustomQueries that is bound to tx
// (i.e. imp.Q is the WithTx(tx) view). Construct ONE imp per batch and reuse it
// across files so imp's intra-batch folder cache and tiled-dir set persist.
//
// After writing, thumbnail buffers are returned to the pool. f.Thumbnail will be
// nil on return.
func WriteFileInTx(ctx context.Context, imp *gallerylib.Importer, f *File) error {
	q := imp.Q

	var thumb []byte
	if f.Thumbnail != nil {
		thumb = f.Thumbnail.Bytes()
	}

	preExisted := f.Exists

	// 1. UpsertPathChain — creates folder chain + file record (uses imp.folderCache)
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
	f.Exists = preExisted || fileExistedBeforeUpsert(dbFile)

	// 2. Clear stale invalid_files entry.
	// Always delete any invalid_files row for this path: a previously-invalid
	// file that has since become valid must be importable on subsequent runs.
	clearStaleInvalidFile(ctx, q, f.Path)

	// 3. UpsertExif if available (non-fatal)
	if f.Exif.CameraMake.Valid {
		f.Exif.FileID = dbFile.ID
		if upsertErr := q.UpsertExif(ctx, f.Exif); upsertErr != nil {
			slog.Error("upsert exif", "path", f.Path, "err", upsertErr)
		}
	}

	// 3b. Upsert XMP if captured (non-fatal)
	upsertFileXMP(ctx, q, f)

	// 4. Check if thumbnail needed.
	// When !f.Exists the file row is new (it did not exist before UpsertPathChain
	// created it), and thumbnails.file_id has a NOT NULL FK to files(id) ON DELETE
	// CASCADE with foreign_keys(true) — so a thumbnail cannot pre-exist for a
	// brand-new file. Skip the 3-table JOIN view query entirely in that case.
	// For re-imports (f.Exists) a thumbnail may already exist, so query the view.
	needsThumb := true
	if f.Exists {
		exists, err := q.GetThumbnailExistsViewByID(ctx, dbFile.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("check thumbnail exists, assuming needed", "path", f.Path, "file_id", dbFile.ID, "err", err)
		} else if err == nil {
			needsThumb = !exists
		}
	}

	// 5. Upsert thumbnail if needed
	if needsThumb && len(thumb) > 0 {
		if _, upsertErr := UpsertThumbnailTxOnly(q, ctx, dbFile.ID, thumb); upsertErr != nil {
			return fmt.Errorf("upsert thumbnail %s: %w", f.Path, upsertErr)
		}
	}

	// 6. Return thumbnail buffer to pool
	if f.Thumbnail != nil {
		thumbnail.PutBytesBuffer(f.Thumbnail)
		f.Thumbnail = nil
	}

	// 7. Folder tile update.
	// If this directory was already tiled earlier in this batch, skip both the
	// GetFolderTileExistsViewByPath query and the UpdateFolderTileChain walk —
	// the dir (and its ancestors, since the chain stops at the first tiled
	// ancestor) already have tiles. Only mark the dir tiled after a successful
	// chain update, so a failed tile update doesn't suppress a later retry.
	dir := path.Dir(f.Path)
	if !imp.IsDirTiled(dir) {
		needsTile := true
		tileExists, err := q.GetFolderTileExistsViewByPath(ctx, dir)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("check folder tile", "path", dir, "err", err)
		} else if err == nil {
			needsTile = !tileExists
		}
		if needsTile && needsThumb && len(thumb) > 0 {
			if err := imp.UpdateFolderTileChain(ctx, dbFile.FolderID.Int64, dbFile.ID); err != nil {
				slog.Error("update folder tile chain", "path", f.Path, "err", err)
				// non-fatal: don't fail the whole file for a tile
			} else {
				imp.MarkDirTiled(dir)
			}
		}
	}

	return nil
}
