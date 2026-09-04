package cachelite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// testPlainHTML returns a small HTML snippet suitable for compression tests.
func testPlainHTML(size int) []byte {
	head := "<html><body>"
	tail := "</body></html>"
	bodyLen := max(size-len(head)-len(tail), 0)
	body := strings.Repeat("x", bodyLen)
	return []byte(head + body + tail)
}

func TestFinalizeForStorage_compressesHTML(t *testing.T) {
	// Use a ~3 KB HTML body so compression is worthwhile.
	plain := testPlainHTML(3000)

	entry := &HTTPCacheEntry{Body: plain}
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("FinalizeForStorage: %v", err)
	}

	// Default write codec is "zstd-1" — expect zstd magic prefix (0x28 0xB5 0x2F 0xFD).
	reg, err := getRegistry()
	if err != nil {
		t.Fatal(err)
	}
	codec := reg.Match(entry.Body)
	if codec == nil {
		t.Fatal("compressed body has no recognized magic prefix")
	}
	if codec.ID() != "zstd-1" {
		t.Fatalf("got codec %q, want zstd-1", codec.ID())
	}
	if len(entry.Body) >= len(plain) {
		t.Fatalf("compressed body len %d >= plain len %d", len(entry.Body), len(plain))
	}
}

func TestFinalizeForStorage_magicNoOp(t *testing.T) {
	plain := testPlainHTML(3000)

	// First compression.
	entry := &HTTPCacheEntry{Body: plain}
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("first FinalizeForStorage: %v", err)
	}
	stored := make([]byte, len(entry.Body))
	copy(stored, entry.Body)

	// Second call — should be a no-op because the body already has magic prefix.
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("second FinalizeForStorage: %v", err)
	}
	if len(entry.Body) != len(stored) {
		t.Fatalf("body length changed: %d -> %d", len(stored), len(entry.Body))
	}
	for i := range stored {
		if entry.Body[i] != stored[i] {
			t.Fatalf("body content changed at byte %d", i)
		}
	}
}

func TestFinalizeForStorage_identity(t *testing.T) {
	originalWriteCodec := func() string {
		bodyCodecMu.RLock()
		defer bodyCodecMu.RUnlock()
		return bodyWriteCodecID
	}()
	t.Cleanup(func() {
		_ = ConfigureBodyCodec(originalWriteCodec)
	})

	if err := ConfigureBodyCodec("identity"); err != nil {
		t.Fatalf("ConfigureBodyCodec(identity): %v", err)
	}

	plain := testPlainHTML(3000)
	entry := &HTTPCacheEntry{Body: plain}
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("FinalizeForStorage: %v", err)
	}

	if len(entry.Body) != len(plain) {
		t.Fatalf("identity: body len changed: %d -> %d", len(plain), len(entry.Body))
	}
	for i := range plain {
		if entry.Body[i] != plain[i] {
			t.Fatalf("identity: byte %d differs", i)
		}
	}
}

func TestFinalizeForStorage_minSize(t *testing.T) {
	// 100 bytes is below MinCompressBytes (256).
	plain := testPlainHTML(100)

	entry := &HTTPCacheEntry{Body: plain}
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("FinalizeForStorage: %v", err)
	}

	if len(entry.Body) != len(plain) {
		t.Fatalf("minSize: body len changed: %d -> %d", len(plain), len(entry.Body))
	}
	for i := range plain {
		if entry.Body[i] != plain[i] {
			t.Fatalf("minSize: byte %d differs", i)
		}
	}
}

func TestFinalizeForStorage_nilAndEmpty(t *testing.T) {
	if err := FinalizeForStorage(nil); err != nil {
		t.Fatalf("FinalizeForStorage(nil): %v", err)
	}
	if err := FinalizeForStorage(&HTTPCacheEntry{}); err != nil {
		t.Fatalf("FinalizeForStorage(empty): %v", err)
	}
}

