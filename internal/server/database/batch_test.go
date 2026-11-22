package database

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/server/files"
)

// ---------------------------------------------------------------------------
// Helpers: construct fully-populated test objects
// ---------------------------------------------------------------------------

func fullyPopulatedGallerydbFile() gallerydb.File {
	return gallerydb.File{
		ID:        42,
		FolderID:  sql.NullInt64{Int64: 7, Valid: true},
		PathID:    13,
		Filename:  "sunset.jpg",
		SizeBytes: sql.NullInt64{Int64: 3_145_728, Valid: true},
		Mtime:     sql.NullInt64{Int64: 1_700_000_000, Valid: true},
		Md5:       sql.NullString{String: "d41d8cd98f00b204e9800998ecf8427e", Valid: true},
		Phash:     sql.NullInt64{Int64: 0xABCD1234, Valid: true},
		MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		Width:     sql.NullInt64{Int64: 4000, Valid: true},
		Height:    sql.NullInt64{Int64: 3000, Valid: true},
		CreatedAt: int64(1_700_000_001),
		UpdatedAt: int64(1_700_000_002),
	}
}

func fullyPopulatedFilesFile() *files.File {
	thumbContent := make([]byte, 50*1024)
	for i := range thumbContent {
		thumbContent[i] = byte(i % 256)
	}

	return &files.File{
		ImagesDir: "/photos/2024",
		Path:      "/photos/2024/sunset.jpg",
		File:      fullyPopulatedGallerydbFile(),
		Thumbnail: bytes.NewBuffer(thumbContent),
		Metadata: metadata.Info{
			Exif: metadata.Exif{
				CameraMake:   "canon",
				CameraModel:  "EOS R5",
				LensModel:    "RF 24-105mm",
				FocalLength:  "85mm",
				Aperture:     "f/2.8",
				ShutterSpeed: "1/500",
				ISO:          400,
				Orientation:  1,
				CaptureDate:  time.Unix(1_700_000_000, 0).UTC(),
				Latitude:     37.7749,
				Longitude:    -122.4194,
				Altitude:     10.5,
				HasAltitude:  true,
			},
			IPTC: metadata.IPTC{
				Title:       "Sunset over Pacific",
				Description: "A beautiful sunset",
				Keywords:    []string{"sunset", "pacific", "ocean"},
				Creator:     "Photographer",
				Copyright:   "© 2024",
				Credit:      "Stock",
				Source:      "Original",
				Created:     time.Unix(1_700_000_000, 0).UTC(),
			},
			XMP: metadata.XMP{
				Packet:       []byte("<xmp>data</xmp>"),
				GPSLatitude:  "26,34.951N",
				GPSLongitude: "80,12.014W",
			},
		},
	}
}

func fullyPopulatedCacheEntry() *cachelite.HTTPCacheEntry {
	return &cachelite.HTTPCacheEntry{
		ID:            99,
		Key:           "sha256:abcdef1234567890",
		Method:        "GET",
		Path:          "/gallery/42",
		QueryString:   sql.NullString{String: "page=1", Valid: true},
		Status:        200,
		ContentType:   sql.NullString{String: "text/html; charset=utf-8", Valid: true},
		CacheControl:  sql.NullString{String: "max-age=3600", Valid: true},
		ETag:          sql.NullString{String: "\"etag-42\"", Valid: true},
		LastModified:  sql.NullString{String: "Mon, 01 Jan 2024 00:00:00 GMT", Valid: true},
		Vary:          sql.NullString{String: "Accept-Encoding", Valid: true},
		Body:          []byte("<html>gallery page</html>"),
		ContentLength: sql.NullInt64{Int64: 1234, Valid: true},
		CreatedAt:     1_700_000_003,
		ExpiresAt:     sql.NullInt64{Int64: 1_700_003_603, Valid: true},
	}
}

// ---------------------------------------------------------------------------
// Field-level assertion helpers
// ---------------------------------------------------------------------------

