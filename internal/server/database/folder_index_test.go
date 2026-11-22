package database

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/tableswap"
)

// folderIndexDBRow is one row of file_folder_index.
type folderIndexDBRow struct {
	fileID, folderID, imageIndex, imageCount int64
	prevID, nextID, firstID, lastID          sql.NullInt64
}

// validID is a non-NULL id for a navigation column.
func validID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }

// readFolderIndex returns every row of file_folder_index ordered by file id.
func readFolderIndex(t *testing.T, tw *testWriter) []folderIndexDBRow {
	t.Helper()
	cpc, err := tw.ro.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.ro.Put(cpc)
	rs, err := cpc.Conn.QueryContext(context.Background(),
		`SELECT file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id
		   FROM file_folder_index ORDER BY file_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []folderIndexDBRow
	for rs.Next() {
		var r folderIndexDBRow
		if err := rs.Scan(&r.fileID, &r.folderID, &r.imageIndex, &r.imageCount,
			&r.prevID, &r.nextID, &r.firstID, &r.lastID); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// seedFolderIndexFixture adds two folders and five files, ids 100, 1, 50 in
// folder 101 and ids 200, 2 in folder 102, plus an orphan with no folder. In
// folder 101 the filenames sort as 100, 50, 1, so image_index follows the
// filename, then the id.
func seedFolderIndexFixture(t *testing.T, tw *testWriter) {
	t.Helper()
	tw.exec(t, `INSERT INTO folder_paths (id, path) VALUES (101, 'folder-a'), (102, 'folder-b')`)
	tw.exec(t, `INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'folder-a'), (102, NULL, 102, 'folder-b')`)
	tw.exec(t, `INSERT INTO file_paths (id, path) VALUES (1001, 'f1a'), (1002, 'f1z'), (1003, 'f1m'),
		(1004, 'f2a'), (1005, 'f2z'), (1999, 'orphan')`)
	tw.exec(t, `INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
		VALUES (100, 101, 1001, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  1, 101, 1002, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       ( 50, 101, 1003, 'm.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (200, 102, 1004, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  2, 102, 1005, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (999, NULL, 1999, 'orphan.jpg', 1, 1, 'image/jpeg', 1, 1)`)
}

func TestRebuildFolderIndexNavigationAndOrphans(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		seedFolderIndexFixture(t, tw)
		if err := tw.RebuildFolderIndex(context.Background()); err != nil {
			t.Fatalf("RebuildFolderIndex: %v", err)
		}

		// Rows ordered by file id. The orphan (file 999) has no row.
		want := []folderIndexDBRow{
			{fileID: 1, folderID: 101, imageIndex: 3, imageCount: 3, prevID: validID(50), firstID: validID(100), lastID: validID(1)},
			{fileID: 2, folderID: 102, imageIndex: 2, imageCount: 2, prevID: validID(200), firstID: validID(200), lastID: validID(2)},
			{fileID: 50, folderID: 101, imageIndex: 2, imageCount: 3, prevID: validID(100), nextID: validID(1), firstID: validID(100), lastID: validID(1)},
			{fileID: 100, folderID: 101, imageIndex: 1, imageCount: 3, nextID: validID(50), firstID: validID(100), lastID: validID(1)},
			{fileID: 200, folderID: 102, imageIndex: 1, imageCount: 2, nextID: validID(2), firstID: validID(200), lastID: validID(2)},
		}
		if got := readFolderIndex(t, tw); !slices.Equal(got, want) {
			t.Errorf("file_folder_index rows:\n got %+v\nwant %+v", got, want)
		}
	})
}

// A folder larger than the chunk is read whole: a chunk ends only at a folder
// boundary.
func TestRebuildFolderIndexDoesNotSplitFolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		tw.exec(t, `INSERT INTO folder_paths (id, path) VALUES (101, 'big'), (102, 'small')`)
		tw.exec(t, `INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'big'), (102, NULL, 102, 'small')`)
		tw.exec(t, `INSERT INTO file_paths (id, path) VALUES (1001, 'b1'), (1002, 'b2'), (1003, 'b3'), (1004, 'b4'), (1005, 'b5'), (1006, 's1')`)
		tw.exec(t, `INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
			VALUES (1, 101, 1001, '1.jpg', 1, 1, 'image/jpeg', 1, 1),
			       (2, 101, 1002, '2.jpg', 1, 1, 'image/jpeg', 1, 1),
			       (3, 101, 1003, '3.jpg', 1, 1, 'image/jpeg', 1, 1),
			       (4, 101, 1004, '4.jpg', 1, 1, 'image/jpeg', 1, 1),
			       (5, 101, 1005, '5.jpg', 1, 1, 'image/jpeg', 1, 1),
			       (6, 102, 1006, 's.jpg', 1, 1, 'image/jpeg', 1, 1)`)

		if err := tw.rebuildFolderIndex(context.Background(), 2); err != nil {
			t.Fatalf("rebuildFolderIndex: %v", err)
		}
		rows := readFolderIndex(t, tw)
		if len(rows) != 6 {
			t.Fatalf("rows = %d, want 6", len(rows))
		}
		for _, r := range rows {
			wantCount := int64(5)
			if r.folderID == 102 {
				wantCount = 1
			}
			if r.imageCount != wantCount {
				t.Errorf("file %d: image_count = %d, want %d", r.fileID, r.imageCount, wantCount)
			}
		}
	})
}

