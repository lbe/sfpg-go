package files

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

type fakeInvalidFileDeleter struct {
	called bool
	path   string
	err    error
}

func (f *fakeInvalidFileDeleter) DeleteInvalidFileByPath(ctx context.Context, path string) error {
	f.called = true
	f.path = path
	return f.err
}

func TestClearStaleInvalidFile(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fake := &fakeInvalidFileDeleter{}
		clearStaleInvalidFile(context.Background(), fake, "foo/bar.jpg")
		if !fake.called {
			t.Error("expected DeleteInvalidFileByPath to be called")
		}
		if fake.path != "foo/bar.jpg" {
			t.Errorf("path = %q, want %q", fake.path, "foo/bar.jpg")
		}
	})

	t.Run("error path does not panic", func(t *testing.T) {
		fake := &fakeInvalidFileDeleter{err: errors.New("delete failed")}
		clearStaleInvalidFile(context.Background(), fake, "foo/bar.jpg")
		if !fake.called {
			t.Error("expected DeleteInvalidFileByPath to be called")
		}
	})
}

func TestWriteFileInTx(t *testing.T) {
	t.Run("success with thumbnail", func(t *testing.T) {
		_, rwPool, _, ctx := createTestPoolsAndDir(t)

		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)

		tx, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()

		buf := thumbnail.GetBytesBuffer()
		if _, err := buf.Write([]byte("thumb-data")); err != nil {
			t.Fatalf("write thumb buffer: %v", err)
		}

		f := &File{
			Path:      "album/photo.jpg",
			Exists:    false,
			Thumbnail: buf,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
				Md5:       sql.NullString{String: "md5", Valid: true},
				Phash:     sql.NullInt64{Int64: 123, Valid: true},
				Width:     sql.NullInt64{Int64: 100, Valid: true},
				Height:    sql.NullInt64{Int64: 100, Valid: true},
			},
		}

		qtx := cpcRw.Queries.WithTx(tx)
		imp := &gallerylib.Importer{Q: qtx}
		if err := WriteFileInTx(ctx, imp, f); err != nil {
			t.Fatalf("WriteFileInTx: %v", err)
		}

		if f.Thumbnail != nil {
			t.Error("expected thumbnail buffer returned to pool")
		}
	})

	t.Run("no thumbnail", func(t *testing.T) {
		_, rwPool, _, ctx := createTestPoolsAndDir(t)

		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)

		tx, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()

		f := &File{
			Path:   "album/nothumb.jpg",
			Exists: false,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}

		qtx := cpcRw.Queries.WithTx(tx)
		imp := &gallerylib.Importer{Q: qtx}
		if err := WriteFileInTx(ctx, imp, f); err != nil {
			t.Fatalf("WriteFileInTx: %v", err)
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		_, rwPool, _, ctx := createTestPoolsAndDir(t)

		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)

		tx, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()

		buf := thumbnail.GetBytesBuffer()
		if _, writeErr := buf.Write([]byte("thumb-data")); writeErr != nil {
			t.Fatalf("write thumb buffer: %v", writeErr)
		}
		f := &File{
			Path:      "album/cancelled.jpg",
			Exists:    false,
			Thumbnail: buf,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}

		qtx := cpcRw.Queries.WithTx(tx)
		imp := &gallerylib.Importer{Q: qtx}

		ctx, cancel := context.WithCancel(ctx)
		cancel()

		if err := WriteFileInTx(ctx, imp, f); err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})

	t.Run("existing file with thumbnail", func(t *testing.T) {
		_, rwPool, _, ctx := createTestPoolsAndDir(t)

		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)

		// First write: create file and thumbnail.
		tx1, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		buf := thumbnail.GetBytesBuffer()
		if _, writeErr := buf.Write([]byte("thumb-data")); writeErr != nil {
			t.Fatalf("write thumb buffer: %v", writeErr)
		}
		f := &File{
			Path:      "album/existing.jpg",
			Exists:    false,
			Thumbnail: buf,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}
		qtx1 := cpcRw.Queries.WithTx(tx1)
		imp1 := &gallerylib.Importer{Q: qtx1}
		if writeErr := WriteFileInTx(ctx, imp1, f); writeErr != nil {
			tx1.Rollback()
			t.Fatalf("first WriteFileInTx: %v", writeErr)
		}
		if commitErr := tx1.Commit(); commitErr != nil {
			t.Fatalf("Commit: %v", commitErr)
		}

		// Second write: f.Exists=true should detect existing thumbnail and skip upsert.
		tx2, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx2.Rollback()
		f2 := &File{
			Path:      "album/existing.jpg",
			Exists:    true,
			Thumbnail: bytes.NewBuffer([]byte("new-thumb")),
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}
		qtx2 := cpcRw.Queries.WithTx(tx2)
		imp2 := &gallerylib.Importer{Q: qtx2}
		if err := WriteFileInTx(ctx, imp2, f2); err != nil {
			t.Fatalf("second WriteFileInTx: %v", err)
		}
	})
}