func assertGallerydbFileEqual(t *testing.T, context string, a, b gallerydb.File) {
	t.Helper()
	if a.ID != b.ID {
		t.Errorf("%s: ID: got %d, want %d", context, b.ID, a.ID)
	}
	if a.FolderID != b.FolderID {
		t.Errorf("%s: FolderID: got %+v, want %+v", context, b.FolderID, a.FolderID)
	}
	if a.PathID != b.PathID {
		t.Errorf("%s: PathID: got %d, want %d", context, b.PathID, a.PathID)
	}
	if a.Filename != b.Filename {
		t.Errorf("%s: Filename: got %q, want %q", context, b.Filename, a.Filename)
	}
	if a.SizeBytes != b.SizeBytes {
		t.Errorf("%s: SizeBytes: got %+v, want %+v", context, b.SizeBytes, a.SizeBytes)
	}
	if a.Mtime != b.Mtime {
		t.Errorf("%s: Mtime: got %+v, want %+v", context, b.Mtime, a.Mtime)
	}
	if a.Md5 != b.Md5 {
		t.Errorf("%s: Md5: got %+v, want %+v", context, b.Md5, a.Md5)
	}
	if a.Phash != b.Phash {
		t.Errorf("%s: Phash: got %+v, want %+v", context, b.Phash, a.Phash)
	}
	if a.MimeType != b.MimeType {
		t.Errorf("%s: MimeType: got %+v, want %+v", context, b.MimeType, a.MimeType)
	}
	if a.Width != b.Width {
		t.Errorf("%s: Width: got %+v, want %+v", context, b.Width, a.Width)
	}
	if a.Height != b.Height {
		t.Errorf("%s: Height: got %+v, want %+v", context, b.Height, a.Height)
	}
	assertInterfaceInt64(t, context+".CreatedAt", b.CreatedAt, a.CreatedAt)
	assertInterfaceInt64(t, context+".UpdatedAt", b.UpdatedAt, a.UpdatedAt)
}

func assertFilesFileEqual(t *testing.T, context string, a, b *files.File) {
	t.Helper()
	if a.ImagesDir != b.ImagesDir {
		t.Errorf("%s: ImagesDir: got %q, want %q", context, b.ImagesDir, a.ImagesDir)
	}
	if a.Path != b.Path {
		t.Errorf("%s: Path: got %q, want %q", context, b.Path, a.Path)
	}
	switch {
	case a.Thumbnail == nil && b.Thumbnail == nil:
	case a.Thumbnail == nil && b.Thumbnail != nil:
		t.Errorf("%s: Thumbnail: original nil, decoded has %d bytes", context, b.Thumbnail.Len())
	case a.Thumbnail != nil && b.Thumbnail == nil:
		t.Errorf("%s: Thumbnail: original has %d bytes, decoded nil", context, a.Thumbnail.Len())
	case a.Thumbnail != nil && b.Thumbnail != nil:
		if a.Thumbnail.Len() != b.Thumbnail.Len() {
			t.Errorf("%s: Thumbnail.Len: got %d, want %d", context, b.Thumbnail.Len(), a.Thumbnail.Len())
		}
		if !bytes.Equal(a.Thumbnail.Bytes(), b.Thumbnail.Bytes()) {
			aBytes := a.Thumbnail.Bytes()
			bBytes := b.Thumbnail.Bytes()
			minLen := min(len(bBytes), len(aBytes))
			for i := range minLen {
				if aBytes[i] != bBytes[i] {
					t.Errorf("%s: Thumbnail content mismatch at byte %d: got 0x%02x, want 0x%02x", context, i, bBytes[i], aBytes[i])
					break
				}
			}
		}
	}
	assertGallerydbFileEqual(t, context+".File", a.File, b.File)
	assertMetadataInfoEqual(t, context+".Metadata", a.Metadata, b.Metadata)
}

