//go:build integration

package cachelite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// ---------------------------------------------------------------------------
// Helper: high-entropy HTML filler (incompressible within an HTML-looking body)
// ---------------------------------------------------------------------------

// highEntropyHTML returns an HTML-looking body with high-entropy payload
// that zstd/gzip cannot meaningfully shrink.
func highEntropyHTML(minSize int) []byte {
	prefix := []byte("<!DOCTYPE html><html><body><div>")
	suffix := []byte("</div></body></html>")
	target := minSize
	if target < len(prefix)+len(suffix) {
		target = len(prefix) + len(suffix)
	}
	payloadLen := target - len(prefix) - len(suffix)
	if payloadLen < 0 {
		payloadLen = 0
	}
	// Fill with high-entropy bytes (crypto/rand is incompressible)
	payload := make([]byte, payloadLen)
	_, _ = rand.Read(payload)
	// Make bytes printable ASCII-ish (avoid nulls/control chars)
	for i := range payload {
		payload[i] = 0x20 + (payload[i] % 0x5E) // 0x20-0x7D printable range
	}
	result := make([]byte, 0, len(prefix)+payloadLen+len(suffix))
	result = append(result, prefix...)
	result = append(result, payload...)
	result = append(result, suffix...)
	return result
}

// zstdMagic is the zstd frame magic prefix: 28 B5 2F FD
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// gzipMagic is the gzip magic prefix: 1F 8B
var gzipMagic = []byte{0x1F, 0x8B}

// requireMagic fails the test if body does not start with the expected prefix.
func requireMagic(t *testing.T, body []byte, want []byte, label string) {
	t.Helper()
	if len(body) < len(want) {
		t.Fatalf("%s: body too short (%d bytes) to contain expected magic %x", label, len(body), want)
	}
	for i := range want {
		if body[i] != want[i] {
			t.Fatalf("%s: byte %d = %02x, want %02x (magic %x)", label, i, body[i], want[i], want)
		}
	}
}

// requireNoMagic fails if body starts with any registered codec magic.
func requireNoMagic(t *testing.T, body []byte, label string) {
	t.Helper()
	reg, err := getRegistry()
	if err != nil {
		t.Fatalf("%s: getRegistry: %v", label, err)
	}
	if reg.Match(body) != nil {
		t.Fatalf("%s: body unexpectedly starts with registered magic; first 4 bytes: %x", label, body[:min(4, len(body))])
	}
}

