package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/server/files"
)

// flush is one batcher's transaction callbacks. The batcher calls them from
// its one goroutine, so conn needs no lock: begin takes it from the RW pool
// and succeeded or failed returns it.
type flush struct {
	w       *Writer
	conn    *dbconnpool.CpConn
	folders int // folders the current attempt created, reported after commit
}

func (f *flush) begin(ctx context.Context) (*sql.Tx, error) {
	conn, err := f.w.rw.Get()
	if err != nil {
		return nil, err
	}
	f.conn = conn
	return conn.Conn.BeginTx(ctx, nil)
}

// putConn returns the batch's connection, if begin took one.
func (f *flush) putConn() {
	if f.conn != nil {
		f.w.rw.Put(f.conn)
		f.conn = nil
	}
}

// write writes the batch in tx: files, invalid files, gallery cache entries,
// other cache entries, then folder-index rows.
func (f *flush) write(ctx context.Context, tx *sql.Tx, batch []batchedWrite) error {
	f.w.wroteDML.Store(false)
	qtx := f.conn.Queries.WithTx(tx)
	indexRows, err := f.w.index.rowsToWrite(ctx, batch, qtx)
	if err != nil {
		return err
	}

	f.folders = 0
	imp := &gallerylib.Importer{Q: qtx, OnFolderCreated: func() { f.folders++ }}
	if err = writeFiles(ctx, imp, qtx, batch); err != nil {
		return err
	}
	if err = storeCacheEntries(ctx, tx, batch); err != nil {
		return err
	}
	if len(indexRows) > 0 {
		if err = qtx.InsertFileFolderIndexNewRows(ctx, indexRows); err != nil {
			return fmt.Errorf("insert folder index rows: %w", err)
		}
	}
	f.w.wroteDML.Store(len(indexRows) > 0 || slices.ContainsFunc(batch, func(bw batchedWrite) bool {
		return bw.File != nil || bw.InvalidFile != nil || bw.CacheEntry != nil
	}))
	return nil
}

// writeFiles writes the batch's files, then its invalid files.
func writeFiles(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries, batch []batchedWrite) error {
	for _, bw := range batch {
		if bw.File == nil {
			continue
		}
		if err := writeFile(ctx, imp, q, bw.File); err != nil {
			return fmt.Errorf("write file %s: %w", bw.File.Path, err)
		}
	}
	for _, bw := range batch {
		if bw.InvalidFile == nil {
			continue
		}
		if err := writeInvalidFile(ctx, imp, q, bw.InvalidFile); err != nil {
			return fmt.Errorf("write invalid file %s: %w", bw.InvalidFile.Path, err)
		}
	}
	return nil
}

// storeCacheEntries stores the batch's gallery cache entries, then the rest.
func storeCacheEntries(ctx context.Context, tx *sql.Tx, batch []batchedWrite) error {
	for _, bw := range batch {
		if e := bw.CacheEntry; e != nil && isGalleryPath(e.Path) {
			if err := cachelite.StoreCacheEntryInTx(ctx, tx, e); err != nil {
				return fmt.Errorf("store gallery cache %s: %w", e.Path, err)
			}
		}
	}
	for _, bw := range batch {
		if e := bw.CacheEntry; e != nil && !isGalleryPath(e.Path) {
			if err := cachelite.StoreCacheEntryInTx(ctx, tx, e); err != nil {
				return fmt.Errorf("store cache %s: %w", e.Path, err)
			}
		}
	}
	return nil
}

func isGalleryPath(p string) bool { return strings.HasPrefix(p, "/gallery/") }

func (f *flush) succeeded(batch []batchedWrite) {
	f.putConn()
	if f.w.onFolderCreated != nil {
		for range f.folders {
			f.w.onFolderCreated()
		}
	}
	f.folders = 0
	if bytes, entries := cacheTotals(batch); entries > 0 && f.w.onCacheStored != nil {
		f.w.onCacheStored(bytes, entries)
	}
	f.w.index.committed(batch)
	release(batch)
}

func (f *flush) failed(err error, batch []batchedWrite) {
	f.putConn()
	var fileCount, invalidCount, cacheCount, indexCount int
	for _, bw := range batch {
		switch {
		case bw.File != nil:
			fileCount++
		case bw.InvalidFile != nil:
			invalidCount++
		case bw.CacheEntry != nil:
			cacheCount++
		case bw.FolderIndex != nil:
			indexCount++
		}
	}
	slog.Error("failed to flush unified batch", "err", err, "files", fileCount,
		"invalid_files", invalidCount, "cache_entries", cacheCount, "folder_index", indexCount)
	if cacheCount > 0 && f.w.onCacheFailed != nil {
		f.w.onCacheFailed()
	}
}