func assertMetadataInfoEqual(t *testing.T, context string, a, b metadata.Info) {
	t.Helper()
	if a.Exif.CameraMake != b.Exif.CameraMake {
		t.Errorf("%s: Exif.CameraMake: got %q, want %q", context, b.Exif.CameraMake, a.Exif.CameraMake)
	}
	if a.Exif.CameraModel != b.Exif.CameraModel {
		t.Errorf("%s: Exif.CameraModel: got %q, want %q", context, b.Exif.CameraModel, a.Exif.CameraModel)
	}
	if a.Exif.LensModel != b.Exif.LensModel {
		t.Errorf("%s: Exif.LensModel: got %q, want %q", context, b.Exif.LensModel, a.Exif.LensModel)
	}
	if a.Exif.FocalLength != b.Exif.FocalLength {
		t.Errorf("%s: Exif.FocalLength: got %q, want %q", context, b.Exif.FocalLength, a.Exif.FocalLength)
	}
	if a.Exif.Aperture != b.Exif.Aperture {
		t.Errorf("%s: Exif.Aperture: got %q, want %q", context, b.Exif.Aperture, a.Exif.Aperture)
	}
	if a.Exif.ShutterSpeed != b.Exif.ShutterSpeed {
		t.Errorf("%s: Exif.ShutterSpeed: got %q, want %q", context, b.Exif.ShutterSpeed, a.Exif.ShutterSpeed)
	}
	if a.Exif.ISO != b.Exif.ISO {
		t.Errorf("%s: Exif.ISO: got %d, want %d", context, b.Exif.ISO, a.Exif.ISO)
	}
	if a.Exif.Orientation != b.Exif.Orientation {
		t.Errorf("%s: Exif.Orientation: got %d, want %d", context, b.Exif.Orientation, a.Exif.Orientation)
	}
	if !a.Exif.CaptureDate.Equal(b.Exif.CaptureDate) {
		t.Errorf("%s: Exif.CaptureDate: got %v, want %v", context, b.Exif.CaptureDate, a.Exif.CaptureDate)
	}
	if a.Exif.Latitude != b.Exif.Latitude {
		t.Errorf("%s: Exif.Latitude: got %v, want %v", context, b.Exif.Latitude, a.Exif.Latitude)
	}
	if a.Exif.Longitude != b.Exif.Longitude {
		t.Errorf("%s: Exif.Longitude: got %v, want %v", context, b.Exif.Longitude, a.Exif.Longitude)
	}
	if a.Exif.Altitude != b.Exif.Altitude {
		t.Errorf("%s: Exif.Altitude: got %v, want %v", context, b.Exif.Altitude, a.Exif.Altitude)
	}
	if a.Exif.HasAltitude != b.Exif.HasAltitude {
		t.Errorf("%s: Exif.HasAltitude: got %v, want %v", context, b.Exif.HasAltitude, a.Exif.HasAltitude)
	}
	if a.IPTC.Title != b.IPTC.Title {
		t.Errorf("%s: IPTC.Title: got %q, want %q", context, b.IPTC.Title, a.IPTC.Title)
	}
	if a.IPTC.Description != b.IPTC.Description {
		t.Errorf("%s: IPTC.Description: got %q, want %q", context, b.IPTC.Description, a.IPTC.Description)
	}
	if !slices.Equal(a.IPTC.Keywords, b.IPTC.Keywords) {
		t.Errorf("%s: IPTC.Keywords: got %q, want %q", context, b.IPTC.Keywords, a.IPTC.Keywords)
	}
	if a.IPTC.Creator != b.IPTC.Creator {
		t.Errorf("%s: IPTC.Creator: got %q, want %q", context, b.IPTC.Creator, a.IPTC.Creator)
	}
	if a.IPTC.Copyright != b.IPTC.Copyright {
		t.Errorf("%s: IPTC.Copyright: got %q, want %q", context, b.IPTC.Copyright, a.IPTC.Copyright)
	}
	if a.IPTC.Credit != b.IPTC.Credit {
		t.Errorf("%s: IPTC.Credit: got %q, want %q", context, b.IPTC.Credit, a.IPTC.Credit)
	}
	if a.IPTC.Source != b.IPTC.Source {
		t.Errorf("%s: IPTC.Source: got %q, want %q", context, b.IPTC.Source, a.IPTC.Source)
	}
	if !a.IPTC.Created.Equal(b.IPTC.Created) {
		t.Errorf("%s: IPTC.Created: got %v, want %v", context, b.IPTC.Created, a.IPTC.Created)
	}
	if !bytes.Equal(a.XMP.Packet, b.XMP.Packet) {
		t.Errorf("%s: XMP.Packet: got %q, want %q", context, b.XMP.Packet, a.XMP.Packet)
	}
	if a.XMP.GPSLatitude != b.XMP.GPSLatitude {
		t.Errorf("%s: XMP.GPSLatitude: got %q, want %q", context, b.XMP.GPSLatitude, a.XMP.GPSLatitude)
	}
	if a.XMP.GPSLongitude != b.XMP.GPSLongitude {
		t.Errorf("%s: XMP.GPSLongitude: got %q, want %q", context, b.XMP.GPSLongitude, a.XMP.GPSLongitude)
	}
}