func TestGetCacheEntry_roundtrip(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	plain := testPlainHTML(3000)
	now := time.Now().Unix()

	entry := &HTTPCacheEntry{
		Key:           "roundtrip-key",
		Method:        "GET",
		Path:          "/roundtrip",
		Status:        200,
		Body:          plain,
		ContentLength: sql.NullInt64{Int64: int64(len(plain)), Valid: true},
		CreatedAt:     now,
	}

	// Finalize before storing (simulates Z4 middleware flow).
	if err := FinalizeForStorage(entry); err != nil {
		t.Fatalf("FinalizeForStorage: %v", err)
	}

	if err := StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "roundtrip-key")
	if err != nil {
		t.Fatalf("GetCacheEntry: %v", err)
	}
	if got == nil {
		t.Fatal("GetCacheEntry returned nil")
	}

	// Body should match the original plaintext.
	if string(got.Body) != string(plain) {
		t.Fatalf("roundtrip body mismatch:\nwant %q\n got %q", string(plain), string(got.Body))
	}
	// ContentLength must remain uncompressed.
	if !got.ContentLength.Valid || got.ContentLength.Int64 != int64(len(plain)) {
		t.Fatalf("ContentLength = %v, want %d", got.ContentLength, len(plain))
	}
}

func TestGetCacheEntry_nonHTMLIdentity(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()

	body := []byte(`{"foo":"bar","baz":123}`)

	cpc, connErr := db.Get()
	if connErr != nil {
		t.Fatalf("Get connection: %v", connErr)
	}
	defer db.Put(cpc)

	if err := cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           "json-identity-key",
		Method:        "GET",
		Path:          "/json-identity",
		Status:        200,
		Body:          body,
		ContentLength: sql.NullInt64{Int64: int64(len(body)), Valid: true},
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("UpsertHttpCache: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "json-identity-key")
	if err != nil {
		t.Fatalf("GetCacheEntry: %v", err)
	}
	if got == nil {
		t.Fatal("GetCacheEntry returned nil")
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body mismatch:\nwant %q\n got %q", string(body), string(got.Body))
	}
	if !got.ContentLength.Valid || got.ContentLength.Int64 != int64(len(body)) {
		t.Fatalf("ContentLength = %v, want %d", got.ContentLength, len(body))
	}
}

func TestGetCacheEntry_legacyPlainHTML(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	now := time.Now().Unix()
	plain := testPlainHTML(500)

	// Insert plaintext HTML directly via sqlc (no FinalizeForStorage) to simulate
	// a legacy row from before codec support.
	cpc, connErr := db.Get()
	if connErr != nil {
		t.Fatalf("Get connection: %v", connErr)
	}
	defer db.Put(cpc)

	if err := cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           "legacy-plain-key",
		Method:        "GET",
		Path:          "/legacy-plain",
		Status:        200,
		Body:          plain,
		ContentLength: sql.NullInt64{Int64: int64(len(plain)), Valid: true},
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("UpsertHttpCache: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "legacy-plain-key")
	if err != nil {
		t.Fatalf("GetCacheEntry: %v", err)
	}
	if got == nil {
		t.Fatal("GetCacheEntry returned nil")
	}

	if string(got.Body) != string(plain) {
		t.Fatalf("legacy plain HTML body mismatch:\nwant %q\n got %q", string(plain), string(got.Body))
	}
}

func TestGetCacheEntry_unrecognized(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	now := time.Now().Unix()

	cpc, connErr := db.Get()
	if connErr != nil {
		t.Fatalf("Get connection: %v", connErr)
	}
	defer db.Put(cpc)

	// Insert a garbage blob that does not match any codec magic with a
	// ContentLength that does not match the body length.
	body := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	if err := cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           "garbage-key",
		Method:        "GET",
		Path:          "/garbage",
		Status:        200,
		Body:          body,
		ContentLength: sql.NullInt64{Int64: int64(len(body)) + 999, Valid: true},
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("UpsertHttpCache: %v", err)
	}

	_, err := GetCacheEntry(ctx, db, "garbage-key")
	if err == nil {
		t.Fatal("expected error for unrecognized cache body")
	}
	if !errors.Is(err, ErrUnrecognizedCacheBody) {
		t.Fatalf("expected ErrUnrecognizedCacheBody, got %v", err)
	}
}

func TestGetCacheEntry_lengthMismatch(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	now := time.Now().Unix()
	// Must be >= MinCompressBytes (256) so zstd actually compresses.
	plain := testPlainHTML(500)

	// Compress with zstd to create a valid stored-form body.
	reg, err := getRegistry()
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := reg.EncodeWith("zstd-1", plain)
	if err != nil {
		t.Fatalf("EncodeWith: %v", err)
	}

	cpc, connErr := db.Get()
	if connErr != nil {
		t.Fatalf("Get connection: %v", connErr)
	}
	defer db.Put(cpc)

	// Store with a ContentLength that does not match (larger than actual).
	if upsertErr := cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           "length-mismatch-key",
		Method:        "GET",
		Path:          "/length-mismatch",
		Status:        200,
		Body:          compressed,
		ContentLength: sql.NullInt64{Int64: int64(len(plain)) + 999, Valid: true},
		CreatedAt:     now,
	}); upsertErr != nil {
		t.Fatalf("UpsertHttpCache: %v", upsertErr)
	}

	_, err = GetCacheEntry(ctx, db, "length-mismatch-key")
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
	// The zstd codec catches the mismatch internally in Decompress,
	// returning a descriptive error rather than ErrUnrecognizedCacheBody.
	if errors.Is(err, ErrUnrecognizedCacheBody) {
		return
	}
	if !strings.Contains(err.Error(), "length mismatch") {
		t.Fatalf("expected length mismatch error, got %v", err)
	}
}