// cacheTotals returns the body bytes and number of the batch's cache entries.
func cacheTotals(batch []batchedWrite) (bytes, entries int64) {
	for _, bw := range batch {
		if bw.CacheEntry != nil {
			bytes += int64(len(bw.CacheEntry.Body))
			entries++
		}
	}
	return bytes, entries
}

// writeFile writes one processed file: its path chain and row, its metadata,
// its thumbnail if it has none, and its folder's tile. It does not change f,
// so a failed batch can be written again.
func writeFile(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries, f *files.File) error {
	var thumb []byte
	if f.Thumbnail != nil {
		thumb = f.Thumbnail.Bytes()
	}

	dbFile, err := imp.UpsertPathChain(ctx, f.Path,
		f.File.Mtime.Int64, f.File.SizeBytes.Int64,
		f.File.Md5.String, f.File.Phash.Int64,
		f.File.Width.Int64, f.File.Height.Int64,
		f.File.MimeType.String)
	if err != nil {
		return fmt.Errorf("upsert path chain %s: %w", f.Path, err)
	}

	// A file that was invalid and is valid now must not stay skipped.
	if err := q.DeleteInvalidFileByPath(ctx, f.Path); err != nil {
		slog.Warn("delete invalid file on success", "path", f.Path, "err", err)
	}

	if err := writeMetadata(ctx, q, dbFile.ID, f.Metadata); err != nil {
		return fmt.Errorf("write metadata %s: %w", f.Path, err)
	}

	// A new file row cannot have a thumbnail yet (thumbnails.file_id references
	// files(id)), so the view is queried only for a file that already existed.
	// upsertThumbnail covers a file written twice within one second, which
	// existedBeforeUpsert cannot tell from a new one.
	needsThumb := true
	if existedBeforeUpsert(dbFile) {
		exists, err := q.GetThumbnailExistsViewByID(ctx, dbFile.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("check thumbnail exists, assuming needed", "path", f.Path, "file_id", dbFile.ID, "err", err)
		} else if err == nil {
			needsThumb = !exists
		}
	}
	if needsThumb && len(thumb) > 0 {
		if err := upsertThumbnail(ctx, q, dbFile.ID, thumb); err != nil {
			return fmt.Errorf("upsert thumbnail %s: %w", f.Path, err)
		}
		updateTile(ctx, imp, q, f.Path, dbFile)
	}
	return nil
}

// updateTile makes the file the tile of its folder and of each ancestor
// without one. A failure is logged, not returned: a missing tile does not
// fail the file. A directory tiled earlier in the batch is skipped, and a
// directory is marked tiled only after a successful update, so a failure is
// retried by the next file.
func updateTile(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries, filePath string, dbFile gallerydb.File) {
	dir := path.Dir(filePath)
	if imp.IsDirTiled(dir) {
		return
	}
	tileExists, err := q.GetFolderTileExistsViewByPath(ctx, dir)
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		slog.Error("check folder tile", "path", dir, "err", err)
	case err == nil && tileExists:
		return
	}
	if err := imp.UpdateFolderTileChain(ctx, dbFile.FolderID.Int64, dbFile.ID); err != nil {
		slog.Error("update folder tile chain", "path", filePath, "err", err)
		return
	}
	imp.MarkDirTiled(dir)
}

// existedBeforeUpsert reports whether UpsertPathChain updated an existing
// files row (ON CONFLICT) rather than inserting one: only an update makes
// updated_at later than created_at.
func existedBeforeUpsert(f gallerydb.File) bool {
	created, okC := unixTime(f.CreatedAt)
	updated, okU := unixTime(f.UpdatedAt)
	return okC && okU && updated > created
}

func unixTime(v any) (int64, bool) {
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

// upsertThumbnail stores thumb as the file's thumbnail. If the file already
// has one, it is kept: the unique conflict fails only this statement, not the
// transaction.
func upsertThumbnail(ctx context.Context, q *gallerydb.CustomQueries, fileID int64, thumb []byte) error {
	now := sql.NullInt64{Int64: time.Now().Unix(), Valid: true}
	thumbnailID, err := q.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
		FileID:    fileID,
		SizeLabel: "m",
		Format:    "jpg",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		return nil
	}
	if err != nil {
		return err
	}
	return q.UpsertThumbnailBlob(ctx, gallerydb.UpsertThumbnailBlobParams{ThumbnailID: thumbnailID, Data: thumb})
}

// writeInvalidFile records f in invalid_files, creating its folder if needed.
// A file imported since with the same mtime and size is not invalid: the
// record is an older one replayed from the overflow queue, and is skipped.
func writeInvalidFile(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries, f *files.InvalidFile) error {
	file, err := q.GetFileByPath(ctx, f.Path)
	switch {
	case err == nil && file.Mtime.Int64 == f.Mtime && file.SizeBytes.Int64 == f.Size:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	folderID, err := imp.UpsertFolderChain(ctx, path.Dir(f.Path))
	if err != nil {
		return err
	}
	return q.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
		Path:     f.Path,
		Mtime:    f.Mtime,
		Size:     f.Size,
		Reason:   sql.NullString{String: f.Reason, Valid: f.Reason != ""},
		FolderID: folderID,
	})
}