// Small chunks give the same rows as one chunk.
func TestRebuildFolderIndexSmallChunksMatchOneChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		seedFolderIndexFixture(t, tw)
		ctx := context.Background()

		if err := tw.rebuildFolderIndex(ctx, 2); err != nil {
			t.Fatalf("rebuild with chunks of 2: %v", err)
		}
		small := readFolderIndex(t, tw)

		if err := tw.rebuildFolderIndex(ctx, rebuildChunkPairs); err != nil {
			t.Fatalf("rebuild with one chunk: %v", err)
		}
		one := readFolderIndex(t, tw)

		if len(one) != 5 {
			t.Fatalf("one-chunk rows = %d, want 5", len(one))
		}
		if !slices.Equal(small, one) {
			t.Errorf("chunks of 2 gave\n%+v\nwant\n%+v", small, one)
		}
	})
}

func TestRebuildFolderIndexEmptyFiles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		if err := tw.RebuildFolderIndex(context.Background()); err != nil {
			t.Fatalf("RebuildFolderIndex: %v", err)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM file_folder_index"); n != 0 {
			t.Errorf("file_folder_index rows = %d, want 0", n)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'file_folder_index_new'"); n != 0 {
			t.Errorf("file_folder_index_new tables = %d after rebuild, want 0", n)
		}
	})
}

// A canceled rebuild returns context.Canceled and leaves the table it
// replaces in place.
func TestRebuildFolderIndexCanceledKeepsIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		seedFolderIndexFixture(t, tw)
		if err := tw.RebuildFolderIndex(context.Background()); err != nil {
			t.Fatalf("RebuildFolderIndex: %v", err)
		}
		before := readFolderIndex(t, tw)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := tw.RebuildFolderIndex(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("RebuildFolderIndex err = %v, want %v", err, context.Canceled)
		}
		if after := readFolderIndex(t, tw); !slices.Equal(after, before) {
			t.Errorf("rows after canceled rebuild = %+v, want %+v", after, before)
		}
		if n := tw.count(t, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'file_folder_index_new'"); n != 0 {
			t.Errorf("file_folder_index_new tables = %d, want 0", n)
		}
	})
}

// A wait for rows that never commit gives up after stallLimit of no change.
func TestWaitFolderIndexCommittedGivesUpWhenStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var w Writer
		w.index.inflight = 1
		start := time.Now()
		if err := w.waitFolderIndexCommitted(context.Background()); err == nil {
			t.Fatal("wait returned nil with a row still in flight")
		}
		if elapsed := time.Since(start); elapsed < stallLimit {
			t.Errorf("wait gave up after %v, want at least %v without progress", elapsed, stallLimit)
		}
	})
}

func TestWaitFolderIndexCommittedReturnsAtZero(t *testing.T) {
	var w Writer
	if err := w.waitFolderIndexCommitted(context.Background()); err != nil {
		t.Errorf("wait with nothing in flight: %v", err)
	}
}

func TestWaitFolderIndexCommittedCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var w Writer
		w.index.inflight = 5
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := w.waitFolderIndexCommitted(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("wait err = %v, want %v", err, context.Canceled)
		}
	})
}

// indexWrite returns a folder-index write for fileID in generation gen.
func indexWrite(gen, fileID int64) batchedWrite {
	return batchedWrite{FolderIndex: &folderIndexRow{FileID: fileID, FolderID: 1, Generation: gen}}
}

