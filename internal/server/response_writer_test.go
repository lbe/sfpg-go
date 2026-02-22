package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestResponseWriter_SetETag tests setting and retrieving ETag
func TestResponseWriter_SetETag(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	etag := `"abc123-456"`
	rw.SetETag(etag)

	if rw.GetETag() != etag {
		t.Errorf("GetETag() = %q, want %q", rw.GetETag(), etag)
	}
}

// TestResponseWriter_SetLastModified tests setting and retrieving Last-Modified
func TestResponseWriter_SetLastModified(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	testTime := time.Date(2025, 12, 5, 10, 30, 0, 0, time.UTC)
	rw.SetLastModified(testTime)

	expected := "Fri, 05 Dec 2025 10:30:00 GMT"
	if rw.GetLastModified() != expected {
		t.Errorf("GetLastModified() = %q, want %q", rw.GetLastModified(), expected)
	}
}

// TestResponseWriter_GetContentType tests extracting Content-Type from headers
func TestResponseWriter_GetContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	contentType := "text/html; charset=utf-8"
	rw.Header().Set("Content-Type", contentType)

	if rw.GetContentType() != contentType {
		t.Errorf("GetContentType() = %q, want %q", rw.GetContentType(), contentType)
	}
}

// TestResponseWriter_GetCacheControl tests extracting Cache-Control from headers
func TestResponseWriter_GetCacheControl(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	cacheControl := "public, max-age=3600"
	rw.Header().Set("Cache-Control", cacheControl)

	if rw.GetCacheControl() != cacheControl {
		t.Errorf("GetCacheControl() = %q, want %q", rw.GetCacheControl(), cacheControl)
	}
}

// TestResponseWriter_WriteHeader_CapturesStatus tests that WriteHeader stores status
func TestResponseWriter_WriteHeader_CapturesStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	rw.WriteHeader(http.StatusNotFound)

	if rw.status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rw.status, http.StatusNotFound)
	}
}

// TestResponseWriter_Write_IncrementsBytes tests that Write tracks bytes written
func TestResponseWriter_Write_IncrementsBytes(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	data := []byte("Hello, World!")
	n, err := rw.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if n != len(data) {
		t.Errorf("Write() returned %d bytes, want %d", n, len(data))
	}

	if rw.bytesWritten != len(data) {
		t.Errorf("bytesWritten = %d, want %d", rw.bytesWritten, len(data))
	}
}

// TestResponseWriter_MultipleWrites tests cumulative byte counting
func TestResponseWriter_MultipleWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	writes := [][]byte{
		[]byte("Hello"),
		[]byte(", "),
		[]byte("World!"),
	}

	totalBytes := 0
	for _, data := range writes {
		n, err := rw.Write(data)
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		totalBytes += n
	}

	if rw.bytesWritten != totalBytes {
		t.Errorf("bytesWritten = %d, want %d", rw.bytesWritten, totalBytes)
	}
}

// TestResponseWriter_SettersPreserveValues tests that all setters work correctly
func TestResponseWriter_SettersPreserveValues(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		status:         http.StatusOK,
	}

	etag := `"test-etag"`
	lastMod := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	rw.SetETag(etag)
	rw.SetLastModified(lastMod)
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-cache")

	// Verify all values are preserved
	if rw.GetETag() != etag {
		t.Errorf("GetETag() = %q, want %q", rw.GetETag(), etag)
	}
	if rw.GetLastModified() != "Wed, 01 Jan 2025 00:00:00 GMT" {
		t.Errorf("GetLastModified() = %q, want %q", rw.GetLastModified(), "Wed, 01 Jan 2025 00:00:00 GMT")
	}
	if rw.GetContentType() != "application/json" {
		t.Errorf("GetContentType() = %q, want %q", rw.GetContentType(), "application/json")
	}
	if rw.GetCacheControl() != "no-cache" {
		t.Errorf("GetCacheControl() = %q, want %q", rw.GetCacheControl(), "no-cache")
	}
}