func assertHTTPCacheEntryEqual(t *testing.T, context string, a, b *cachelite.HTTPCacheEntry) {
	t.Helper()
	if a.ID != b.ID {
		t.Errorf("%s: ID: got %d, want %d", context, b.ID, a.ID)
	}
	if a.Key != b.Key {
		t.Errorf("%s: Key: got %q, want %q", context, b.Key, a.Key)
	}
	if a.Method != b.Method {
		t.Errorf("%s: Method: got %q, want %q", context, b.Method, a.Method)
	}
	if a.Path != b.Path {
		t.Errorf("%s: Path: got %q, want %q", context, b.Path, a.Path)
	}
	if a.QueryString != b.QueryString {
		t.Errorf("%s: QueryString: got %+v, want %+v", context, b.QueryString, a.QueryString)
	}

	if a.Status != b.Status {
		t.Errorf("%s: Status: got %d, want %d", context, b.Status, a.Status)
	}
	if a.ContentType != b.ContentType {
		t.Errorf("%s: ContentType: got %+v, want %+v", context, b.ContentType, a.ContentType)
	}

	if a.CacheControl != b.CacheControl {
		t.Errorf("%s: CacheControl: got %+v, want %+v", context, b.CacheControl, a.CacheControl)
	}
	if a.ETag != b.ETag {
		t.Errorf("%s: ETag: got %+v, want %+v", context, b.ETag, a.ETag)
	}
	if a.LastModified != b.LastModified {
		t.Errorf("%s: LastModified: got %+v, want %+v", context, b.LastModified, a.LastModified)
	}
	if a.Vary != b.Vary {
		t.Errorf("%s: Vary: got %+v, want %+v", context, b.Vary, a.Vary)
	}
	if !bytes.Equal(a.Body, b.Body) {
		t.Errorf("%s: Body: got %d bytes, want %d bytes", context, len(b.Body), len(a.Body))
	}
	if a.ContentLength != b.ContentLength {
		t.Errorf("%s: ContentLength: got %+v, want %+v", context, b.ContentLength, a.ContentLength)
	}
	if a.CreatedAt != b.CreatedAt {
		t.Errorf("%s: CreatedAt: got %d, want %d", context, b.CreatedAt, a.CreatedAt)
	}
	if a.ExpiresAt != b.ExpiresAt {
		t.Errorf("%s: ExpiresAt: got %+v, want %+v", context, b.ExpiresAt, a.ExpiresAt)
	}
}

func assertInterfaceInt64(t *testing.T, fieldName string, got, want any) {
	t.Helper()
	gotInt64, ok := got.(int64)
	if !ok {
		t.Errorf("%s: wrong concrete type: got %T (%+v), want int64", fieldName, got, got)
		return
	}
	wantInt64, ok := want.(int64)
	if !ok {
		t.Fatalf("%s: expected value is not int64: %T (%+v)", fieldName, want, want)
	}
	if gotInt64 != wantInt64 {
		t.Errorf("%s: got %d, want %d", fieldName, gotInt64, wantInt64)
	}
}

// ---------------------------------------------------------------------------
// Phase A: Raw type validation — gob round-trip for individual types
// ---------------------------------------------------------------------------

// gobRoundTripValue encodes in with gob and decodes it into a fresh T.
func gobRoundTripValue[T any](t *testing.T, in T) T {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	var out T
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("gob decode: %v", err)
	}
	return out
}

