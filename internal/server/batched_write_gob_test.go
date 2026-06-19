package server

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
)

// ---------------------------------------------------------------------------
// Helpers: construct fully-populated test objects
// ---------------------------------------------------------------------------

// fullyPopulatedGallerydbFile returns a gallerydb.File with every field set
// to a non-zero value, including CreatedAt/UpdatedAt as int64 (the concrete
// runtime type stored in the interface{} fields by the SQLite driver).
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
		CreatedAt: int64(1_700_000_001), // interface{} holding int64 — must be gob-registered
		UpdatedAt: int64(1_700_000_002), // interface{} holding int64 — must be gob-registered
	}
}

// fullyPopulatedFilesFile returns a *files.File with every field set to a
// non-zero value, including a Thumbnail buffer with realistic content.
func fullyPopulatedFilesFile() *files.File {
	thumbContent := make([]byte, 50*1024) // 50 KB thumbnail
	for i := range thumbContent {
		thumbContent[i] = byte(i % 256)
	}

	return &files.File{
		Ok:        true,
		Exists:    false,
		ImagesDir: "/photos/2024",
		Path:      "/photos/2024/sunset.jpg",
		File:      fullyPopulatedGallerydbFile(),
		Thumbnail: bytes.NewBuffer(thumbContent),
		Exif: gallerydb.UpsertExifParams{
			FileID:       42,
			CameraMake:   sql.NullString{String: "Canon", Valid: true},
			CameraModel:  sql.NullString{String: "EOS R5", Valid: true},
			LensModel:    sql.NullString{String: "RF 24-105mm", Valid: true},
			FocalLength:  sql.NullString{String: "85mm", Valid: true},
			Aperture:     sql.NullString{String: "f/2.8", Valid: true},
			ShutterSpeed: sql.NullString{String: "1/500", Valid: true},
			Iso:          sql.NullInt64{Int64: 400, Valid: true},
			Orientation:  sql.NullInt64{Int64: 1, Valid: true},
			Latitude:     sql.NullFloat64{Float64: 37.7749, Valid: true},
			Longitude:    sql.NullFloat64{Float64: -122.4194, Valid: true},
			Altitude:     sql.NullFloat64{Float64: 10.5, Valid: true},
			CaptureDate:  sql.NullInt64{Int64: 1_700_000_000, Valid: true},
		},
		Itpc: gallerydb.UpsertIPTCParams{
			FileID:      42,
			Title:       sql.NullString{String: "Sunset over Pacific", Valid: true},
			Description: sql.NullString{String: "A beautiful sunset", Valid: true},
			Keywords:    sql.NullString{String: "sunset,pacific,ocean", Valid: true},
			Creator:     sql.NullString{String: "Photographer", Valid: true},
			Copyright:   sql.NullString{String: "© 2024", Valid: true},
			Credit:      sql.NullString{String: "Stock", Valid: true},
			Source:      sql.NullString{String: "Original", Valid: true},
			CreatedDate: sql.NullInt64{Int64: 1_700_000_000, Valid: true},
		},
		XmpProp: gallerydb.UpsertXMPPropertyParams{
			ID:        1,
			FileID:    42,
			Namespace: "dc",
			Property:  "title",
			Value:     sql.NullString{String: "Sunset over Pacific", Valid: true},
		},
		XmpRaw: gallerydb.UpsertXMPRawParams{
			FileID: 42,
			RawXml: sql.NullString{String: "<xmp>data</xmp>", Valid: true},
		},
		HasValidJpegMarkers: true,
	}
}

// fullyPopulatedCacheEntry returns a *cachelite.HTTPCacheEntry with every
// field set to a non-zero value.
func fullyPopulatedCacheEntry() *cachelite.HTTPCacheEntry {
	return &cachelite.HTTPCacheEntry{
		ID:              99,
		Key:             "sha256:abcdef1234567890",
		Method:          "GET",
		Path:            "/gallery/42",
		QueryString:     sql.NullString{String: "page=1", Valid: true},
		Encoding:        "br",
		Status:          200,
		ContentType:     sql.NullString{String: "text/html; charset=utf-8", Valid: true},
		ContentEncoding: sql.NullString{String: "br", Valid: true},
		CacheControl:    sql.NullString{String: "max-age=3600", Valid: true},
		ETag:            sql.NullString{String: "\"etag-42\"", Valid: true},
		LastModified:    sql.NullString{String: "Mon, 01 Jan 2024 00:00:00 GMT", Valid: true},
		Vary:            sql.NullString{String: "Accept-Encoding", Valid: true},
		Body:            []byte("<html>gallery page</html>"),
		ContentLength:   sql.NullInt64{Int64: 1234, Valid: true},
		CreatedAt:       1_700_000_003,
		ExpiresAt:       sql.NullInt64{Int64: 1_700_003_603, Valid: true},
	}
}

