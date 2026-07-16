package files

import (
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractXMPGPSStrings_CanonSidecar(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "imagemeta", "xmp", "test", "1.xmp"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	lat, lon, ok := extractXMPGPSStrings(data)
	if !ok {
		t.Fatal("expected GPS strings from XMP")
	}
	if lat != "11,57.1312N" {
		t.Errorf("lat = %q, want %q", lat, "11,57.1312N")
	}
	if lon != "120,11.573E" {
		t.Errorf("lon = %q, want %q", lon, "120,11.573E")
	}
}

func TestExtractXMPGPSStrings_EmbeddedElementForm(t *testing.T) {
	const embeddedXMP = `<rdf:RDF xmlns:exif="http://ns.adobe.com/exif/1.0/">
<exif:GPSLatitude>26,34.951N</exif:GPSLatitude>
<exif:GPSLongitude>80,12.014W</exif:GPSLongitude>
</rdf:RDF>`

	lat, lon, ok := extractXMPGPSStrings([]byte(embeddedXMP))
	if !ok {
		t.Fatal("expected GPS strings from element-form XMP")
	}
	if lat != "26,34.951N" {
		t.Errorf("lat = %q, want %q", lat, "26,34.951N")
	}
	if lon != "80,12.014W" {
		t.Errorf("lon = %q, want %q", lon, "80,12.014W")
	}
}

func TestApplyExifGPSFromXMP_SetsValidCoords(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "imagemeta", "xmp", "test", "1.xmp"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	f := &File{Path: "xmpgps.jpg"}
	populateFileXMPFromRaw(f, data)
	applyExifGPSFromXMP(f)

	if !f.Exif.Latitude.Valid || !f.Exif.Longitude.Valid {
		t.Fatal("expected valid GPS from XMP fallback")
	}

	const wantLat = 11.952186666666666
	const wantLon = 120.19288333333333
	if math.Abs(f.Exif.Latitude.Float64-wantLat) > 1e-4 {
		t.Errorf("latitude = %v, want ≈ %v", f.Exif.Latitude.Float64, wantLat)
	}
	if math.Abs(f.Exif.Longitude.Float64-wantLon) > 1e-4 {
		t.Errorf("longitude = %v, want ≈ %v", f.Exif.Longitude.Float64, wantLon)
	}
}

func TestApplyExifGPSFromXMP_SkipsWhenExifAlreadyValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "imagemeta", "xmp", "test", "1.xmp"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	f := &File{Path: "xmpgps.jpg"}
	f.Exif.Latitude = sql.NullFloat64{Float64: 1.0, Valid: true}
	f.Exif.Longitude = sql.NullFloat64{Float64: 2.0, Valid: true}

	populateFileXMPFromRaw(f, data)
	applyExifGPSFromXMP(f)

	if f.Exif.Latitude.Float64 != 1.0 || f.Exif.Longitude.Float64 != 2.0 {
		t.Errorf("coordinates changed unexpectedly: lat=%v lon=%v", f.Exif.Latitude.Float64, f.Exif.Longitude.Float64)
	}
}
