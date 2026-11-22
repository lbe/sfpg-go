package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/tableswap"
	"github.com/lbe/sfpg-go/internal/thumbnail"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// testWriter is a Writer on a new database, with the callbacks it ran.
type testWriter struct {
	*Writer
	paths       DatabasePaths
	rw, ro      *dbconnpool.DbSQLConnPool
	cacheBytes  int64
	cacheCount  int64
	folders     int
	cacheFailed int
}

// newTestWriter starts a Writer on a new database in a temporary directory.
// Cleanup closes the Writer, then the pools.
func newTestWriter(t *testing.T) *testWriter {
	t.Helper()
	paths, rw, ro, err := Setup(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rw.Close()
		_ = ro.Close()
	})
	tw := &testWriter{paths: paths, rw: rw, ro: ro}
	tw.Writer, err = NewWriter(context.Background(), WriterConfig{
		Paths:           paths,
		RW:              rw,
		RO:              ro,
		OnFolderCreated: func() { tw.folders++ },
		OnCacheStored: func(bytes, entries int64) {
			tw.cacheBytes += bytes
			tw.cacheCount += entries
		},
		OnCacheFailed: func() { tw.cacheFailed++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tw.Close() })
	return tw
}

// query runs a single-row query on the read-only pool.
func (tw *testWriter) query(t *testing.T, q string, dest ...any) {
	t.Helper()
	cpc, err := tw.ro.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.ro.Put(cpc)
	if err := cpc.Conn.QueryRowContext(context.Background(), q).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// exec runs a statement on the read-write pool.
func (tw *testWriter) exec(t *testing.T, q string) {
	t.Helper()
	cpc, err := tw.rw.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.rw.Put(cpc)
	if _, err := cpc.Conn.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// count returns the result of a COUNT(*) query.
func (tw *testWriter) count(t *testing.T, q string) int {
	t.Helper()
	cpc, err := tw.ro.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.ro.Put(cpc)
	var n int
	if err := cpc.Conn.QueryRowContext(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// testFile returns a processed file at path with a pooled thumbnail.
func testFile(path string, thumb []byte) *files.File {
	buf := thumbnail.GetBytesBuffer()
	buf.Write(thumb)
	return &files.File{
		Path: path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
			SizeBytes: sql.NullInt64{Int64: 1234, Valid: true},
			Md5:       sql.NullString{String: "0123456789abcdef0123456789abcdef", Valid: true},
			Phash:     sql.NullInt64{Int64: 42, Valid: true},
			MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			Width:     sql.NullInt64{Int64: 800, Valid: true},
			Height:    sql.NullInt64{Int64: 600, Valid: true},
		},
		Thumbnail: buf,
		Metadata:  metadata.Info{Exif: metadata.Exif{CameraMake: "acme"}},
	}
}

func TestWriterWritesEachKind(t *testing.T) {
	tw := newTestWriter(t)
	thumb := []byte("thumbnail bytes")
	if err := tw.SubmitFile(testFile("a/b/one.jpg", thumb)); err != nil {
		t.Fatal(err)
	}
	// Its folder has no valid file, so the flush must create it.
	if err := tw.SubmitInvalidFile(files.InvalidFile{Path: "c/bad.jpg", Mtime: 5, Size: 6, Reason: "decode"}); err != nil {
		t.Fatal(err)
	}
	entry := cachelite.GetHTTPCacheEntry()
	entry.Key, entry.Path, entry.Method, entry.Status = "k", "/gallery/1", "GET", 200
	entry.Body = append(entry.Body, "body"...)
	entry.ContentLength = sql.NullInt64{Int64: 4, Valid: true}
	entry.CreatedAt = 1
	tw.SubmitCacheEntry(entry)
	if err := tw.Close(); err != nil { // flushes what is in memory
		t.Fatal(err)
	}

	var got []byte
	tw.query(t, `SELECT tb.data FROM thumbnails t JOIN thumbs.thumbnail_blobs tb ON tb.thumbnail_id = t.id
		JOIN files f ON f.id = t.file_id JOIN file_paths p ON p.id = f.path_id WHERE p.path = 'a/b/one.jpg'`, &got)
	if !bytes.Equal(got, thumb) {
		t.Errorf("thumbnail = %q, want %q", got, thumb)
	}
	var make string
	tw.query(t, "SELECT camera_make FROM exif_metadata", &make)
	if make != "acme" {
		t.Errorf("camera_make = %q, want acme", make)
	}
	var reason string
	var folderPath string
	tw.query(t, `SELECT i.reason, fp.path FROM invalid_files i JOIN folders fo ON fo.id = i.folder_id
		JOIN folder_paths fp ON fp.id = fo.path_id WHERE i.path = 'c/bad.jpg'`, &reason, &folderPath)
	if reason != "decode" || folderPath != "c" {
		t.Errorf("invalid file reason, folder = %q, %q; want decode, c", reason, folderPath)
	}
	if n := tw.count(t, "SELECT COUNT(*) FROM http_cache WHERE key = 'k'"); n != 1 {
		t.Errorf("http_cache rows = %d, want 1", n)
	}
	if tw.cacheBytes != 4 || tw.cacheCount != 1 {
		t.Errorf("OnCacheStored got %d bytes, %d entries; want 4, 1", tw.cacheBytes, tw.cacheCount)
	}
	if tw.folders != 3 { // a, a/b, c
		t.Errorf("OnFolderCreated ran %d times, want 3", tw.folders)
	}
}

func TestWriterRecordsInvalidFileInRoot(t *testing.T) {
	tw := newTestWriter(t)
	if err := tw.SubmitInvalidFile(files.InvalidFile{Path: "bad.jpg", Mtime: 1, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var reason sql.NullString
	var folderPath string
	tw.query(t, `SELECT i.reason, fp.path FROM invalid_files i JOIN folders fo ON fo.id = i.folder_id
		JOIN folder_paths fp ON fp.id = fo.path_id WHERE i.path = 'bad.jpg'`, &reason, &folderPath)
	if reason.Valid || folderPath != "" {
		t.Errorf("reason, folder = %v, %q; want NULL, root", reason, folderPath)
	}
}

// TestWriterRetriesFailedBatch fails a batch at its second file, after the
// first file's thumbnail is written, and checks that once the batch succeeds
// the first file has its own thumbnail.
func TestWriterRetriesFailedBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		tw.exec(t, "CREATE TABLE fail_flush (n INTEGER)")
		tw.exec(t, "INSERT INTO fail_flush VALUES (1)")
		tw.exec(t, `CREATE TRIGGER fail_files BEFORE INSERT ON files
			WHEN NEW.filename = 'two.jpg' AND (SELECT COUNT(*) FROM fail_flush) > 0
			BEGIN SELECT RAISE(ABORT, 'test abort'); END`)

		thumb := []byte("the file's own thumbnail")
		for _, f := range []*files.File{testFile("x/one.jpg", thumb), testFile("x/two.jpg", []byte("two"))} {
			if err := tw.SubmitFile(f); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if tw.GetStats().TotalErrors == 0 {
			t.Fatal("the flush did not fail")
		}
		// A buffer taken from the pool now must not be the file's thumbnail.
		other := thumbnail.GetBytesBuffer()
		other.Reset()
		other.WriteString("someone else's bytes")

		tw.exec(t, "DELETE FROM fail_flush")
		time.Sleep(time.Second)
		synctest.Wait()
		if n := tw.PendingCount(); n != 0 {
			t.Fatalf("%d writes pending after the retry", n)
		}
		var got []byte
		tw.query(t, `SELECT tb.data FROM thumbnails t JOIN thumbs.thumbnail_blobs tb ON tb.thumbnail_id = t.id
			JOIN files f ON f.id = t.file_id WHERE f.filename = 'one.jpg'`, &got)
		if !bytes.Equal(got, thumb) {
			t.Errorf("thumbnail = %q, want %q", got, thumb)
		}
		// The failed attempts created folder x too, and were rolled back.
		if tw.folders != 1 {
			t.Errorf("OnFolderCreated ran %d times, want 1", tw.folders)
		}
	})
}

func TestWriterDropsWritesAfterClose(t *testing.T) {
	tw := newTestWriter(t)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tw.SubmitFile(testFile("late.jpg", []byte("t"))); !errors.Is(err, writebatcher.ErrClosed) {
		t.Errorf("SubmitFile after Close = %v, want %v", err, writebatcher.ErrClosed)
	}
	if err := tw.SubmitInvalidFile(files.InvalidFile{Path: "late.jpg"}); !errors.Is(err, writebatcher.ErrClosed) {
		t.Errorf("SubmitInvalidFile after Close = %v, want %v", err, writebatcher.ErrClosed)
	}
	entry := cachelite.GetHTTPCacheEntry()
	entry.Key = "k"
	tw.SubmitCacheEntry(entry)
	if entry.Key != "" || tw.PendingCount() != 0 {
		t.Errorf("cache entry after Close: key %q, %d pending; want it back in the pool, none pending", entry.Key, tw.PendingCount())
	}
}

func TestCheckpointWAL(t *testing.T) {
	tw := newTestWriter(t)
	walSize := func() int64 {
		info, err := os.Stat(tw.walPath)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	write := func() {
		tw.exec(t, "INSERT INTO file_paths (path) VALUES (hex(randomblob(16)))")
		if walSize() == 0 {
			t.Fatal("the write left the WAL empty")
		}
	}
	due := time.Now().Add(-walCheckpointAge)
	ctx := context.Background()

	write()
	tw.index.scanHeld.Store(true)
	tw.checkpointWAL(ctx, tw.rw, due, false)
	if walSize() == 0 {
		t.Error("checkpoint ran while the rebuild's cursor was open")
	}
	tw.index.scanHeld.Store(false)

	tw.wroteDML.Store(false)
	tw.checkpointWAL(ctx, tw.rw, due, true)
	if walSize() == 0 {
		t.Error("checkpoint ran after a flush that wrote nothing")
	}

	tw.checkpointWAL(ctx, tw.rw, time.Now(), false)
	if walSize() == 0 {
		t.Error("checkpoint ran before it was due")
	}

	tw.checkpointWAL(ctx, tw.rw, due, false)
	if n := walSize(); n != 0 {
		t.Errorf("WAL is %d bytes after a due checkpoint, want 0", n)
	}

	// A WAL over the size limit is checkpointed after a flush that wrote,
	// however recent the last checkpoint. The file is extended past its last
	// valid frame; SQLite ignores what follows.
	write()
	if err := os.Truncate(tw.walPath, walCheckpointBytes+1); err != nil {
		t.Fatal(err)
	}
	tw.wroteDML.Store(true)
	tw.checkpointWAL(ctx, tw.rw, time.Time{}, true)
	if n := walSize(); n != 0 {
		t.Errorf("WAL is %d bytes after a checkpoint for size, want 0", n)
	}
}

// TestWriterBatchOrder writes every kind of write in one batch and checks the
// order the flush writes them in: files, invalid files, gallery cache
// entries, other cache entries, then folder-index rows. The valid and the
// invalid file share a folder that no committed row has yet.
func TestWriterBatchOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		tw.exec(t, "CREATE TABLE write_log (id INTEGER PRIMARY KEY, kind TEXT)")
		cpc, err := tw.rw.Get()
		if err != nil {
			t.Fatal(err)
		}
		err = tableswap.CloneEmpty(context.Background(), cpc.Conn, "file_folder_index")
		tw.rw.Put(cpc)
		if err != nil {
			t.Fatal(err)
		}
		for _, trigger := range []string{
			`CREATE TRIGGER log_files AFTER INSERT ON files BEGIN INSERT INTO write_log (kind) VALUES ('file'); END`,
			`CREATE TRIGGER log_invalid AFTER INSERT ON invalid_files BEGIN INSERT INTO write_log (kind) VALUES ('invalid'); END`,
			`CREATE TRIGGER log_cache AFTER INSERT ON http_cache BEGIN INSERT INTO write_log (kind)
				VALUES (CASE WHEN NEW.path LIKE '/gallery/%' THEN 'gallery cache' ELSE 'cache' END); END`,
			`CREATE TRIGGER log_index AFTER INSERT ON file_folder_index_new BEGIN INSERT INTO write_log (kind) VALUES ('index'); END`,
		} {
			tw.exec(t, trigger)
		}
		tw.index.active.Store(true)
		generation := tw.index.bumpGeneration()

		cache := func(path string) *cachelite.HTTPCacheEntry {
			e := cachelite.GetHTTPCacheEntry()
			e.Key, e.Path, e.Method, e.Status, e.CreatedAt = path, path, "GET", 200, 1
			e.Body = append(e.Body, "b"...)
			e.ContentLength = sql.NullInt64{Int64: 1, Valid: true}
			return e
		}
		// Submitted in the reverse of the order they must be written in.
		if err = tw.submit(batchedWrite{FolderIndex: &folderIndexRow{FileID: 1, FolderID: 2, ImageIndex: 1, ImageCount: 1, FirstID: 1, LastID: 1, Generation: generation}}); err != nil {
			t.Fatal(err)
		}
		tw.SubmitCacheEntry(cache("/info/1"))
		tw.SubmitCacheEntry(cache("/gallery/1"))
		if err = tw.SubmitInvalidFile(files.InvalidFile{Path: "new/bad.jpg", Reason: "decode"}); err != nil {
			t.Fatal(err)
		}
		if err = tw.SubmitFile(testFile("new/good.jpg", []byte("t"))); err != nil {
			t.Fatal(err)
		}
		if err = tw.Close(); err != nil { // one batch: nothing was flushed yet
			t.Fatal(err)
		}

		var got []string
		ro, err := tw.ro.Get()
		if err != nil {
			t.Fatal(err)
		}
		defer tw.ro.Put(ro)
		rows, err := ro.Conn.QueryContext(context.Background(), "SELECT kind FROM write_log ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			if err := rows.Scan(&kind); err != nil {
				t.Fatal(err)
			}
			got = append(got, kind)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		want := []string{"file", "invalid", "gallery cache", "cache", "index"}
		if !slices.Equal(got, want) {
			t.Errorf("write order = %q, want %q", got, want)
		}
		if tw.folders != 1 {
			t.Errorf("OnFolderCreated ran %d times, want 1 for the shared folder", tw.folders)
		}
	})
}

// TestWriterSameFileTwiceInOneBatch checks that a file written twice in one
// batch, as when a queued write and a new one meet, gets one thumbnail and
// does not fail the batch.
func TestWriterSameFileTwiceInOneBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		for range 2 {
			if err := tw.SubmitFile(testFile("d/same.jpg", []byte("t"))); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if n := tw.GetStats().TotalErrors; n != 0 {
			t.Errorf("%d failed flushes, want 0", n)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM thumbnails"); n != 1 {
			t.Errorf("thumbnails = %d, want 1", n)
		}
	})
}

