package files

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestFile_GobRoundTrip(t *testing.T) {
	t.Run("with thumbnail", func(t *testing.T) {
		thumb := bytes.NewBuffer([]byte{1, 2, 3, 4})
		original := File{
			Ok:        true,
			Exists:    true,
			ImagesDir: "/tmp/Images",
			Path:      "foo/bar.jpg",
			File:      gallerydb.File{ID: 42, Filename: "bar.jpg"},
			Thumbnail: thumb,
			Exif:      gallerydb.UpsertExifParams{CameraMake: sql.NullString{String: "make", Valid: true}},
			Itpc:      gallerydb.UpsertIPTCParams{Title: sql.NullString{String: "title", Valid: true}},
			XmpProp: gallerydb.UpsertXMPPropertyParams{
				Namespace: "ns",
				Property:  "prop",
				Value:     sql.NullString{String: "value", Valid: true},
			},
			XmpRaw:              gallerydb.UpsertXMPRawParams{RawXml: sql.NullString{String: "<xmp/>", Valid: true}},
			HasValidJpegMarkers: true,
		}

		encoded, err := original.GobEncode()
		if err != nil {
			t.Fatalf("GobEncode: %v", err)
		}

		// Original thumbnail must not be mutated.
		if !bytes.Equal(thumb.Bytes(), []byte{1, 2, 3, 4}) {
			t.Errorf("GobEncode mutated original thumbnail: %v", thumb.Bytes())
		}

		var decoded File
		if err := decoded.GobDecode(encoded); err != nil {
			t.Fatalf("GobDecode: %v", err)
		}

		if decoded.Ok != original.Ok ||
			decoded.Exists != original.Exists ||
			decoded.ImagesDir != original.ImagesDir ||
			decoded.Path != original.Path ||
			decoded.File.ID != original.File.ID ||
			decoded.File.Filename != original.File.Filename ||
			decoded.Exif.CameraMake.String != original.Exif.CameraMake.String ||
			decoded.Itpc.Title.String != original.Itpc.Title.String ||
			decoded.XmpProp.Namespace != original.XmpProp.Namespace ||
			decoded.XmpProp.Property != original.XmpProp.Property ||
			decoded.XmpProp.Value.String != original.XmpProp.Value.String ||
			decoded.XmpRaw.RawXml.String != original.XmpRaw.RawXml.String ||
			decoded.HasValidJpegMarkers != original.HasValidJpegMarkers {
			t.Errorf("decoded File mismatch: got %+v, want %+v", decoded, original)
		}
		if decoded.Thumbnail == nil {
			t.Fatal("expected decoded thumbnail to be non-nil")
		}
		if !bytes.Equal(decoded.Thumbnail.Bytes(), []byte{1, 2, 3, 4}) {
			t.Errorf("decoded thumbnail mismatch: got %v", decoded.Thumbnail.Bytes())
		}
	})

	t.Run("without thumbnail", func(t *testing.T) {
		original := File{
			Ok:        true,
			Path:      "foo/bar.jpg",
			File:      gallerydb.File{ID: 7},
			Thumbnail: nil,
		}

		encoded, err := original.GobEncode()
		if err != nil {
			t.Fatalf("GobEncode: %v", err)
		}

		var decoded File
		if err := decoded.GobDecode(encoded); err != nil {
			t.Fatalf("GobDecode: %v", err)
		}

		if decoded.Thumbnail != nil {
			t.Errorf("expected decoded thumbnail nil, got %v", decoded.Thumbnail)
		}
	})

	t.Run("invalid data", func(t *testing.T) {
		var f File
		if err := f.GobDecode([]byte("not gob data")); err == nil {
			t.Fatal("expected error for invalid gob data")
		}
	})
}