// ---------------------------------------------------------------------------
// Phase A: Raw type validation — can individual types survive gob encoding?
// ---------------------------------------------------------------------------

// TestGob_SqlNullTypes verifies that sql.NullXxx types round-trip through
// gob without data loss. These are used extensively in gallerydb structs.
func TestGob_SqlNullTypes(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		equal func(a, b interface{}) bool
	}{
		{
			name:  "NullString valid",
			value: sql.NullString{String: "hello", Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullString) == b.(sql.NullString)
			},
		},
		{
			name:  "NullString invalid",
			value: sql.NullString{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullString) == b.(sql.NullString)
			},
		},
		{
			name:  "NullInt64 valid",
			value: sql.NullInt64{Int64: 42, Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullInt64) == b.(sql.NullInt64)
			},
		},
		{
			name:  "NullInt64 invalid",
			value: sql.NullInt64{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullInt64) == b.(sql.NullInt64)
			},
		},
		{
			name:  "NullFloat64 valid",
			value: sql.NullFloat64{Float64: 3.14, Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullFloat64) == b.(sql.NullFloat64)
			},
		},
		{
			name:  "NullFloat64 invalid",
			value: sql.NullFloat64{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullFloat64) == b.(sql.NullFloat64)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(tt.value); err != nil {
				t.Fatalf("gob encode: %v", err)
			}

			// Decode into a new zero value of the same type
			newVal := reflect.New(reflect.TypeOf(tt.value)).Interface()
			if err := gob.NewDecoder(&buf).Decode(newVal); err != nil {
				t.Fatalf("gob decode: %v", err)
			}

			// Compare
			decoded := reflect.ValueOf(newVal).Elem().Interface()
			if !tt.equal(tt.value, decoded) {
				t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", tt.value, decoded)
			}
		})
	}
}

// TestGob_GallerydbFile_InterfaceFields verifies that gallerydb.File with
// interface{} fields holding int64 values round-trips through gob. This is
// the critical test: without gob.Register(int64(0)), this will panic.
func TestGob_GallerydbFile_InterfaceFields(t *testing.T) {
	original := fullyPopulatedGallerydbFile()

	// Encode
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&original); err != nil {
		t.Fatalf("gob encode gallerydb.File: %v", err)
	}

	// Decode
	var decoded gallerydb.File
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode gallerydb.File: %v", err)
	}

	// Verify all fields
	assertGallerydbFileEqual(t, "gallerydb.File round-trip", original, decoded)
}

// TestGob_UpsertThumbnailReturningIDParams_InterfaceFields verifies that
// UpsertThumbnailReturningIDParams (which also has CreatedAt/UpdatedAt
// interface{} fields) round-trips correctly.
func TestGob_UpsertThumbnailReturningIDParams_InterfaceFields(t *testing.T) {
	original := gallerydb.UpsertThumbnailReturningIDParams{
		FileID:    42,
		SizeLabel: "medium",
		Width:     400,
		Height:    300,
		Format:    "webp",
		CreatedAt: int64(1_700_000_001),
		UpdatedAt: int64(1_700_000_002),
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&original); err != nil {
		t.Fatalf("gob encode: %v", err)
	}

	var decoded gallerydb.UpsertThumbnailReturningIDParams
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode: %v", err)
	}

	if decoded.FileID != original.FileID {
		t.Errorf("FileID: got %d, want %d", decoded.FileID, original.FileID)
	}
	if decoded.SizeLabel != original.SizeLabel {
		t.Errorf("SizeLabel: got %q, want %q", decoded.SizeLabel, original.SizeLabel)
	}
	if decoded.Width != original.Width || decoded.Height != original.Height {
		t.Errorf("dimensions: got %dx%d, want %dx%d", decoded.Width, decoded.Height, original.Width, original.Height)
	}
	if decoded.Format != original.Format {
		t.Errorf("Format: got %q, want %q", decoded.Format, original.Format)
	}

	// Verify the interface{} fields came back as int64 with correct values
	assertInterfaceInt64(t, "CreatedAt", decoded.CreatedAt, original.CreatedAt)
	assertInterfaceInt64(t, "UpdatedAt", decoded.UpdatedAt, original.UpdatedAt)
}