// TestWriterOutlivesItsContext checks that canceling the context the Writer
// was started with does not stop it: a write submitted afterwards is written
// by Close.
func TestWriterOutlivesItsContext(t *testing.T) {
	paths, rw, ro, err := Setup(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rw.Close()
		_ = ro.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	w, err := NewWriter(ctx, WriterConfig{Paths: paths, RW: rw, RO: ro})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = w.SubmitFile(testFile("late.jpg", []byte("t"))); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	tw := &testWriter{ro: ro}
	if n := tw.count(t, "SELECT COUNT(*) FROM files"); n != 1 {
		t.Errorf("files = %d after Close, want 1", n)
	}
}

// TestWriterSameFileInConsecutiveBatches checks that a file written again in
// the next batch within the same second keeps its one thumbnail and does not
// fail the batch.
func TestWriterSameFileInConsecutiveBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		for range 2 {
			if err := tw.SubmitFile(testFile("d/same.jpg", []byte("t"))); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond) // past the flush interval, within the second
			synctest.Wait()
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if n := tw.GetStats().TotalErrors; n != 0 {
			t.Errorf("%d failed flushes, want 0", n)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM thumbnails"); n != 1 {
			t.Errorf("thumbnails = %d, want 1", n)
		}
	})
}

// TestWriterSkipsReplayedInvalidFile checks that an invalid record replayed
// after the same file imported, unchanged, is not written.
func TestWriterSkipsReplayedInvalidFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		f := testFile("d/ok.jpg", []byte("t"))
		if err := tw.SubmitFile(f); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		stale := files.InvalidFile{Path: "d/ok.jpg", Mtime: f.File.Mtime.Int64, Size: f.File.SizeBytes.Int64, Reason: "exif"}
		if err := tw.SubmitInvalidFile(stale); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if n := tw.count(t, "SELECT COUNT(*) FROM invalid_files"); n != 0 {
			t.Errorf("invalid_files = %d after the replayed record, want 0", n)
		}

		// A record for the file as it is now, changed, is written.
		changed := stale
		changed.Size++
		if err := tw.SubmitInvalidFile(changed); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM invalid_files"); n != 1 {
			t.Errorf("invalid_files = %d after a record for the changed file, want 1", n)
		}
	})
}