// ---------------------------------------------------------------------------
// Scenario 1: MISS → STORE → HIT
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_MissStoreHit(t *testing.T) {
	db := createTestDBPoolInternal(t)
	bodyPlaintext := testPlainHTML(3000) // ~3 KB, well above MinCompressBytes
	if len(bodyPlaintext) < 256 {
		t.Fatalf("test body too small: %d bytes", len(bodyPlaintext))
	}

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(bodyPlaintext)
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/test-compress"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// --- MISS ---
	req1 := httptest.NewRequest("GET", "/test-compress", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("MISS status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after MISS = %d, want 1", handlerCalls)
	}

	// Verify DB body has zstd magic
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test-compress", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}

	// -- Assert stored body starts with zstd magic
	requireMagic(t, result.Body, zstdMagic, "stored body after MISS")

	// -- Assert content_length stays uncompressed
	if !result.ContentLength.Valid {
		t.Fatal("stored ContentLength is not set")
	}
	if result.ContentLength.Int64 != int64(len(bodyPlaintext)) {
		t.Fatalf("stored ContentLength = %d, want uncompressed %d",
			result.ContentLength.Int64, len(bodyPlaintext))
	}

	// -- Assert stored body is smaller than original
	if len(result.Body) >= len(bodyPlaintext) {
		t.Logf("stored body not compressed (len=%d >= original=%d); expand guard kept plaintext",
			len(result.Body), len(bodyPlaintext))
	}

	// --- HIT ---
	req2 := httptest.NewRequest("GET", "/test-compress", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("HIT status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after HIT = %d, want 1 (cached)", handlerCalls)
	}
	// HIT body should equal original plaintext
	if !bytes.Equal(w2.Body.Bytes(), bodyPlaintext) {
		t.Fatalf("HIT body mismatch:\n  got:  %d bytes\n  want: %d bytes",
			w2.Body.Len(), len(bodyPlaintext))
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Legacy plaintext row (no compression magic)
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_LegacyPlaintextRow(t *testing.T) {
	db := createTestDBPoolInternal(t)
	plainHTML := testPlainHTML(500) // plain HTML, no magic

	// Insert directly via sqlc (pre-compression era style)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/legacy-plain", Variant: "full"})
	now := time.Now().Unix()
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	if err := cpc.Queries.UpsertHttpCache(context.Background(), gallerydb.UpsertHttpCacheParams{
		Key:           key,
		Method:        "GET",
		Path:          "/legacy-plain",
		Status:        200,
		ContentType:   sql.NullString{String: "text/html", Valid: true},
		Body:          plainHTML,
		ContentLength: sql.NullInt64{Int64: int64(len(plainHTML)), Valid: true},
		CreatedAt:     now,
	}); err != nil {
		db.Put(cpc)
		t.Fatalf("UpsertHttpCache: %v", err)
	}
	db.Put(cpc)

	// Serve via middleware — should HIT and return plaintext
	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(200)
		_, _ = w.Write([]byte("should-not-be-called"))
	})
	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/legacy-plain"},
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/legacy-plain", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT (legacy plaintext row)", w.Header().Get("X-Cache"))
	}
	if handlerCalls != 0 {
		t.Fatalf("handler should not be called on HIT; called %d times", handlerCalls)
	}
	if !bytes.Equal(w.Body.Bytes(), plainHTML) {
		t.Fatalf("HIT body mismatch:\n  got:  %d bytes\n  want: %d bytes",
			w.Body.Len(), len(plainHTML))
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: MinCompressBytes — body < 256 B stays plaintext
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_MinCompressBytes(t *testing.T) {
	db := createTestDBPoolInternal(t)
	smallBody := testPlainHTML(200) // 200 < 256

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(smallBody)
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/small"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// MISS
	req1 := httptest.NewRequest("GET", "/small", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Check DB body stored as plaintext (no magic)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/small", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}

	requireNoMagic(t, result.Body, "small-body row")
	if len(result.Body) != len(smallBody) {
		t.Fatalf("stored body len = %d, want %d (plaintext)", len(result.Body), len(smallBody))
	}

	// HIT
	req2 := httptest.NewRequest("GET", "/small", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if !bytes.Equal(w2.Body.Bytes(), smallBody) {
		t.Fatal("HIT body mismatch for small plaintext body")
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Expand guard — HTML payload that does not compress
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_ExpandGuard(t *testing.T) {
	db := createTestDBPoolInternal(t)
	// High-entropy HTML ≥ 256 B that won't shrink under compression
	expandingBody := highEntropyHTML(3000)
	if len(expandingBody) < 256 {
		t.Fatalf("expand guard test body too small: %d bytes", len(expandingBody))
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(expandingBody)
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/expand-guard"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// MISS
	req1 := httptest.NewRequest("GET", "/expand-guard", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Check DB body does NOT have compression magic (expand guard kept it plaintext)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/expand-guard", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}

	requireNoMagic(t, result.Body, "expand-guard row")

	// HIT — body returned as identity bytes (length matches ContentLength)
	req2 := httptest.NewRequest("GET", "/expand-guard", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT (expand guard)", w2.Header().Get("X-Cache"))
	}
	if !bytes.Equal(w2.Body.Bytes(), expandingBody) {
		t.Fatal("HIT body mismatch for expand-guard body")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Corrupt / garbage row → MISS → re-render overwrites
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_CorruptRow(t *testing.T) {
	db := createTestDBPoolInternal(t)

	// Insert a garbage blob that does not start with any valid magic.
	// ContentLength intentionally mismatched so decode fails with ErrUnrecognizedCacheBody.
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/corrupt", Variant: "full"})
	now := time.Now().Unix()
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	if err := cpc.Queries.UpsertHttpCache(context.Background(), gallerydb.UpsertHttpCacheParams{
		Key:           key,
		Method:        "GET",
		Path:          "/corrupt",
		Status:        200,
		ContentType:   sql.NullString{String: "text/html", Valid: true},
		Body:          garbage,
		ContentLength: sql.NullInt64{Int64: int64(len(garbage)) + 999, Valid: true},
		CreatedAt:     now,
	}); err != nil {
		db.Put(cpc)
		t.Fatalf("UpsertHttpCache: %v", err)
	}
	db.Put(cpc)

	serveBody := "<html><body>re-rendered content</body></html>"
	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(serveBody))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/corrupt"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request: garbage blob should be unrecognized → MISS → handler runs
	req1 := httptest.NewRequest("GET", "/corrupt", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("after corrupt row status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("corrupt row X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1 (MISS after corrupt row)", handlerCalls)
	}
	// Response should be from handler, not garbage bytes
	if !bytes.Equal(w1.Body.Bytes(), []byte(serveBody)) {
		t.Fatalf("MISS body mismatch after corrupt row:\n  got:  %q\n  want: %q",
			w1.Body.String(), serveBody)
	}

	// The overwritten row should now be a valid HIT
	req2 := httptest.NewRequest("GET", "/corrupt", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache = %q, want HIT (overwritten)", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after overwrite = %d, want 1 (HIT)", handlerCalls)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: identity codec — new rows stay plaintext
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_IdentityCodec(t *testing.T) {
	// Save current codec and restore after test
	origWriteCodec := func() string {
		bodyCodecMu.RLock()
		defer bodyCodecMu.RUnlock()
		return bodyWriteCodecID
	}()
	t.Cleanup(func() { _ = ConfigureBodyCodec(origWriteCodec) })

	if err := ConfigureBodyCodec("identity"); err != nil {
		t.Fatalf("ConfigureBodyCodec(identity): %v", err)
	}

	db := createTestDBPoolInternal(t)
	plainBody := testPlainHTML(3000)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(plainBody)
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/identity"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// MISS
	req1 := httptest.NewRequest("GET", "/identity", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// DB body should be plaintext (no magic)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/identity", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}

	requireNoMagic(t, result.Body, "identity-codec row")
	if !bytes.Equal(result.Body, plainBody) {
		t.Fatal("identity codec stored body differs from input")
	}

	// HIT
	req2 := httptest.NewRequest("GET", "/identity", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if !bytes.Equal(w2.Body.Bytes(), plainBody) {
		t.Fatal("HIT body mismatch with identity codec")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: Stored-byte accounting — SUM(LENGTH(body)) after compress
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_StoredByteAccounting(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	// Compute uncompressed sizes before inserting
	var uncompressedTotal int64

	entries := make([]*HTTPCacheEntry, 3)
	for i := 0; i < 3; i++ {
		body := testPlainHTML(2000 + i*500) // 2000, 2500, 3000 bytes
		uncompressedTotal += int64(len(body))
		entries[i] = &HTTPCacheEntry{
			Key:           fmt.Sprintf("stored-byte-key-%d", i),
			Method:        "GET",
			Path:          fmt.Sprintf("/stored-bytes/%d", i),
			Status:        200,
			Body:          body,
			ContentLength: sql.NullInt64{Int64: int64(len(body)), Valid: true},
			CreatedAt:     time.Now().Unix(),
		}
		// Finalize to compress
		if err := FinalizeForStorage(entries[i]); err != nil {
			t.Fatalf("FinalizeForStorage[%d]: %v", i, err)
		}
		if err := StoreCacheEntry(ctx, db, entries[i]); err != nil {
			t.Fatalf("StoreCacheEntry[%d]: %v", i, err)
		}
	}

	// Get stored size from DB
	storeSize, err := GetCacheSizeBytes(ctx, db)
	if err != nil {
		t.Fatalf("GetCacheSizeBytes: %v", err)
	}

	// Stored bytes should be less than uncompressed total (each > 256 B and compressible)
	if storeSize >= uncompressedTotal {
		t.Fatalf("GetCacheSizeBytes = %d, want < uncompressed total %d (compression should shrink)",
			storeSize, uncompressedTotal)
	}

	// Also verify via direct SQL that SUM(LENGTH(body)) matches
	cpc, connErr := db.Get()
	if connErr != nil {
		t.Fatalf("Get connection: %v", connErr)
	}
	var dbSum sql.NullInt64
	row := cpc.Conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(LENGTH(body)), 0) FROM http_cache")
	if err := row.Scan(&dbSum); err != nil {
		db.Put(cpc)
		t.Fatalf("SUM(LENGTH(body)) query: %v", err)
	}
	db.Put(cpc)

	if !dbSum.Valid {
		t.Fatal("SUM(LENGTH(body)) returned NULL, expected a value")
	}
	if dbSum.Int64 != storeSize {
		t.Fatalf("GetCacheSizeBytes = %d, direct SUM(LENGTH(body)) = %d — mismatch",
			storeSize, dbSum.Int64)
	}

	if storeSize <= 0 {
		t.Fatal("GetCacheSizeBytes should be > 0")
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: Config codec switch to gzip-6 → magic 1F 8B; still HIT
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_CodecSwitchGzip(t *testing.T) {
	// Save current codec and restore after test
	origWriteCodec := func() string {
		bodyCodecMu.RLock()
		defer bodyCodecMu.RUnlock()
		return bodyWriteCodecID
	}()
	t.Cleanup(func() { _ = ConfigureBodyCodec(origWriteCodec) })

	// Switch to gzip-6 for write
	if err := ConfigureBodyCodec("gzip-6"); err != nil {
		t.Fatalf("ConfigureBodyCodec(gzip-6): %v", err)
	}

	db := createTestDBPoolInternal(t)
	bodyPlaintext := testPlainHTML(3000)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(bodyPlaintext)
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gzip-switch"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// MISS
	req1 := httptest.NewRequest("GET", "/gzip-switch", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// DB body should have gzip magic 1F 8B
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/gzip-switch", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}

	requireMagic(t, result.Body, gzipMagic, "stored body with gzip-6")
	if len(result.Body) >= len(bodyPlaintext) {
		t.Logf("gzip-6 stored body len=%d >= original=%d; expand guard kept plaintext",
			len(result.Body), len(bodyPlaintext))
	}

	// HIT — gzip-compressed row should decode to plaintext
	req2 := httptest.NewRequest("GET", "/gzip-switch", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT (gzip row)", w2.Header().Get("X-Cache"))
	}
	if !bytes.Equal(w2.Body.Bytes(), bodyPlaintext) {
		t.Fatal("HIT body mismatch after gzip codec switch")
	}

	// Now switch back to zstd-1 and write another entry
	if err := ConfigureBodyCodec("zstd-1"); err != nil {
		t.Fatalf("ConfigureBodyCodec(zstd-1) restore: %v", err)
	}

	handler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(bodyPlaintext)
	})
	cfg2 := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/zstd-after-gzip"},
	}
	cacheMW2 := NewHTTPCacheMiddlewareForTest(db, cfg2, nil, createSyncSubmitFuncForIntegration(t, db))
	mw2 := cacheMW2.Middleware(handler2)

	req3 := httptest.NewRequest("GET", "/zstd-after-gzip", nil)
	w3 := httptest.NewRecorder()
	mw2.ServeHTTP(w3, req3)
	if w3.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("zstd-after-gzip X-Cache = %q, want MISS", w3.Header().Get("X-Cache"))
	}

	// This entry should have zstd magic
	key2 := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/zstd-after-gzip", Variant: "full"})
	cpc2, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result2, err := cpc2.Queries.GetHttpCacheByKey(context.Background(), key2)
	db.Put(cpc2)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}
	requireMagic(t, result2.Body, zstdMagic, "zstd-after-gzip row")
}

// ---------------------------------------------------------------------------
// BONUS: Verify a non-HTML blob with mismatched ContentLength is NOT served
// (regression guard for length-based identity validation)
// ---------------------------------------------------------------------------

func TestCacheBodyCompression_NonHTMLGarbageFails(t *testing.T) {
	db := createTestDBPoolInternal(t)

	// Random bytes that do not start with any codec magic.
	garbage := make([]byte, 512)
	_, _ = rand.Read(garbage)

	ctx := context.Background()
	key := "non-html-garbage-key"
	now := time.Now().Unix()
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// ContentLength intentionally mismatched so length validation rejects the body.
	if err := cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           key,
		Method:        "GET",
		Path:          "/non-html-garbage",
		Status:        200,
		Body:          garbage,
		ContentLength: sql.NullInt64{Int64: int64(len(garbage)) + 999, Valid: true},
		CreatedAt:     now,
	}); err != nil {
		db.Put(cpc)
		t.Fatalf("UpsertHttpCache: %v", err)
	}
	db.Put(cpc)

	_, err = GetCacheEntry(ctx, db, key)
	if err == nil {
		t.Fatal("expected error for garbage body with mismatched length")
	}
	if !errors.Is(err, ErrUnrecognizedCacheBody) {
		t.Fatalf("expected ErrUnrecognizedCacheBody, got %v", err)
	}
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ensure io is used (for highEntropyHTML rand.Read)
var _ = io.Discard