func TestConfigureBodyCodec_unknownID(t *testing.T) {
	originalWriteCodec := func() string {
		bodyCodecMu.RLock()
		defer bodyCodecMu.RUnlock()
		return bodyWriteCodecID
	}()
	t.Cleanup(func() {
		_ = ConfigureBodyCodec(originalWriteCodec)
	})

	// After the failure, bodyWriteCodecID must still be the original.
	if err := ConfigureBodyCodec("bogus-codec"); err == nil {
		t.Fatal("expected error for unknown codec ID")
	}

	bodyCodecMu.RLock()
	defer bodyCodecMu.RUnlock()
	if bodyWriteCodecID != originalWriteCodec {
		t.Fatalf("bodyWriteCodecID changed from %q to %q after failed ConfigureBodyCodec",
			originalWriteCodec, bodyWriteCodecID)
	}
}

func TestConfigureBodyCodec_emptyIsDefault(t *testing.T) {
	originalWriteCodec := func() string {
		bodyCodecMu.RLock()
		defer bodyCodecMu.RUnlock()
		return bodyWriteCodecID
	}()
	t.Cleanup(func() {
		_ = ConfigureBodyCodec(originalWriteCodec)
	})

	if err := ConfigureBodyCodec(""); err != nil {
		t.Fatalf("ConfigureBodyCodec(''): %v", err)
	}

	bodyCodecMu.RLock()
	defer bodyCodecMu.RUnlock()
	if bodyWriteCodecID != "zstd-1" {
		t.Fatalf("empty string -> bodyWriteCodecID = %q, want zstd-1", bodyWriteCodecID)
	}
}

func TestValidateWriteCodecID(t *testing.T) {
	if err := ValidateWriteCodecID(""); err != nil {
		t.Fatalf("ValidateWriteCodecID(''): %v", err)
	}
	if err := ValidateWriteCodecID("identity"); err != nil {
		t.Fatalf("ValidateWriteCodecID('identity'): %v", err)
	}
	if err := ValidateWriteCodecID("zstd-1"); err != nil {
		t.Fatalf("ValidateWriteCodecID('zstd-1'): %v", err)
	}
	if err := ValidateWriteCodecID("gzip-6"); err != nil {
		t.Fatalf("ValidateWriteCodecID('gzip-6'): %v", err)
	}
	if err := ValidateWriteCodecID("bogus"); err == nil {
		t.Fatal("expected error for ValidateWriteCodecID('bogus')")
	}
}

func TestErrUnrecognizedCacheBody(t *testing.T) {
	if !errors.Is(ErrUnrecognizedCacheBody, bodycodec.ErrUnrecognizedCacheBody) {
		t.Error("ErrUnrecognizedCacheBody should wrap bodycodec.ErrUnrecognizedCacheBody")
	}
}
