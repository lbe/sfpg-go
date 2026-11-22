package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/server/files"
)

// writeInTx runs fn in one transaction on tw's RW pool, with queries and an
// importer bound to it, and commits unless fn fails. before, if set, runs
// first in the same transaction.
func writeInTx(t *testing.T, tw *testWriter, before string,
	fn func(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries) error) error {
	t.Helper()
	ctx := context.Background()
	cpc, err := tw.rw.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.rw.Put(cpc)
	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != "" {
		if _, err := tx.ExecContext(ctx, before); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	q := cpc.Queries.WithTx(tx)
	if err := fn(ctx, &gallerylib.Importer{Q: q}, q); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Fatal(rbErr)
		}
		return err
	}
	return tx.Commit()
}

// writeFileInTx writes f in its own committed transaction.
func writeFileInTx(t *testing.T, tw *testWriter, f *files.File) {
	t.Helper()
	err := writeInTx(t, tw, "", func(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries) error {
		return writeFile(ctx, imp, q, f)
	})
	if err != nil {
		t.Fatalf("writeFile %s: %v", f.Path, err)
	}
}

// withReadQueries runs fn with the queries of a read-only connection.
func withReadQueries(t *testing.T, tw *testWriter, fn func(q *gallerydb.CustomQueries)) {
	t.Helper()
	cpc, err := tw.ro.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.ro.Put(cpc)
	fn(cpc.Queries)
}

// fileIDByPath returns the id of the files row for path.
func fileIDByPath(t *testing.T, tw *testWriter, path string) int64 {
	t.Helper()
	var id int64
	tw.query(t, `SELECT f.id FROM files f JOIN file_paths p ON p.id = f.path_id WHERE p.path = '`+path+`'`, &id)
	return id
}

func newMetadataFile(m metadata.Info) *files.File {
	return &files.File{
		Path: "a/photo.jpg",
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: 1, Valid: true},
			SizeBytes: sql.NullInt64{Int64: 2, Valid: true},
			MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		},
		Metadata: m,
	}
}

func fullMetadata() metadata.Info {
	return metadata.Info{
		Exif: metadata.Exif{CameraMake: "canon", ISO: 100, Latitude: 1.5, Longitude: 2.5,
			Altitude: -3, HasAltitude: true, CaptureDate: time.Unix(1700000000, 0)},
		IPTC: metadata.IPTC{Title: "title", Keywords: []string{"b", "a"}},
		XMP:  metadata.XMP{Packet: []byte("<x/>"), GPSLatitude: "1.5", GPSLongitude: "2.5"},
	}
}

