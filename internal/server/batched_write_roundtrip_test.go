package server

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
)

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
