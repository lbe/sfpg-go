package thumbnail

import (
	"bytes"
	"database/sql"
	"testing"
)

// TestMockGenerator demonstrates fast testing without real image processing.
// This test runs in microseconds vs milliseconds for real thumbnail generation.
func TestMockGenerator(t *testing.T) {
	mockThumb := bytes.NewBuffer([]byte("fake-thumbnail-data"))
	mockMD5 := &sql.NullString{String: "abc123", Valid: true}
	mockPHash := &sql.NullInt64{Int64: 12345, Valid: true}

	mock := &MockGenerator{
		Thumbnail: mockThumb,
		MD5:       mockMD5,
		PHash:     mockPHash,
	}

	// Fast test - no actual image decoding
	thumb, md5, phash, err := mock.GenerateThumbnailAndHashes(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(thumb.Bytes(), mockThumb.Bytes()) {
		t.Errorf("expected thumbnail %q, got %q", mockThumb.Bytes(), thumb.Bytes())
	}
	if md5.String != mockMD5.String {
		t.Errorf("expected MD5 %q, got %q", mockMD5.String, md5.String)
	}
	if phash.Int64 != mockPHash.Int64 {
		t.Errorf("expected pHash %d, got %d", mockPHash.Int64, phash.Int64)
	}
}

// TestMockGenerator_Error demonstrates error testing with mocks.
func TestMockGenerator_Error(t *testing.T) {
	mock := &MockGenerator{
		Err: sql.ErrConnDone,
	}

	_, _, _, err := mock.GenerateThumbnailAndHashes(nil)
	if err != sql.ErrConnDone {
		t.Errorf("expected sql.ErrConnDone, got %v", err)
	}
}

// BenchmarkMockGenerator vs real generation shows the performance difference.
func BenchmarkMockGenerator(b *testing.B) {
	mock := &MockGenerator{
		Thumbnail: bytes.NewBuffer([]byte("fake-thumbnail-data")),
		MD5:       &sql.NullString{String: "abc123", Valid: true},
		PHash:     &sql.NullInt64{Int64: 12345, Valid: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := mock.GenerateThumbnailAndHashes(nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