func TestFolderIndexIndexOnlyAndInactive(t *testing.T) {
	cacheWrite := batchedWrite{CacheEntry: &cachelite.HTTPCacheEntry{Key: "k"}}
	tests := []struct {
		name   string
		active bool
		batch  []batchedWrite
		want   bool
	}{
		{"index rows while inactive", false, []batchedWrite{indexWrite(1, 1), indexWrite(1, 2)}, true},
		{"index rows while active", true, []batchedWrite{indexWrite(5, 1)}, false},
		{"mixed with a cache entry", false, []batchedWrite{indexWrite(1, 1), cacheWrite}, false},
		{"empty slot", false, []batchedWrite{{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var x folderIndex
			x.active.Store(tt.active)
			if got := x.indexOnlyAndInactive(tt.batch); got != tt.want {
				t.Errorf("indexOnlyAndInactive = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFolderIndexDropBatch(t *testing.T) {
	cacheWrite := batchedWrite{CacheEntry: &cachelite.HTTPCacheEntry{Key: "x"}}
	tests := []struct {
		name   string
		active bool
		batch  []batchedWrite
		want   bool
	}{
		{"index-only batch while inactive is dropped", false, []batchedWrite{indexWrite(1, 1)}, true},
		{"index-only batch while active is kept", true, []batchedWrite{indexWrite(1, 1)}, false},
		{"mixed batch is kept", false, []batchedWrite{indexWrite(1, 1), cacheWrite}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var x folderIndex
			x.active.Store(tt.active)
			if got := x.dropBatch(tt.batch); got != tt.want {
				t.Errorf("dropBatch = %v, want %v", got, tt.want)
			}
		})
	}
}

// rowsToWrite returns the rows to insert: none unless a rebuild is active and
// its destination table exists, and only rows of the current generation.
func TestFolderIndexRowsToWrite(t *testing.T) {
	const current int64 = 42
	batch := []batchedWrite{indexWrite(current, 10), indexWrite(7, 1), indexWrite(current, 2)}
	tests := []struct {
		name       string
		active     bool
		createDest bool
		want       []int64
	}{
		{"inactive rebuild writes nothing", false, true, nil},
		{"missing destination writes nothing", true, false, nil},
		{"active rebuild writes current generation only", true, true, []int64{10, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tw := newTestWriter(t)
			ctx := context.Background()
			cpc, err := tw.rw.Get()
			if err != nil {
				t.Fatal(err)
			}
			defer tw.rw.Put(cpc)
			tw.exec(t, "DROP TABLE IF EXISTS file_folder_index_new")
			if tt.createDest {
				if err = tableswap.CloneEmpty(ctx, cpc.Conn, "file_folder_index"); err != nil {
					t.Fatal(err)
				}
			}

			var x folderIndex
			x.active.Store(tt.active)
			x.generation = current
			params, err := x.rowsToWrite(ctx, batch, cpc.Queries)
			if err != nil {
				t.Fatalf("rowsToWrite: %v", err)
			}
			var got []int64
			for _, p := range params {
				got = append(got, p.FileID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("file ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFolderIndexCommittedDecrementsInflight(t *testing.T) {
	const current int64 = 100
	const stale int64 = 1
	tests := []struct {
		name     string
		inflight int64
		batch    []batchedWrite
		want     int64
	}{
		{"matching generation decrements", 2, []batchedWrite{indexWrite(current, 1), indexWrite(current, 2)}, 0},
		{"stale generation leaves the count", 1, []batchedWrite{indexWrite(stale, 1)}, 1},
		{"count never goes negative", 0, []batchedWrite{indexWrite(current, 1)}, 0},
		{"only matching rows decrement", 3, []batchedWrite{indexWrite(stale, 1), indexWrite(current, 2), indexWrite(current, 3)}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var x folderIndex
			x.generation = current
			x.inflight = tt.inflight
			x.committed(tt.batch)
			if got := x.inflight; got != tt.want {
				t.Errorf("inflight after committed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFolderIndexBumpGeneration(t *testing.T) {
	var x folderIndex
	a := x.bumpGeneration()
	b := x.bumpGeneration()
	if a == 0 || b == 0 {
		t.Errorf("generations = %d, %d; want non-zero", a, b)
	}
	if a == b {
		t.Errorf("generations are equal: %d", a)
	}
	if got := x.generation; got != b {
		t.Errorf("stored generation = %d, want %d", got, b)
	}
}

// A rebuild replaces tables left by a crash: the destination is recreated
// and the stale table is dropped.
func TestRebuildFolderIndexReplacesLeftoverTables(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		tw.exec(t, "CREATE TABLE file_folder_index_new (x INTEGER)")
		tw.exec(t, "CREATE TABLE file_folder_index_to_be_dropped (x INTEGER)")
		seedFolderIndexFixture(t, tw)

		if err := tw.RebuildFolderIndex(context.Background()); err != nil {
			t.Fatalf("RebuildFolderIndex: %v", err)
		}
		for _, name := range []string{"file_folder_index_new", "file_folder_index_to_be_dropped"} {
			if n := tw.count(t, "SELECT COUNT(*) FROM sqlite_master WHERE name = '"+name+"'"); n != 0 {
				t.Errorf("%s tables = %d after rebuild, want 0", name, n)
			}
		}
		if n := len(readFolderIndex(t, tw)); n != 5 {
			t.Errorf("file_folder_index rows = %d, want 5", n)
		}
	})
}

// The rebuilt table has an index on folder_id, which the folder views use.
func TestRebuildFolderIndexIndexesFolderID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tw := newTestWriter(t)
		seedFolderIndexFixture(t, tw)
		if err := tw.RebuildFolderIndex(context.Background()); err != nil {
			t.Fatalf("RebuildFolderIndex: %v", err)
		}
		n := tw.count(t, `SELECT COUNT(*) FROM sqlite_master i, pragma_index_info(i.name) c
			WHERE i.type = 'index' AND i.tbl_name = 'file_folder_index' AND c.name = 'folder_id'`)
		if n == 0 {
			t.Error("file_folder_index has no index on folder_id")
		}
	})
}

// TestBumpGenerationClearsInflight checks that a new rebuild does not wait
// for rows an earlier rebuild left in flight: they are skipped, not counted.
func TestBumpGenerationClearsInflight(t *testing.T) {
	var x folderIndex
	x.inflight = 3
	x.bumpGeneration()
	if n := x.inflight; n != 0 {
		t.Errorf("inflight = %d after a new generation, want 0", n)
	}
}
