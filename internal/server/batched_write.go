package server

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"fmt"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/files"
)

func init() {
	// Register concrete types stored in sqlc-generated interface{} fields.
	// The database stores timestamps as INT64 (UNIXEPOCH), so the concrete
	// runtime type is int64. Without this registration, gob panics when it
	// encounters an unregistered concrete type inside an interface{} field.
	//
	// sql.NullXxx types are also registered defensively: they are struct
	// fields with declared type sql.NullString/etc. (not inside interface{}),
	// so gob can encode them without registration. However, if any future
	// code path stores a sql.NullXxx value in an interface{} field, the
	// registration will already be in place.
	gob.Register(int64(0))
	gob.Register(sql.NullString{})
	gob.Register(sql.NullInt64{})
	gob.Register(sql.NullFloat64{})
}

// BatchedWrite is a union type for all high-volume database writes.
// Exactly one field should be non-nil per instance.
type BatchedWrite struct {
	File       *files.File               // File metadata + EXIF + thumbnails
	CacheEntry *cachelite.HTTPCacheEntry // HTTP cache entries
}

// batchedWriteWire is the gob-safe wire format for BatchedWrite.
// Separating File and CacheEntry into distinct byte blobs ensures that
// gob encoding of one does not affect the other, and allows each to be
// nil independently.
//
// files.File has its own GobEncode/GobDecode that handles the
// *bytes.Buffer Thumbnail field (which cannot be gob-encoded directly
// because bytes.Buffer has no exported fields).
type batchedWriteWire struct {
	FileData       []byte // gob-encoded files.File (has GobEncode handling Thumbnail)
	CacheEntryData []byte // gob-encoded cachelite.HTTPCacheEntry (nil if file write)
}

// GobEncode serializes BatchedWrite into a gob-safe wire format.
// Since files.File now has its own GobEncode/GobDecode handling the
// *bytes.Buffer Thumbnail, we can encode it directly without copying
// or mutating the caller's object.
func (bw BatchedWrite) GobEncode() ([]byte, error) {
	var w batchedWriteWire

	if bw.File != nil {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(bw.File); err != nil {
			return nil, fmt.Errorf("gob encode File: %w", err)
		}
		w.FileData = buf.Bytes()
	}

	if bw.CacheEntry != nil {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(bw.CacheEntry); err != nil {
			return nil, fmt.Errorf("gob encode CacheEntry: %w", err)
		}
		w.CacheEntryData = buf.Bytes()
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GobDecode deserializes BatchedWrite from the gob-safe wire format,
// reconstructing the files.File (including its Thumbnail) and cache entry
// from their separately-encoded blobs.
func (bw *BatchedWrite) GobDecode(data []byte) error {
	var w batchedWriteWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}

	if len(w.FileData) > 0 {
		var f files.File
		if err := gob.NewDecoder(bytes.NewReader(w.FileData)).Decode(&f); err != nil {
			return fmt.Errorf("gob decode File: %w", err)
		}
		bw.File = &f
	}

	if len(w.CacheEntryData) > 0 {
		var e cachelite.HTTPCacheEntry
		if err := gob.NewDecoder(bytes.NewReader(w.CacheEntryData)).Decode(&e); err != nil {
			return fmt.Errorf("gob decode CacheEntry: %w", err)
		}
		bw.CacheEntry = &e
	}

	return nil
}

// Size returns estimated memory cost in bytes for batch size limiting.
func (bw BatchedWrite) Size() int64 {
	const overhead = 64 // struct pointer overhead

	if bw.File != nil {
		const fileOverhead = 512 // File struct fields
		if bw.File.Thumbnail != nil {
			return int64(bw.File.Thumbnail.Cap()) + fileOverhead
		}
		return fileOverhead
	}

	if bw.CacheEntry != nil {
		size := int64(len(bw.CacheEntry.Body))
		return size + 256
	}

	return overhead
}
