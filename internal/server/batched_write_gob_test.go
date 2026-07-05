package server

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
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

// TestGob_GallerydbFile_InterfaceFields verifies that gallerydb.File with
// interface{} fields holding int64 values round-trips through gob. This is
// the critical test: without gob.Register(int64(0)), this will panic.

// TestGob_UpsertThumbnailReturningIDParams_InterfaceFields verifies that
// UpsertThumbnailReturningIDParams (which also has CreatedAt/UpdatedAt
// interface{} fields) round-trips correctly.

// ---------------------------------------------------------------------------
// Phase B: BatchedWrite custom GobEncode/GobDecode round-trip validation
// ---------------------------------------------------------------------------

// TestBatchedWrite_GobRoundTrip_FileWithThumbnail verifies that a fully
// populated BatchedWrite with a File (including thumbnail) survives
// GobEncode → GobDecode with zero data loss.

// TestBatchedWrite_GobRoundTrip_FileWithoutThumbnail verifies that a File
// without a thumbnail round-trips correctly (Thumbnail should be nil after
// decode, not an empty buffer).

// TestBatchedWrite_GobRoundTrip_CacheEntry verifies that a BatchedWrite
// with an HTTPCacheEntry round-trips correctly.

// TestBatchedWrite_GobRoundTrip_Empty verifies that a BatchedWrite with
// both fields nil round-trips correctly (both should remain nil).

// TestBatchedWrite_GobEncode_DoesNotMutateOriginal verifies the critical
// invariant: GobEncode must not mutate the caller's *files.File, especially
// the Thumbnail pointer. The design uses a struct copy before nil-out to
// ensure this.

// TestBatchedWrite_GobRoundTrip_InterfaceFields verifies that the
// interface{} fields (CreatedAt, UpdatedAt) inside gallerydb.File survive
// the full BatchedWrite GobEncode/GobDecode round-trip as int64 values.

// TestBatchedWrite_GobRoundTrip_NullFields verifies that sql.NullXxx fields
// with Valid=false round-trip correctly through the full BatchedWrite
// encoding path.

// TestBatchedWrite_GobRoundTrip_LargeThumbnail verifies that a large
// thumbnail (1 MB) round-trips correctly, ensuring no size limits are hit.

// ---------------------------------------------------------------------------
// Phase C: dque integration validation
// ---------------------------------------------------------------------------

// TestBatchedWrite_DqueRoundTrip verifies that a fully-populated BatchedWrite
// survives the full dque Enqueue → Dequeue path. This tests the actual gob
// encoding that dque uses internally (length-prefixed gob records in segment
// files), not just our GobEncode/GobDecode methods.

// TestBatchedWrite_DqueRoundTrip_CrashRecovery verifies that items survive
// a simulated crash: enqueue items, close dque without draining, reopen,
// and verify items are still present.

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

// TestGob_InterfaceFieldRequiresRegistration_Subprocess verifies that WITHOUT
// gob.Register, encoding an int64 in an interface{} field fails. It runs a
// Go program as a subprocess that does NOT call gob.Register(int64(0)) and
// tries to encode an interface{} holding int64.