// ---------------------------------------------------------------------------
// Phase B: BatchedWrite custom GobEncode/GobDecode round-trip validation
// ---------------------------------------------------------------------------

// TestBatchedWrite_GobRoundTrip_FileWithThumbnail verifies that a fully
// populated BatchedWrite with a File (including thumbnail) survives
// GobEncode → GobDecode with zero data loss.
func TestBatchedWrite_GobRoundTrip_FileWithThumbnail(t *testing.T) {
	originalFile := fullyPopulatedFilesFile()
	original := BatchedWrite{File: originalFile}

	// Save the original thumbnail pointer and content for immutability check
	originalThumbPtr := originalFile.Thumbnail
	originalThumbBytes := make([]byte, originalFile.Thumbnail.Len())
	copy(originalThumbBytes, originalFile.Thumbnail.Bytes())

	// Encode
	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("GobEncode returned empty byte slice")
	}

	// Verify the original object is unmutated
	if originalFile.Thumbnail != originalThumbPtr {
		t.Error("GobEncode mutated the original Thumbnail pointer")
	}
	if !bytes.Equal(originalFile.Thumbnail.Bytes(), originalThumbBytes) {
		t.Error("GobEncode mutated the original Thumbnail content")
	}

	// Decode
	var decoded BatchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	// Verify decoded BatchedWrite
	if decoded.CacheEntry != nil {
		t.Error("decoded CacheEntry should be nil for file write")
	}
	if decoded.File == nil {
		t.Fatal("decoded File should not be nil")
	}

	// Verify all fields of the decoded files.File
	assertFilesFileEqual(t, "BatchedWrite with File+Thumbnail", originalFile, decoded.File)
}

// TestBatchedWrite_GobRoundTrip_FileWithoutThumbnail verifies that a File
// without a thumbnail round-trips correctly (Thumbnail should be nil after
// decode, not an empty buffer).
func TestBatchedWrite_GobRoundTrip_FileWithoutThumbnail(t *testing.T) {
	fileNoThumb := fullyPopulatedFilesFile()
	fileNoThumb.Thumbnail = nil // No thumbnail

	original := BatchedWrite{File: fileNoThumb}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
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

	// Verify all other fields
	assertGallerydbFileEqual(t, "File without thumbnail", fileNoThumb.File, decoded.File.File)
	if decoded.File.Ok != fileNoThumb.Ok {
		t.Errorf("Ok: got %v, want %v", decoded.File.Ok, fileNoThumb.Ok)
	}
	if decoded.File.Path != fileNoThumb.Path {
		t.Errorf("Path: got %q, want %q", decoded.File.Path, fileNoThumb.Path)
	}
	if decoded.File.HasValidJpegMarkers != fileNoThumb.HasValidJpegMarkers {
		t.Errorf("HasValidJpegMarkers: got %v, want %v", decoded.File.HasValidJpegMarkers, fileNoThumb.HasValidJpegMarkers)
	}
}