func TestGenerateThumbnailAndUpdateDbIfNeeded(t *testing.T) {
	t.Run("nil file", func(t *testing.T) {
		err := GenerateThumbnailAndUpdateDbIfNeeded(context.Background(), nil, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil file")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("roPool.Get: %v", err)
		}
		defer roPool.Put(cpcRo)

		importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		f := &File{ImagesDir: imagesDir, Path: "test.jpg"}
		err = GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory)
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})

	t.Run("empty thumbnail", func(t *testing.T) {
		roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("roPool.Get: %v", err)
		}
		defer roPool.Put(cpcRo)

		importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		}

		f := &File{
			ImagesDir: imagesDir,
			Path:      "empty.jpg",
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
			Thumbnail: bytes.NewBuffer(nil),
		}
		err = GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory)
		if err == nil {
			t.Fatal("expected error for empty thumbnail")
		}
	})

	t.Run("needs thumbnail error", func(t *testing.T) {
		roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("roPool.Get: %v", err)
		}
		defer roPool.Put(cpcRo)

		importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		}

		// Close RO prepared statements so NeedsThumbnail returns an error.
		if closeErr := cpcRo.Queries.Close(); closeErr != nil {
			t.Fatalf("close queries: %v", closeErr)
		}

		f := &File{
			ImagesDir: imagesDir,
			Path:      "needs-thumb-err.jpg",
			Thumbnail: bytes.NewBuffer([]byte("thumb")),
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				SizeBytes: sql.NullInt64{Int64: 1, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}
		err = GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory)
		if err == nil {
			t.Fatal("expected error from NeedsThumbnail")
		}
	})

	t.Run("success", func(t *testing.T) {
		roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("roPool.Get: %v", err)
		}
		defer roPool.Put(cpcRo)

		importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		}

		buf := thumbnail.GetBytesBuffer()
		if _, writeErr := buf.Write([]byte("thumb-bytes")); writeErr != nil {
			t.Fatalf("write thumb buffer: %v", writeErr)
		}

		f := &File{
			ImagesDir: imagesDir,
			Path:      path.Join("album", "success.jpg"),
			Thumbnail: buf,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				SizeBytes: sql.NullInt64{Int64: 123, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}
		if genErr := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory); genErr != nil {
			t.Fatalf("GenerateThumbnailAndUpdateDbIfNeeded: %v", genErr)
		}

		dbFile, err := cpcRo.Queries.GetFileByPath(ctx, f.Path)
		if err != nil {
			t.Fatalf("GetFileByPath: %v", err)
		}
		thumbExists, err := cpcRo.Queries.GetThumbnailExistsViewByID(ctx, dbFile.ID)
		if err != nil {
			t.Fatalf("GetThumbnailExistsViewByID: %v", err)
		}
		if !thumbExists {
			t.Error("expected thumbnail to exist")
		}
	})

	t.Run("no thumbnail needed", func(t *testing.T) {
		roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		defer rwPool.Put(cpcRw)
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("roPool.Get: %v", err)
		}
		defer roPool.Put(cpcRo)

		importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		}

		// First call creates the file and thumbnail.
		buf := thumbnail.GetBytesBuffer()
		if _, writeErr := buf.Write([]byte("thumb-bytes")); writeErr != nil {
			t.Fatalf("write thumb buffer: %v", writeErr)
		}
		f := &File{
			ImagesDir: imagesDir,
			Path:      path.Join("album", "exists.jpg"),
			Thumbnail: buf,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				SizeBytes: sql.NullInt64{Int64: 123, Valid: true},
				MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			},
		}
		if genErr := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory); genErr != nil {
			t.Fatalf("first GenerateThumbnailAndUpdateDbIfNeeded: %v", genErr)
		}

		// Second call with f.Exists true should skip thumbnail generation.
		dbFile, err := cpcRo.Queries.GetFileByPath(ctx, f.Path)
		if err != nil {
			t.Fatalf("GetFileByPath: %v", err)
		}
		f2 := &File{
			ImagesDir: imagesDir,
			Path:      f.Path,
			Exists:    true,
			File:      dbFile,
			Thumbnail: bytes.NewBuffer([]byte("new-thumb")),
		}
		if genErr := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f2, importerFactory); genErr != nil {
			t.Fatalf("second GenerateThumbnailAndUpdateDbIfNeeded: %v", genErr)
		}
	})
}