// writeMetadata replaces the stored metadata of a file with m, so a value
// that is gone from the file is gone from the database too.
func writeMetadata(ctx context.Context, q *gallerydb.CustomQueries, fileID int64, m metadata.Info) error {
	if err := q.DeleteExif(ctx, fileID); err != nil {
		return fmt.Errorf("delete exif: %w", err)
	}
	if m.Exif != (metadata.Exif{}) {
		if err := q.UpsertExif(ctx, exifParams(fileID, m.Exif)); err != nil {
			return fmt.Errorf("upsert exif: %w", err)
		}
	}

	if err := q.DeleteIPTCKeywords(ctx, fileID); err != nil {
		return fmt.Errorf("delete iptc keywords: %w", err)
	}
	p := iptcParams(fileID, m.IPTC)
	if p == (gallerydb.UpsertIPTCParams{FileID: fileID}) {
		if err := q.DeleteIPTC(ctx, fileID); err != nil {
			return fmt.Errorf("delete iptc: %w", err)
		}
	} else {
		if err := q.UpsertIPTC(ctx, p); err != nil {
			return fmt.Errorf("upsert iptc: %w", err)
		}
		for _, k := range m.IPTC.Keywords {
			if err := q.InsertIPTCKeyword(ctx, gallerydb.InsertIPTCKeywordParams{FileID: fileID, Keyword: k}); err != nil {
				return fmt.Errorf("insert iptc keyword: %w", err)
			}
		}
	}

	if err := q.DeleteXMPRaw(ctx, fileID); err != nil {
		return fmt.Errorf("delete xmp raw: %w", err)
	}
	if err := q.DeleteXMPProperties(ctx, fileID); err != nil {
		return fmt.Errorf("delete xmp properties: %w", err)
	}
	x := m.XMP
	if len(x.Packet) == 0 {
		return nil
	}
	if err := q.UpsertXMPRaw(ctx, gallerydb.UpsertXMPRawParams{FileID: fileID, RawXml: nullString(string(x.Packet))}); err != nil {
		return fmt.Errorf("upsert xmp raw: %w", err)
	}
	if x.GPSLatitude == "" {
		return nil
	}
	for _, prop := range [...]struct{ name, value string }{
		{"GPSLatitude", x.GPSLatitude},
		{"GPSLongitude", x.GPSLongitude},
	} {
		arg := gallerydb.InsertXMPPropertyParams{FileID: fileID, Namespace: "exif", Property: prop.name, Value: nullString(prop.value)}
		if err := q.InsertXMPProperty(ctx, arg); err != nil {
			return fmt.Errorf("insert xmp property: %w", err)
		}
	}
	return nil
}

func exifParams(fileID int64, e metadata.Exif) gallerydb.UpsertExifParams {
	p := gallerydb.UpsertExifParams{
		FileID:       fileID,
		CameraMake:   nullString(e.CameraMake),
		CameraModel:  nullString(e.CameraModel),
		LensModel:    nullString(e.LensModel),
		FocalLength:  nullString(e.FocalLength),
		Aperture:     nullString(e.Aperture),
		ShutterSpeed: nullString(e.ShutterSpeed),
		Iso:          nullInt64(e.ISO),
		Orientation:  nullInt64(e.Orientation),
		CaptureDate:  nullTime(e.CaptureDate),
	}
	if e.Latitude != 0 || e.Longitude != 0 {
		p.Latitude = sql.NullFloat64{Float64: e.Latitude, Valid: true}
		p.Longitude = sql.NullFloat64{Float64: e.Longitude, Valid: true}
	}
	if e.HasAltitude {
		p.Altitude = sql.NullFloat64{Float64: e.Altitude, Valid: true}
	}
	return p
}

func iptcParams(fileID int64, i metadata.IPTC) gallerydb.UpsertIPTCParams {
	p := gallerydb.UpsertIPTCParams{
		FileID:      fileID,
		Title:       nullString(i.Title),
		Description: nullString(i.Description),
		Creator:     nullString(i.Creator),
		Copyright:   nullString(i.Copyright),
		Credit:      nullString(i.Credit),
		Source:      nullString(i.Source),
		CreatedDate: nullTime(i.Created),
	}
	if len(i.Keywords) > 0 {
		b, _ := json.Marshal(i.Keywords) //nolint:errcheck // a []string always encodes
		p.Keywords = nullString(string(b))
	}
	return p
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullInt64(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }

func nullTime(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}
