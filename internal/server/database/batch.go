package database

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"fmt"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

func init() {
	// Register concrete types stored in sqlc-generated interface{} fields.
	// The database stores timestamps as INT64 (UNIXEPOCH), so the concrete
	// runtime type is int64. Without this registration, gob panics when it
	// encounters an unregistered concrete type inside an interface{} field.
	gob.Register(int64(0))
	gob.Register(sql.NullString{})
	gob.Register(sql.NullInt64{})
	gob.Register(sql.NullFloat64{})
}

// batchedWrite is one write in a batch. Exactly one field is set.
type batchedWrite struct {
	File        *files.File
	InvalidFile *files.InvalidFile
	CacheEntry  *cachelite.HTTPCacheEntry
	FolderIndex *folderIndexRow
}

// batchedWriteWire is the gob form of batchedWrite: each write is encoded on
// its own, so a nil field stays nil. files.File has its own GobEncode for its
// *bytes.Buffer thumbnail.
type batchedWriteWire struct {
	FileData        []byte
	InvalidFileData []byte
	CacheEntryData  []byte
	FolderIndexData []byte
}

// GobEncode encodes bw for the overflow queue.
func (bw batchedWrite) GobEncode() ([]byte, error) {
	var w batchedWriteWire
	var err error
	if bw.File != nil {
		if w.FileData, err = encodeGob(bw.File); err != nil {
			return nil, fmt.Errorf("gob encode File: %w", err)
		}
	}
	if bw.InvalidFile != nil {
		if w.InvalidFileData, err = encodeGob(bw.InvalidFile); err != nil {
			return nil, fmt.Errorf("gob encode InvalidFile: %w", err)
		}
	}
	if bw.CacheEntry != nil {
		if w.CacheEntryData, err = encodeGob(bw.CacheEntry); err != nil {
			return nil, fmt.Errorf("gob encode CacheEntry: %w", err)
		}
	}
	if bw.FolderIndex != nil {
		if w.FolderIndexData, err = encodeGob(bw.FolderIndex); err != nil {
			return nil, fmt.Errorf("gob encode FolderIndex: %w", err)
		}
	}
	return encodeGob(w)
}

// GobDecode decodes bw from the overflow queue.
func (bw *batchedWrite) GobDecode(data []byte) error {
	var w batchedWriteWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}
	if len(w.FileData) > 0 {
		bw.File = new(files.File)
		if err := decodeGob(w.FileData, bw.File); err != nil {
			return fmt.Errorf("gob decode File: %w", err)
		}
	}
	if len(w.InvalidFileData) > 0 {
		bw.InvalidFile = new(files.InvalidFile)
		if err := decodeGob(w.InvalidFileData, bw.InvalidFile); err != nil {
			return fmt.Errorf("gob decode InvalidFile: %w", err)
		}
	}
	if len(w.CacheEntryData) > 0 {
		bw.CacheEntry = new(cachelite.HTTPCacheEntry)
		if err := decodeGob(w.CacheEntryData, bw.CacheEntry); err != nil {
			return fmt.Errorf("gob decode CacheEntry: %w", err)
		}
	}
	if len(w.FolderIndexData) > 0 {
		bw.FolderIndex = new(folderIndexRow)
		if err := decodeGob(w.FolderIndexData, bw.FolderIndex); err != nil {
			return fmt.Errorf("gob decode FolderIndex: %w", err)
		}
	}
	return nil
}

func encodeGob(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeGob(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// size estimates the memory bw holds, for the batch byte limit.
func (bw batchedWrite) size() int64 {
	switch {
	case bw.File != nil:
		const fileOverhead = 512 // File struct fields
		if bw.File.Thumbnail != nil {
			return int64(bw.File.Thumbnail.Cap()) + fileOverhead
		}
		return fileOverhead
	case bw.InvalidFile != nil:
		return 256
	case bw.CacheEntry != nil:
		return int64(len(bw.CacheEntry.Body)) + 256
	case bw.FolderIndex != nil:
		return 128
	}
	return 64
}

// release returns the batch's pooled buffers to their pools. It runs only
// after a commit, so a failed batch keeps its thumbnails for the retry.
func release(batch []batchedWrite) {
	for i := range batch {
		bw := &batch[i]
		if bw.File != nil && bw.File.Thumbnail != nil {
			thumbnail.PutBytesBuffer(bw.File.Thumbnail)
			bw.File.Thumbnail = nil
		}
		if bw.CacheEntry != nil {
			cachelite.PutHTTPCacheEntry(bw.CacheEntry)
		}
		*bw = batchedWrite{}
	}
}