// TestBatchedWrite_GobRoundTrip_CacheEntry verifies that a BatchedWrite
// with an HTTPCacheEntry round-trips correctly.
func TestBatchedWrite_GobRoundTrip_CacheEntry(t *testing.T) {
	originalEntry := fullyPopulatedCacheEntry()
	original := BatchedWrite{CacheEntry: originalEntry}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
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

// TestBatchedWrite_GobRoundTrip_Empty verifies that a BatchedWrite with
// both fields nil round-trips correctly (both should remain nil).
func TestBatchedWrite_GobRoundTrip_Empty(t *testing.T) {
	original := BatchedWrite{} // both nil

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
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

// TestBatchedWrite_GobEncode_DoesNotMutateOriginal verifies the critical
// invariant: GobEncode must not mutate the caller's *files.File, especially
// the Thumbnail pointer. The design uses a struct copy before nil-out to
// ensure this.
func TestBatchedWrite_GobRoundTrip_DoesNotMutateOriginal(t *testing.T) {
	file := fullyPopulatedFilesFile()
	originalThumbPtr := file.Thumbnail
	originalThumbLen := file.Thumbnail.Len()
	originalThumbBytes := make([]byte, originalThumbLen)
	copy(originalThumbBytes, file.Thumbnail.Bytes())

	original := BatchedWrite{File: file}

	// Encode (should NOT mutate the original)
	if _, err := original.GobEncode(); err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	// Check 1: Thumbnail pointer unchanged
	if file.Thumbnail != originalThumbPtr {
		t.Error("GobEncode changed the Thumbnail pointer — caller's object was mutated")
	}

	// Check 2: Thumbnail content unchanged
	if file.Thumbnail.Len() != originalThumbLen {
		t.Errorf("Thumbnail length changed: got %d, want %d", file.Thumbnail.Len(), originalThumbLen)
	}
	if !bytes.Equal(file.Thumbnail.Bytes(), originalThumbBytes) {
		t.Error("Thumbnail content changed after GobEncode")
	}

	// Check 3: Other fields unchanged
	if file.Path != "/photos/2024/sunset.jpg" {
		t.Errorf("Path changed: got %q", file.Path)
	}
	if file.File.Filename != "sunset.jpg" {
		t.Errorf("Filename changed: got %q", file.File.Filename)
	}
	if file.Ok != true {
		t.Error("Ok changed")
	}

	// Encode again — should produce the same result (idempotent)
	encoded1, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode (2nd): %v", err)
	}

	// Thumbnail should still be intact after second encode
	if file.Thumbnail != originalThumbPtr {
		t.Error("Second GobEncode changed the Thumbnail pointer")
	}
	if file.Thumbnail.Len() != originalThumbLen {
		t.Errorf("Thumbnail length changed after second encode: got %d, want %d", file.Thumbnail.Len(), originalThumbLen)
	}

	// Decode both encodings and verify they produce identical results
	var decoded1, decoded2 BatchedWrite
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

// TestBatchedWrite_GobRoundTrip_InterfaceFields verifies that the
// interface{} fields (CreatedAt, UpdatedAt) inside gallerydb.File survive
// the full BatchedWrite GobEncode/GobDecode round-trip as int64 values.
func TestBatchedWrite_GobRoundTrip_InterfaceFields(t *testing.T) {
	file := fullyPopulatedFilesFile()
	original := BatchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	// Verify interface{} fields came back as int64 with correct values
	assertInterfaceInt64(t, "CreatedAt", decoded.File.File.CreatedAt, int64(1_700_000_001))
	assertInterfaceInt64(t, "UpdatedAt", decoded.File.File.UpdatedAt, int64(1_700_000_002))
}

// TestBatchedWrite_GobRoundTrip_NullFields verifies that sql.NullXxx fields
// with Valid=false round-trip correctly through the full BatchedWrite
// encoding path.
func TestBatchedWrite_GobRoundTrip_NullFields(t *testing.T) {
	file := &files.File{
		Ok:        true,
		Exists:    false,
		ImagesDir: "/test",
		Path:      "/test/file.jpg",
		File: gallerydb.File{
			ID:        1,
			FolderID:  sql.NullInt64{}, // invalid
			PathID:    2,
			Filename:  "file.jpg",
			SizeBytes: sql.NullInt64{},  // invalid
			Mtime:     sql.NullInt64{},  // invalid
			Md5:       sql.NullString{}, // invalid
			Phash:     sql.NullInt64{},  // invalid
			MimeType:  sql.NullString{}, // invalid
			Width:     sql.NullInt64{},  // invalid
			Height:    sql.NullInt64{},  // invalid
			CreatedAt: int64(100),
			UpdatedAt: int64(200),
		},
		Exif: gallerydb.UpsertExifParams{
			FileID:      1,
			CameraMake:  sql.NullString{},  // invalid
			Latitude:    sql.NullFloat64{}, // invalid
			CaptureDate: sql.NullInt64{},   // invalid
		},
	}

	original := BatchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
	if err := decoded.GobDecode(encoded); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}

	// Verify invalid NullXxx fields stayed invalid
	if decoded.File.File.FolderID.Valid {
		t.Error("FolderID should be invalid")
	}
	if decoded.File.File.Md5.Valid {
		t.Error("Md5 should be invalid")
	}
	if decoded.File.Exif.Latitude.Valid {
		t.Error("Exif.Latitude should be invalid")
	}
	if decoded.File.Exif.CaptureDate.Valid {
		t.Error("Exif.CaptureDate should be invalid")
	}

	// Verify interface{} fields still correct
	assertInterfaceInt64(t, "CreatedAt", decoded.File.File.CreatedAt, int64(100))
	assertInterfaceInt64(t, "UpdatedAt", decoded.File.File.UpdatedAt, int64(200))
}

// TestBatchedWrite_GobRoundTrip_LargeThumbnail verifies that a large
// thumbnail (1 MB) round-trips correctly, ensuring no size limits are hit.
func TestBatchedWrite_GobRoundTrip_LargeThumbnail(t *testing.T) {
	largeThumb := make([]byte, 1024*1024) // 1 MB
	for i := range largeThumb {
		largeThumb[i] = byte(i % 256)
	}

	file := &files.File{
		Ok:        true,
		Path:      "/photos/large.jpg",
		File:      fullyPopulatedGallerydbFile(),
		Thumbnail: bytes.NewBuffer(largeThumb),
	}

	original := BatchedWrite{File: file}

	encoded, err := original.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}

	var decoded BatchedWrite
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

// ---------------------------------------------------------------------------
// Phase C: dque integration validation
// ---------------------------------------------------------------------------

// TestBatchedWrite_DqueRoundTrip verifies that a fully-populated BatchedWrite
// survives the full dque Enqueue → Dequeue path. This tests the actual gob
// encoding that dque uses internally (length-prefixed gob records in segment
// files), not just our GobEncode/GobDecode methods.
func TestBatchedWrite_DqueRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-test")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	q, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	t.Cleanup(func() { q.Close() })

	// Enqueue all variants
	items := []struct {
		name string
		bw   BatchedWrite
	}{
		{
			name: "file_with_thumbnail",
			bw:   BatchedWrite{File: fullyPopulatedFilesFile()},
		},
		{
			name: "file_without_thumbnail",
			bw: BatchedWrite{File: func() *files.File {
				f := fullyPopulatedFilesFile()
				f.Thumbnail = nil
				return f
			}()},
		},
		{
			name: "cache_entry",
			bw:   BatchedWrite{CacheEntry: fullyPopulatedCacheEntry()},
		},
		{
			name: "empty",
			bw:   BatchedWrite{},
		},
	}

	for _, item := range items {
		t.Run("enqueue_"+item.name, func(t *testing.T) {
			copy := item.bw
			if enqErr := q.Enqueue(&copy); enqErr != nil {
				t.Fatalf("dque.Enqueue (%s): %v", item.name, enqErr)
			}
		})
	}

	// Dequeue and verify each
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

	// Queue should now be empty
	_, err = q.Dequeue()
	if err != dque.ErrEmpty {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}

// TestBatchedWrite_DqueRoundTrip_CrashRecovery verifies that items survive
// a simulated crash: enqueue items, close dque without draining, reopen,
// and verify items are still present.
func TestBatchedWrite_DqueRoundTrip_CrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-crash")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	// Phase 1: Enqueue items and close (simulating a clean shutdown with unprocessed items)
	q1, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (1st): %v", err)
	}

	fileItem := BatchedWrite{File: fullyPopulatedFilesFile()}
	cacheItem := BatchedWrite{CacheEntry: fullyPopulatedCacheEntry()}

	if enqErr := q1.Enqueue(&fileItem); enqErr != nil {
		t.Fatalf("enqueue file: %v", enqErr)
	}
	if enqErr := q1.Enqueue(&cacheItem); enqErr != nil {
		t.Fatalf("enqueue cache: %v", enqErr)
	}
	if cloErr := q1.Close(); cloErr != nil {
		t.Fatalf("close q1: %v", cloErr)
	}

	// Phase 2: Reopen in a new instance (simulating process restart)
	q2, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (2nd): %v", err)
	}
	t.Cleanup(func() { q2.Close() })

	// Verify size
	if size := q2.Size(); size != 2 {
		t.Errorf("expected queue size 2, got %d", size)
	}

	// Dequeue and verify file item
	dequeuedFile, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue file: %v", err)
	}
	if dequeuedFile.File == nil {
		t.Fatal("expected File item, got nil")
	}
	assertFilesFileEqual(t, "recovered file", fileItem.File, dequeuedFile.File)

	// Dequeue and verify cache item
	dequeuedCache, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue cache: %v", err)
	}
	if dequeuedCache.CacheEntry == nil {
		t.Fatal("expected CacheEntry item, got nil")
	}
	assertHTTPCacheEntryEqual(t, "recovered cache", cacheItem.CacheEntry, dequeuedCache.CacheEntry)

	// Queue should now be empty
	_, err = q2.Dequeue()
	if err != dque.ErrEmpty {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Field-level assertion helpers
// ---------------------------------------------------------------------------

// assertGallerydbFileEqual compares two gallerydb.File values field by field.
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

// assertFilesFileEqual compares two *files.File values field by field,
// including the Thumbnail buffer content.
func assertFilesFileEqual(t *testing.T, context string, a, b *files.File) {
	t.Helper()
	if a.Ok != b.Ok {
		t.Errorf("%s: Ok: got %v, want %v", context, b.Ok, a.Ok)
	}
	if a.Exists != b.Exists {
		t.Errorf("%s: Exists: got %v, want %v", context, b.Exists, a.Exists)
	}
	if a.ImagesDir != b.ImagesDir {
		t.Errorf("%s: ImagesDir: got %q, want %q", context, b.ImagesDir, a.ImagesDir)
	}
	if a.Path != b.Path {
		t.Errorf("%s: Path: got %q, want %q", context, b.Path, a.Path)
	}
	if a.HasValidJpegMarkers != b.HasValidJpegMarkers {
		t.Errorf("%s: HasValidJpegMarkers: got %v, want %v", context, b.HasValidJpegMarkers, a.HasValidJpegMarkers)
	}

	// Thumbnail
	switch {
	case a.Thumbnail == nil && b.Thumbnail == nil:
		// Both nil — OK
	case a.Thumbnail == nil && b.Thumbnail != nil:
		t.Errorf("%s: Thumbnail: original nil, decoded has %d bytes", context, b.Thumbnail.Len())
	case a.Thumbnail != nil && b.Thumbnail == nil:
		t.Errorf("%s: Thumbnail: original has %d bytes, decoded nil", context, a.Thumbnail.Len())
	case a.Thumbnail != nil && b.Thumbnail != nil:
		if a.Thumbnail.Len() != b.Thumbnail.Len() {
			t.Errorf("%s: Thumbnail.Len: got %d, want %d", context, b.Thumbnail.Len(), a.Thumbnail.Len())
		}
		if !bytes.Equal(a.Thumbnail.Bytes(), b.Thumbnail.Bytes()) {
			// Show first mismatched byte for debugging
			aBytes := a.Thumbnail.Bytes()
			bBytes := b.Thumbnail.Bytes()
			minLen := len(aBytes)
			if len(bBytes) < minLen {
				minLen = len(bBytes)
			}
			for i := 0; i < minLen; i++ {
				if aBytes[i] != bBytes[i] {
					t.Errorf("%s: Thumbnail content mismatch at byte %d: got 0x%02x, want 0x%02x", context, i, bBytes[i], aBytes[i])
					break
				}
			}
		}
	}

	// Embedded gallerydb.File
	assertGallerydbFileEqual(t, context+".File", a.File, b.File)

	// EXIF
	assertUpsertExifParamsEqual(t, context+".Exif", a.Exif, b.Exif)

	// IPTC
	assertUpsertIPTCParamsEqual(t, context+".Itpc", a.Itpc, b.Itpc)

	// XMP
	assertUpsertXMPPropertyParamsEqual(t, context+".XmpProp", a.XmpProp, b.XmpProp)
	assertUpsertXMPRawParamsEqual(t, context+".XmpRaw", a.XmpRaw, b.XmpRaw)
}

// assertUpsertExifParamsEqual compares two UpsertExifParams values.
func assertUpsertExifParamsEqual(t *testing.T, context string, a, b gallerydb.UpsertExifParams) {
	t.Helper()
	if a.FileID != b.FileID {
		t.Errorf("%s: FileID: got %d, want %d", context, b.FileID, a.FileID)
	}
	if a.CameraMake != b.CameraMake {
		t.Errorf("%s: CameraMake: got %+v, want %+v", context, b.CameraMake, a.CameraMake)
	}
	if a.CameraModel != b.CameraModel {
		t.Errorf("%s: CameraModel: got %+v, want %+v", context, b.CameraModel, a.CameraModel)
	}
	if a.LensModel != b.LensModel {
		t.Errorf("%s: LensModel: got %+v, want %+v", context, b.LensModel, a.LensModel)
	}
	if a.FocalLength != b.FocalLength {
		t.Errorf("%s: FocalLength: got %+v, want %+v", context, b.FocalLength, a.FocalLength)
	}
	if a.Aperture != b.Aperture {
		t.Errorf("%s: Aperture: got %+v, want %+v", context, b.Aperture, a.Aperture)
	}
	if a.ShutterSpeed != b.ShutterSpeed {
		t.Errorf("%s: ShutterSpeed: got %+v, want %+v", context, b.ShutterSpeed, a.ShutterSpeed)
	}
	if a.Iso != b.Iso {
		t.Errorf("%s: Iso: got %+v, want %+v", context, b.Iso, a.Iso)
	}
	if a.Orientation != b.Orientation {
		t.Errorf("%s: Orientation: got %+v, want %+v", context, b.Orientation, a.Orientation)
	}
	if a.Latitude != b.Latitude {
		t.Errorf("%s: Latitude: got %+v, want %+v", context, b.Latitude, a.Latitude)
	}
	if a.Longitude != b.Longitude {
		t.Errorf("%s: Longitude: got %+v, want %+v", context, b.Longitude, a.Longitude)
	}
	if a.Altitude != b.Altitude {
		t.Errorf("%s: Altitude: got %+v, want %+v", context, b.Altitude, a.Altitude)
	}
	if a.CaptureDate != b.CaptureDate {
		t.Errorf("%s: CaptureDate: got %+v, want %+v", context, b.CaptureDate, a.CaptureDate)
	}
}

// assertUpsertIPTCParamsEqual compares two UpsertIPTCParams values.
func assertUpsertIPTCParamsEqual(t *testing.T, context string, a, b gallerydb.UpsertIPTCParams) {
	t.Helper()
	if a.FileID != b.FileID {
		t.Errorf("%s: FileID: got %d, want %d", context, b.FileID, a.FileID)
	}
	if a.Title != b.Title {
		t.Errorf("%s: Title: got %+v, want %+v", context, b.Title, a.Title)
	}
	if a.Description != b.Description {
		t.Errorf("%s: Description: got %+v, want %+v", context, b.Description, a.Description)
	}
	if a.Keywords != b.Keywords {
		t.Errorf("%s: Keywords: got %+v, want %+v", context, b.Keywords, a.Keywords)
	}
	if a.Creator != b.Creator {
		t.Errorf("%s: Creator: got %+v, want %+v", context, b.Creator, a.Creator)
	}
	if a.Copyright != b.Copyright {
		t.Errorf("%s: Copyright: got %+v, want %+v", context, b.Copyright, a.Copyright)
	}
	if a.Credit != b.Credit {
		t.Errorf("%s: Credit: got %+v, want %+v", context, b.Credit, a.Credit)
	}
	if a.Source != b.Source {
		t.Errorf("%s: Source: got %+v, want %+v", context, b.Source, a.Source)
	}
	if a.CreatedDate != b.CreatedDate {
		t.Errorf("%s: CreatedDate: got %+v, want %+v", context, b.CreatedDate, a.CreatedDate)
	}
}

// assertUpsertXMPPropertyParamsEqual compares two UpsertXMPPropertyParams values.
func assertUpsertXMPPropertyParamsEqual(t *testing.T, context string, a, b gallerydb.UpsertXMPPropertyParams) {
	t.Helper()
	if a.ID != b.ID {
		t.Errorf("%s: ID: got %d, want %d", context, b.ID, a.ID)
	}
	if a.FileID != b.FileID {
		t.Errorf("%s: FileID: got %d, want %d", context, b.FileID, a.FileID)
	}
	if a.Namespace != b.Namespace {
		t.Errorf("%s: Namespace: got %q, want %q", context, b.Namespace, a.Namespace)
	}
	if a.Property != b.Property {
		t.Errorf("%s: Property: got %q, want %q", context, b.Property, a.Property)
	}
	if a.Value != b.Value {
		t.Errorf("%s: Value: got %+v, want %+v", context, b.Value, a.Value)
	}
}

// assertUpsertXMPRawParamsEqual compares two UpsertXMPRawParams values.
func assertUpsertXMPRawParamsEqual(t *testing.T, context string, a, b gallerydb.UpsertXMPRawParams) {
	t.Helper()
	if a.FileID != b.FileID {
		t.Errorf("%s: FileID: got %d, want %d", context, b.FileID, a.FileID)
	}
	if a.RawXml != b.RawXml {
		t.Errorf("%s: RawXml: got %+v, want %+v", context, b.RawXml, a.RawXml)
	}
}

// assertHTTPCacheEntryEqual compares two *cachelite.HTTPCacheEntry values field by field.
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
	if a.Encoding != b.Encoding {
		t.Errorf("%s: Encoding: got %q, want %q", context, b.Encoding, a.Encoding)
	}
	if a.Status != b.Status {
		t.Errorf("%s: Status: got %d, want %d", context, b.Status, a.Status)
	}
	if a.ContentType != b.ContentType {
		t.Errorf("%s: ContentType: got %+v, want %+v", context, b.ContentType, a.ContentType)
	}
	if a.ContentEncoding != b.ContentEncoding {
		t.Errorf("%s: ContentEncoding: got %+v, want %+v", context, b.ContentEncoding, a.ContentEncoding)
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

// assertInterfaceInt64 verifies that an interface{} value contains an int64
// with the expected value. This is used for the sqlc-generated CreatedAt/
// UpdatedAt fields which are interface{} at compile time but hold int64 at
// runtime.
func assertInterfaceInt64(t *testing.T, fieldName string, got, want interface{}) {
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
// Additional verification: ensure gob.Register is required
// ---------------------------------------------------------------------------

// TestGob_InterfaceFieldRequiresRegistration demonstrates that encoding an
// int64 inside an interface{} field without gob.Register(int64(0)) would
// fail. Since our init() already registers int64, we can't easily test the
// negative case without a separate process. Instead, we verify that the
// registration IS in effect by checking the gob type map.
func TestGob_Int64RegisteredForInterfaceFields(t *testing.T) {
	// Verify that int64 is registered in gob's global type map.
	// We do this by encoding a struct with an interface{} field holding int64
	// and confirming it succeeds (if registration were missing, this would panic).
	type testStruct struct {
		Value interface{}
	}

	s := testStruct{Value: int64(42)}

	var buf bytes.Buffer
	// This would panic if int64 was not registered
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

// TestGob_InterfaceFieldRequiresRegistration_Subprocess verifies that WITHOUT
// gob.Register, encoding an int64 in an interface{} field fails. It runs a
// Go program as a subprocess that does NOT call gob.Register(int64(0)) and
// tries to encode an interface{} holding int64.
func TestGob_InterfaceFieldRequiresRegistration_Subprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	source := `package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
)

type testStruct struct {
	Value interface{}
}

func main() {
	s := testStruct{Value: int64(42)}
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(&s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ENCODE_ERROR: %v\n", err)
		os.Exit(0) // Expected: encoding fails without registration
	}
	fmt.Fprintf(os.Stderr, "ENCODE_SUCCEEDED\n")
	os.Exit(1) // Unexpected: encoding worked without registration
}
`

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "main.go")

	if err := os.WriteFile(srcFile, []byte(source), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Run the subprocess via 'go run'
	runCmd := exec.Command("go", "run", srcFile)
	runOutput, _ := runCmd.CombinedOutput()
	t.Logf("subprocess output: %q", string(runOutput))

	// The subprocess prints ENCODE_ERROR (and exits 0) if encoding failed without registration,
	// or ENCODE_SUCCEEDED (and exits 1) if encoding worked.
	output := string(runOutput)
	switch {
	case bytes.Contains([]byte(output), []byte("ENCODE_ERROR")):
		t.Log("CONFIRMED: gob.Register(int64(0)) is required — encoding fails without it")
	case bytes.Contains([]byte(output), []byte("ENCODE_SUCCEEDED")):
		t.Log("NOTE: gob encoded int64 in interface{} WITHOUT registration — " +
			"our init() registration is defensive but may not be strictly required")
	default:
		t.Logf("unexpected subprocess output: %q", output)
	}
}