func TestGob_SqlNullTypes(t *testing.T) {
	t.Run("NullString valid", func(t *testing.T) {
		in := sql.NullString{String: "hello", Valid: true}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
	t.Run("NullString invalid", func(t *testing.T) {
		in := sql.NullString{}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
	t.Run("NullInt64 valid", func(t *testing.T) {
		in := sql.NullInt64{Int64: 42, Valid: true}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
	t.Run("NullInt64 invalid", func(t *testing.T) {
		in := sql.NullInt64{}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
	t.Run("NullFloat64 valid", func(t *testing.T) {
		in := sql.NullFloat64{Float64: 3.14, Valid: true}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
	t.Run("NullFloat64 invalid", func(t *testing.T) {
		in := sql.NullFloat64{}
		if got := gobRoundTripValue(t, in); got != in {
			t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", in, got)
		}
	})
}

func TestGob_GallerydbFile_InterfaceFields(t *testing.T) {
	original := fullyPopulatedGallerydbFile()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&original); err != nil {
		t.Fatalf("gob encode gallerydb.File: %v", err)
	}

	var decoded gallerydb.File
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode gallerydb.File: %v", err)
	}

	assertGallerydbFileEqual(t, "gallerydb.File round-trip", original, decoded)
}

func TestGob_Int64RegisteredForInterfaceFields(t *testing.T) {
	type testStruct struct {
		Value any
	}
	s := testStruct{Value: int64(42)}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&s); err != nil {
		t.Fatalf("gob encode struct with interface{} holding int64: %v", err)
	}

	var decoded testStruct
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode struct with interface{} holding int64: %v", err)
	}

	got, ok := decoded.Value.(int64)
	if !ok {
		t.Fatalf("decoded Value is %T, want int64", decoded.Value)
	}
	if got != 42 {
		t.Errorf("decoded Value: got %d, want 42", got)
	}
}

// ---------------------------------------------------------------------------
// Phase B: BatchedWrite custom GobEncode/GobDecode round-trip validation
// ---------------------------------------------------------------------------

func TestBatchedWrite_GobRoundTrip_FileWithThumbnail(t *testing.T) {
	originalFile := fullyPopulatedFilesFile()
	original := batchedWrite{File: originalFile}

	originalThumbPtr := originalFile.Thumbnail
	originalThumbBytes := make([]byte, originalFile.Thumbnail.Len())
	copy(originalThumbBytes, originalFile.Thumbnail.Bytes())

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("GobEncode returned empty byte slice")
	}

	if originalFile.Thumbnail != originalThumbPtr {
		t.Error("GobEncode mutated the original Thumbnail pointer")
	}
	if !bytes.Equal(originalFile.Thumbnail.Bytes(), originalThumbBytes) {
		t.Error("GobEncode mutated the original Thumbnail content")
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.CacheEntry != nil {
		t.Error("decoded CacheEntry should be nil for file write")
	}
	if decoded.File == nil {
		t.Fatal("decoded File should not be nil")
	}

	assertFilesFileEqual(t, "BatchedWrite with File+Thumbnail", originalFile, decoded.File)
}

func TestBatchedWrite_GobRoundTrip_FileWithoutThumbnail(t *testing.T) {
	fileNoThumb := fullyPopulatedFilesFile()
	fileNoThumb.Thumbnail = nil

	original := batchedWrite{File: fileNoThumb}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File == nil {
		t.Fatal("decoded File should not be nil")
	}
	if decoded.File.Thumbnail != nil {
		t.Errorf("decoded Thumbnail should be nil, got buffer with %d bytes", decoded.File.Thumbnail.Len())
	}
	if decoded.CacheEntry != nil {
		t.Error("decoded CacheEntry should be nil")
	}

	assertGallerydbFileEqual(t, "File without thumbnail", fileNoThumb.File, decoded.File.File)
	if decoded.File.Path != fileNoThumb.Path {
		t.Errorf("Path: got %q, want %q", decoded.File.Path, fileNoThumb.Path)
	}
}

func TestBatchedWrite_GobRoundTrip_CacheEntry(t *testing.T) {
	originalEntry := fullyPopulatedCacheEntry()
	original := batchedWrite{CacheEntry: originalEntry}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File != nil {
		t.Error("decoded File should be nil for cache entry")
	}
	if decoded.CacheEntry == nil {
		t.Fatal("decoded CacheEntry should not be nil")
	}

	assertHTTPCacheEntryEqual(t, "BatchedWrite with CacheEntry", originalEntry, decoded.CacheEntry)
}

func TestBatchedWrite_GobRoundTrip_Empty(t *testing.T) {
	original := batchedWrite{}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File != nil {
		t.Error("decoded File should be nil for empty BatchedWrite")
	}
	if decoded.CacheEntry != nil {
		t.Error("decoded CacheEntry should be nil for empty BatchedWrite")
	}
}

func TestBatchedWrite_GobRoundTrip_DoesNotMutateOriginal(t *testing.T) {
	file := fullyPopulatedFilesFile()
	originalThumbPtr := file.Thumbnail
	originalThumbLen := file.Thumbnail.Len()
	originalThumbBytes := make([]byte, originalThumbLen)
	copy(originalThumbBytes, file.Thumbnail.Bytes())

	original := batchedWrite{File: file}

	if _, err := original.GobEncode(); err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	if file.Thumbnail != originalThumbPtr {
		t.Error("GobEncode changed the Thumbnail pointer — caller's object was mutated")
	}
	if file.Thumbnail.Len() != originalThumbLen {
		t.Errorf("Thumbnail length changed: got %d, want %d", file.Thumbnail.Len(), originalThumbLen)
	}
	if !bytes.Equal(file.Thumbnail.Bytes(), originalThumbBytes) {
		t.Error("Thumbnail content changed after GobEncode")
	}
	if file.Path != "/photos/2024/sunset.jpg" {
		t.Errorf("Path changed: got %q", file.Path)
	}
	if file.File.Filename != "sunset.jpg" {
		t.Errorf("Filename changed: got %q", file.File.Filename)
	}

	encoded1, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode (2nd): %v", err)
	}

	if file.Thumbnail != originalThumbPtr {
		t.Error("Second GobEncode changed the Thumbnail pointer")
	}
	if file.Thumbnail.Len() != originalThumbLen {
		t.Errorf("Thumbnail length changed after second encode: got %d, want %d", file.Thumbnail.Len(), originalThumbLen)
	}

	var decoded1, decoded2 batchedWrite
	if decErr := decoded1.GobDecode(encoded1); decErr != nil {
		t.Fatalf("GobDecode (1st): %v", decErr)
	}

	encoded2, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode (3rd): %v", err)
	}
	if err := decoded2.GobDecode(encoded2); err != nil {
		t.Fatalf("GobDecode (2nd): %v", err)
	}

	if decoded1.File.Thumbnail.Len() != decoded2.File.Thumbnail.Len() {
		t.Errorf("idempotent encode mismatch: thumb len %d vs %d",
			decoded1.File.Thumbnail.Len(), decoded2.File.Thumbnail.Len())
	}
}

func TestBatchedWrite_GobRoundTrip_InterfaceFields(t *testing.T) {
	file := fullyPopulatedFilesFile()
	original := batchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	assertInterfaceInt64(t, "CreatedAt", decoded.File.File.CreatedAt, int64(1_700_000_001))
	assertInterfaceInt64(t, "UpdatedAt", decoded.File.File.UpdatedAt, int64(1_700_000_002))
}

func TestBatchedWrite_GobRoundTrip_NullFields(t *testing.T) {
	file := &files.File{
		ImagesDir: "/test",
		Path:      "/test/file.jpg",
		File: gallerydb.File{
			ID:        1,
			FolderID:  sql.NullInt64{},
			PathID:    2,
			Filename:  "file.jpg",
			SizeBytes: sql.NullInt64{},
			Mtime:     sql.NullInt64{},
			Md5:       sql.NullString{},
			Phash:     sql.NullInt64{},
			MimeType:  sql.NullString{},
			Width:     sql.NullInt64{},
			Height:    sql.NullInt64{},
			CreatedAt: int64(100),
			UpdatedAt: int64(200),
		},
	}

	original := batchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File.File.FolderID.Valid {
		t.Error("FolderID should be invalid")
	}
	if decoded.File.File.Md5.Valid {
		t.Error("Md5 should be invalid")
	}
	if decoded.File.Metadata.Exif.Latitude != 0 {
		t.Error("Exif.Latitude should be zero")
	}
	if !decoded.File.Metadata.Exif.CaptureDate.IsZero() {
		t.Error("Exif.CaptureDate should be zero")
	}

	assertInterfaceInt64(t, "CreatedAt", decoded.File.File.CreatedAt, int64(100))
	assertInterfaceInt64(t, "UpdatedAt", decoded.File.File.UpdatedAt, int64(200))
}

func TestBatchedWrite_GobRoundTrip_LargeThumbnail(t *testing.T) {
	largeThumb := make([]byte, 1024*1024)
	for i := range largeThumb {
		largeThumb[i] = byte(i % 256)
	}

	file := &files.File{
		Path:      "/photos/large.jpg",
		File:      fullyPopulatedGallerydbFile(),
		Thumbnail: bytes.NewBuffer(largeThumb),
	}

	original := batchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File.Thumbnail == nil {
		t.Fatal("decoded Thumbnail is nil")
	}
	if decoded.File.Thumbnail.Len() != len(largeThumb) {
		t.Errorf("Thumbnail length: got %d, want %d", decoded.File.Thumbnail.Len(), len(largeThumb))
	}
	if !bytes.Equal(decoded.File.Thumbnail.Bytes(), largeThumb) {
		t.Error("Thumbnail content mismatch")
	}
}

func TestBatchedWrite_GobDecode_InvalidWireData(t *testing.T) {
	var decoded batchedWrite
	err := decoded.GobDecode([]byte("not gob data"))
	if err == nil {
		t.Error("expected error for invalid wire data")
	}
}

func TestBatchedWrite_GobDecode_InvalidFileData(t *testing.T) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(batchedWriteWire{FileData: []byte("not gob data")}); err != nil {
		t.Fatalf("failed to encode wire: %v", err)
	}

	var decoded batchedWrite
	err := decoded.GobDecode(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for invalid FileData")
	}
	if !strings.Contains(err.Error(), "gob decode File") {
		t.Errorf("error = %q, want wrap 'gob decode File'", err.Error())
	}
}

func TestBatchedWrite_GobDecode_InvalidCacheEntryData(t *testing.T) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(batchedWriteWire{CacheEntryData: []byte("not gob data")}); err != nil {
		t.Fatalf("failed to encode wire: %v", err)
	}

	var decoded batchedWrite
	err := decoded.GobDecode(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for invalid CacheEntryData")
	}
	if !strings.Contains(err.Error(), "gob decode CacheEntry") {
		t.Errorf("error = %q, want wrap 'gob decode CacheEntry'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Phase C: dque integration validation
// ---------------------------------------------------------------------------

func TestBatchedWrite_DqueRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-test")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	q, err := dque.NewOrOpen[batchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	t.Cleanup(func() { q.Close() })

	items := []struct {
		name string
		bw   batchedWrite
	}{
		{
			name: "file_with_thumbnail",
			bw:   batchedWrite{File: fullyPopulatedFilesFile()},
		},
		{
			name: "file_without_thumbnail",
			bw: batchedWrite{File: func() *files.File {
				f := fullyPopulatedFilesFile()
				f.Thumbnail = nil
				return f
			}()},
		},
		{
			name: "cache_entry",
			bw:   batchedWrite{CacheEntry: fullyPopulatedCacheEntry()},
		},
		{
			name: "empty",
			bw:   batchedWrite{},
		},
	}

	for _, item := range items {
		t.Run("enqueue_"+item.name, func(t *testing.T) {
			bw := item.bw
			if enqErr := q.Enqueue(&bw); enqErr != nil {
				t.Fatalf("dque.Enqueue (%s): %v", item.name, enqErr)
			}
		})
	}

	for _, item := range items {
		t.Run("dequeue_"+item.name, func(t *testing.T) {
			dequeued, deqErr := q.Dequeue()
			if deqErr != nil {
				t.Fatalf("dque.Dequeue (%s): %v", item.name, deqErr)
			}
			if dequeued == nil {
				t.Fatalf("dequeued item is nil (%s)", item.name)
			}

			switch {
			case item.bw.File != nil:
				if dequeued.File == nil {
					t.Fatalf("expected File, got nil (%s)", item.name)
				}
				assertFilesFileEqual(t, item.name, item.bw.File, dequeued.File)
			case item.bw.CacheEntry != nil:
				if dequeued.CacheEntry == nil {
					t.Fatalf("expected CacheEntry, got nil (%s)", item.name)
				}
				assertHTTPCacheEntryEqual(t, item.name, item.bw.CacheEntry, dequeued.CacheEntry)
			default:
				if dequeued.File != nil || dequeued.CacheEntry != nil {
					t.Fatalf("expected empty BatchedWrite, got File=%v CacheEntry=%v (%s)",
						dequeued.File != nil, dequeued.CacheEntry != nil, item.name)
				}
			}
		})
	}

	_, err = q.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}

func TestBatchedWrite_DqueRoundTrip_CrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-crash")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	q1, err := dque.NewOrOpen[batchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (1st): %v", err)
	}

	fileItem := batchedWrite{File: fullyPopulatedFilesFile()}
	cacheItem := batchedWrite{CacheEntry: fullyPopulatedCacheEntry()}

	if enqErr := q1.Enqueue(&fileItem); enqErr != nil {
		t.Fatalf("enqueue file: %v", enqErr)
	}
	if enqErr := q1.Enqueue(&cacheItem); enqErr != nil {
		t.Fatalf("enqueue cache: %v", enqErr)
	}
	if cloErr := q1.Close(); cloErr != nil {
		t.Fatalf("close q1: %v", cloErr)
	}

	q2, err := dque.NewOrOpen[batchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (2nd): %v", err)
	}
	t.Cleanup(func() { q2.Close() })

	if size := q2.Size(); size != 2 {
		t.Errorf("expected queue size 2, got %d", size)
	}

	dequeuedFile, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue file: %v", err)
	}
	if dequeuedFile.File == nil {
		t.Fatal("expected File item, got nil")
	}
	assertFilesFileEqual(t, "recovered file", fileItem.File, dequeuedFile.File)

	dequeuedCache, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue cache: %v", err)
	}
	if dequeuedCache.CacheEntry == nil {
		t.Fatal("expected CacheEntry item, got nil")
	}
	assertHTTPCacheEntryEqual(t, "recovered cache", cacheItem.CacheEntry, dequeuedCache.CacheEntry)

	_, err = q2.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}

