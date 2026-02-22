// Package testutil provides helper functions for benchmarking the HTTP cache
package testutil

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// LoadTestDatabase decompresses testdata/sfpg.db.gz and returns initialized *dbconnpool.DbSQLConnPool
// This helper loads the real test dataset for benchmark use (Approach #1: Real Data)
func LoadTestDatabase(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()

	// Open testdata/sfpg.db.gz
	gzPath := filepath.Join("testdata", "sfpg.db.gz")
	gzFile, err := os.Open(gzPath)
	if err != nil {
		t.Fatalf("failed to open testdata/sfpg.db.gz: %v", err)
	}
	defer gzFile.Close()

	// Decompress to temporary file
	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	tmpFile, err := os.CreateTemp("", "sfpg-bench-*.db")
	if err != nil {
		t.Fatalf("failed to create temporary database file: %v", err)
	}
	dbPath := tmpFile.Name()

	_, err = io.Copy(tmpFile, gzReader)
	if err != nil {
		tmpFile.Close()
		os.Remove(dbPath)
		t.Fatalf("failed to decompress database: %v", err)
	}
	tmpFile.Close()

	// Cleanup temporary database file after test
	t.Cleanup(func() {
		os.Remove(dbPath)
		os.Remove(dbPath + "-shm")
		os.Remove(dbPath + "-wal")
	})

	// Open SQLite connection via dbconnpool with minimal config
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := dbconnpool.Config{
		DriverName:         "sqlite",
		ReadOnly:           false,
		MaxConnections:     5,
		MinIdleConnections: 1,
	}
	dsn := "file:" + dbPath + "?cache=shared&mode=rwc"
	connPool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, config)
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("failed to create database connection pool: %v", err)
	}

	return connPool
}

// CaptureGalleryResponse gets real HTTP response from handler (Approach #1)
// Note: Implementation requires actual app instance and test setup
func CaptureGalleryResponse(t *testing.T, galleryID string, encoding string) []byte {
	t.Helper()

	// Placeholder for now - full implementation requires:
	// 1. Create app instance with database
	// 2. Create test auth cookie
	// 3. Make GET request: /gallery/{galleryID}
	// 4. Set Accept-Encoding: {encoding} (gzip, br, or identity)
	// 5. Capture response body
	// 6. Return decompressed bytes

	// TODO: Implement with actual app and request handler
	return []byte{}
}

// GenerateGalleryHTML creates synthetic HTML response with controlled size (Approach #2).
// HTML content uses string concatenation with loops (rather than templates) because:
// - This is a benchmark utility for generating variable-size payloads
// - Dynamic size generation using loops cannot be expressed naturally in templates
// - Performance-critical path: string building must be as fast as possible
// - No conditional rendering or complex template logic needed
// - Test-only utility that generates realistic HTML for HTTP cache benchmarking
func GenerateGalleryHTML(sizeBytes int) string {
	// Start with basic HTML structure
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Gallery</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .gallery { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 20px; }
        .gallery-item { border: 1px solid #ddd; padding: 10px; border-radius: 5px; }
        .gallery-item img { width: 100%; height: auto; }
    </style>
</head>
<body>
    <h1>Gallery</h1>
    <div class="gallery">
`

	// Add image entries until target size is reached
	// Each image entry is approximately 150 bytes
	imageTemplate := `        <div class="gallery-item">
            <img src="/images/photo-%d.jpg" alt="Gallery photo %d" />
            <p>Gallery photo %d - High quality image for your collection</p>
        </div>
`

	imageNum := 1
	for len(html) < sizeBytes {
		html += fmt.Sprintf(imageTemplate, imageNum, imageNum, imageNum)
		imageNum++
	}

	// Close HTML structure
	html += `    </div>
</body>
</html>`

	// Trim to exact size if necessary
	if len(html) > sizeBytes {
		html = html[:sizeBytes]
	}

	return html
}

// GenerateRandomBytes generates random data of exact size
func GenerateRandomBytes(sizeBytes int) []byte {
	data := make([]byte, sizeBytes)
	_, err := rand.Read(data)
	if err != nil {
		// Fallback: fill with deterministic data if crypto/rand.Read fails
		for i := 0; i < sizeBytes; i++ {
			data[i] = byte(i % 256)
		}
	}
	return data
}

// MakeHTTPCacheEntry builds a cache entry for benchmark seeding
func MakeHTTPCacheEntry(path string, payload []byte, encoding string) *cachelite.HTTPCacheEntry {
	// Compress payload based on encoding parameter
	var compressedPayload []byte

	switch encoding {
	case "gzip":
		var buf bytes.Buffer
		gzWriter := gzip.NewWriter(&buf)
		gzWriter.Write(payload)
		gzWriter.Close()
		compressedPayload = buf.Bytes()

	case "identity", "":
		// No compression for identity encoding
		compressedPayload = payload

	default:
		// Default to uncompressed for unknown encodings
		compressedPayload = payload
	}

	// Create hash of original payload for ETag
	hash := md5.Sum(payload)
	etag := fmt.Sprintf(`"%x"`, hash)

	// Build cache entry with proper field mapping to HTTPCacheEntry
	entry := &cachelite.HTTPCacheEntry{
		Key:           path + "|" + encoding,
		Method:        "GET",
		Path:          path,
		QueryString:   sql.NullString{},
		Encoding:      encoding,
		Status:        200,
		ContentType:   sql.NullString{String: "text/html; charset=utf-8", Valid: true},
		CacheControl:  sql.NullString{String: "public, max-age=3600", Valid: true},
		ETag:          sql.NullString{String: etag, Valid: true},
		LastModified:  sql.NullString{String: time.Now().Format(time.RFC1123), Valid: true},
		Vary:          sql.NullString{String: "Accept-Encoding", Valid: true},
		Body:          compressedPayload,
		ContentLength: sql.NullInt64{Int64: int64(len(compressedPayload)), Valid: true},
		CreatedAt:     time.Now().Unix(),
	}

	return entry
}
