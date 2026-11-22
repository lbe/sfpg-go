package files

import (
	"bytes"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
)

func TestFile_GobRoundTrip(t *testing.T) {
	t.Run("with thumbnail", func(t *testing.T) {
		thumb := bytes.NewBuffer([]byte{1, 2, 3, 4})
		original := File{
			ImagesDir: "/tmp/Images",
			Path:      "foo/bar.jpg",
			File:      gallerydb.File{ID: 42, Filename: "bar.jpg"},
			Thumbnail: thumb,
			Metadata: metadata.Info{
				Exif: metadata.Exif{CameraMake: "make"},
				IPTC: metadata.IPTC{Title: "title"},
				XMP: metadata.XMP{
					Packet:       []byte("<xmp/>"),
					GPSLatitude:  "26,34.951N",
					GPSLongitude: "80,12.014W",
				},
			},
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

		if decoded.ImagesDir != original.ImagesDir ||
			decoded.Path != original.Path ||
			decoded.File.ID != original.File.ID ||
			decoded.File.Filename != original.File.Filename ||
			decoded.Metadata.Exif.CameraMake != original.Metadata.Exif.CameraMake ||
			decoded.Metadata.IPTC.Title != original.Metadata.IPTC.Title {
			t.Errorf("decoded File mismatch: got %+v, want %+v", decoded, original)
		}
		if !bytes.Equal(decoded.Metadata.XMP.Packet, original.Metadata.XMP.Packet) {
			t.Errorf("decoded XMP packet mismatch: got %q, want %q", decoded.Metadata.XMP.Packet, original.Metadata.XMP.Packet)
		}
		if decoded.Metadata.XMP.GPSLatitude != original.Metadata.XMP.GPSLatitude {
			t.Errorf("decoded XMP GPSLatitude: got %q, want %q", decoded.Metadata.XMP.GPSLatitude, original.Metadata.XMP.GPSLatitude)
		}
		if decoded.Metadata.XMP.GPSLongitude != original.Metadata.XMP.GPSLongitude {
			t.Errorf("decoded XMP GPSLongitude: got %q, want %q", decoded.Metadata.XMP.GPSLongitude, original.Metadata.XMP.GPSLongitude)
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