// checkFullMetadata checks that fullMetadata is stored for file id.
func checkFullMetadata(t *testing.T, tw *testWriter, id int64) {
	t.Helper()
	ctx := context.Background()
	withReadQueries(t, tw, func(q *gallerydb.CustomQueries) {
		exif, err := q.GetExifByFile(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if exif.CameraMake.String != "canon" || exif.Iso.Int64 != 100 || exif.Latitude.Float64 != 1.5 ||
			exif.Longitude.Float64 != 2.5 || !exif.Altitude.Valid || exif.Altitude.Float64 != -3 ||
			exif.CaptureDate.Int64 != 1700000000 || exif.LensModel.Valid {
			t.Errorf("exif row = %+v", exif)
		}
		iptc, err := q.GetIPTCByFile(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if iptc.Title.String != "title" || iptc.Keywords.String != `["b","a"]` || iptc.Creator.Valid {
			t.Errorf("iptc row = %+v", iptc)
		}
		keywords, err := q.GetIPTCKeywords(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(keywords) != 2 || keywords[0].Keyword != "b" || keywords[1].Keyword != "a" {
			t.Errorf("keyword rows = %+v, want b then a", keywords)
		}
		raw, err := q.GetXMPRaw(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if raw.RawXml.String != "<x/>" {
			t.Errorf("xmp raw = %q", raw.RawXml.String)
		}
		props, err := q.GetXMPPropertiesByFile(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(props) != 2 || props[0].Property != "GPSLatitude" || props[0].Value.String != "1.5" ||
			props[1].Property != "GPSLongitude" || props[1].Value.String != "2.5" {
			t.Errorf("xmp property rows = %+v", props)
		}
	})
}

func TestWriteFileReplacesMetadata(t *testing.T) {
	tw := newTestWriter(t)
	ctx := context.Background()

	writeFileInTx(t, tw, newMetadataFile(fullMetadata()))
	id := fileIDByPath(t, tw, "a/photo.jpg")
	checkFullMetadata(t, tw, id)

	// A rescan that finds less metadata leaves no stale rows.
	writeFileInTx(t, tw, newMetadataFile(metadata.Info{Exif: metadata.Exif{CameraModel: "R5"}}))
	withReadQueries(t, tw, func(q *gallerydb.CustomQueries) {
		exif, err := q.GetExifByFile(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if exif.CameraModel.String != "R5" || exif.CameraMake.Valid || exif.Latitude.Valid || exif.Altitude.Valid {
			t.Errorf("exif row after rescan = %+v", exif)
		}
		if _, err := q.GetIPTCByFile(ctx, id); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("iptc row after rescan: err = %v, want %v", err, sql.ErrNoRows)
		}
		if _, err := q.GetXMPRaw(ctx, id); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("xmp raw after rescan: err = %v, want %v", err, sql.ErrNoRows)
		}
		if keywords, kerr := q.GetIPTCKeywords(ctx, id); kerr != nil || len(keywords) != 0 {
			t.Errorf("keyword rows after rescan = %+v, err %v; want none", keywords, kerr)
		}
		if props, perr := q.GetXMPPropertiesByFile(ctx, id); perr != nil || len(props) != 0 {
			t.Errorf("xmp property rows after rescan = %+v, err %v; want none", props, perr)
		}
	})

	// No metadata at all removes the EXIF row too.
	writeFileInTx(t, tw, newMetadataFile(metadata.Info{}))
	withReadQueries(t, tw, func(q *gallerydb.CustomQueries) {
		if _, err := q.GetExifByFile(ctx, id); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("exif row after empty rescan: err = %v, want %v", err, sql.ErrNoRows)
		}
	})
}

// A metadata write that fails part way rolls back with the transaction,
// leaving the previous metadata in place.
func TestWriteFileMetadataFailureRollsBack(t *testing.T) {
	tw := newTestWriter(t)
	writeFileInTx(t, tw, newMetadataFile(fullMetadata()))
	id := fileIDByPath(t, tw, "a/photo.jpg")

	// Without xmp_properties, the rescan fails after deleting the EXIF, IPTC
	// and keyword rows in the same transaction.
	err := writeInTx(t, tw, "DROP TABLE xmp_properties",
		func(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries) error {
			return writeFile(ctx, imp, q, newMetadataFile(metadata.Info{}))
		})
	if err == nil {
		t.Fatal("writeFile succeeded without the xmp_properties table, want an error")
	}
	checkFullMetadata(t, tw, id)
}

// An existing thumbnail is kept when the file is imported again.
func TestWriteFileKeepsExistingThumbnail(t *testing.T) {
	tw := newTestWriter(t)
	const path = "album/existing.jpg"
	writeFileInTx(t, tw, testFile(path, []byte("first")))

	again := testFile(path, []byte("second"))
	writeFileInTx(t, tw, again)

	var got []byte
	tw.query(t, `SELECT tb.data FROM thumbnails t JOIN thumbs.thumbnail_blobs tb ON tb.thumbnail_id = t.id
		JOIN files f ON f.id = t.file_id JOIN file_paths p ON p.id = f.path_id WHERE p.path = '`+path+`'`, &got)
	if !bytes.Equal(got, []byte("first")) {
		t.Errorf("thumbnail = %q, want the first import's %q", got, "first")
	}
}

// A folder's tile is the first file with a thumbnail written in it. A file
// without a thumbnail sets no tile.
func TestWriteFileSetsFolderTile(t *testing.T) {
	tileOf := func(t *testing.T, tw *testWriter, folderPath string) sql.NullInt64 {
		t.Helper()
		var tile sql.NullInt64
		tw.query(t, `SELECT fo.tile_id FROM folders fo JOIN folder_paths fp ON fp.id = fo.path_id
			WHERE fp.path = '`+folderPath+`'`, &tile)
		return tile
	}

	t.Run("file with thumbnail", func(t *testing.T) {
		tw := newTestWriter(t)
		writeFileInTx(t, tw, testFile("album/tiled.jpg", []byte("thumb")))
		id := fileIDByPath(t, tw, "album/tiled.jpg")
		if tile := tileOf(t, tw, "album"); !tile.Valid || tile.Int64 != id {
			t.Errorf("album tile = %+v, want file %d", tile, id)
		}
	})

	t.Run("file without thumbnail", func(t *testing.T) {
		tw := newTestWriter(t)
		writeFileInTx(t, tw, testFile("plain/nothumb.jpg", nil))
		if tile := tileOf(t, tw, "plain"); tile.Valid {
			t.Errorf("plain tile = %d, want none", tile.Int64)
		}
	})
}

// A file recorded as invalid loses that row when it imports successfully,
// so the next run does not skip it.
func TestWriteFileClearsStaleInvalidFile(t *testing.T) {
	tw := newTestWriter(t)
	const path = "d4/recover.jpg"
	err := writeInTx(t, tw, "", func(ctx context.Context, imp *gallerylib.Importer, q *gallerydb.CustomQueries) error {
		return writeInvalidFile(ctx, imp, q, &files.InvalidFile{Path: path, Mtime: 1600000000, Size: 512, Reason: "prior corruption"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := tw.count(t, `SELECT COUNT(*) FROM invalid_files WHERE path = '`+path+`'`); n != 1 {
		t.Fatalf("precondition: invalid_files rows = %d, want 1", n)
	}

	writeFileInTx(t, tw, testFile(path, []byte("t")))
	if n := tw.count(t, `SELECT COUNT(*) FROM invalid_files WHERE path = '`+path+`'`); n != 0 {
		t.Errorf("invalid_files rows after import = %d, want 0", n)
	}
}

// A canceled context fails the write, and nothing is stored.
func TestWriteFileCanceledContext(t *testing.T) {
	tw := newTestWriter(t)
	cpc, err := tw.rw.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer tw.rw.Put(cpc)
	tx, err := cpc.Conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	q := cpc.Queries.WithTx(tx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = writeFile(ctx, &gallerylib.Importer{Q: q}, q, testFile("album/cancelled.jpg", []byte("t")))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("writeFile err = %v, want %v", err, context.Canceled)
	}
	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatal(rbErr)
	}
	if n := tw.count(t, "SELECT COUNT(*) FROM files WHERE filename = 'cancelled.jpg'"); n != 0 {
		t.Errorf("files rows = %d, want 0", n)
	}
}