func TestBatchedWrite_GobRoundTrip_InvalidFile(t *testing.T) {
	original := batchedWrite{InvalidFile: &files.InvalidFile{Path: "c/bad.jpg", Mtime: 5, Size: 6, Reason: "decode"}}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File != nil || decoded.CacheEntry != nil || decoded.FolderIndex != nil {
		t.Errorf("decoded other kinds set: File=%v CacheEntry=%v FolderIndex=%v",
			decoded.File != nil, decoded.CacheEntry != nil, decoded.FolderIndex != nil)
	}
	if decoded.InvalidFile == nil {
		t.Fatal("decoded InvalidFile is nil")
	}
	if *decoded.InvalidFile != *original.InvalidFile {
		t.Errorf("InvalidFile: got %+v, want %+v", *decoded.InvalidFile, *original.InvalidFile)
	}
}

func TestBatchedWrite_GobRoundTrip_FolderIndexRow(t *testing.T) {
	original := batchedWrite{FolderIndex: &folderIndexRow{
		FileID:     10,
		FolderID:   3,
		ImageIndex: 2,
		ImageCount: 4,
		PrevID:     sql.NullInt64{Int64: 9, Valid: true},
		NextID:     sql.NullInt64{},
		FirstID:    8,
		LastID:     11,
		Generation: 1_700_000_000_123,
	}}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded batchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	if decoded.File != nil || decoded.InvalidFile != nil || decoded.CacheEntry != nil {
		t.Errorf("decoded other kinds set: File=%v InvalidFile=%v CacheEntry=%v",
			decoded.File != nil, decoded.InvalidFile != nil, decoded.CacheEntry != nil)
	}
	if decoded.FolderIndex == nil {
		t.Fatal("decoded FolderIndex is nil")
	}
	if *decoded.FolderIndex != *original.FolderIndex {
		t.Errorf("FolderIndex: got %+v, want %+v", *decoded.FolderIndex, *original.FolderIndex)
	}
}

func TestBatchedWrite_GobDecode_InvalidInvalidFileData(t *testing.T) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(batchedWriteWire{InvalidFileData: []byte("not gob data")}); err != nil {
		t.Fatalf("failed to encode wire: %v", err)
	}

	var decoded batchedWrite
	err := decoded.GobDecode(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for invalid InvalidFileData")
	}
	if !strings.Contains(err.Error(), "gob decode InvalidFile") {
		t.Errorf("error = %q, want wrap 'gob decode InvalidFile'", err.Error())
	}
}
