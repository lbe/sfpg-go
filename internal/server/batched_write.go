package server

import (
	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/server/files"
)

// BatchedWrite is a union type for all high-volume database writes.
// Exactly one field should be non-nil per instance.
type BatchedWrite struct {
	File        *files.File                        // File metadata + EXIF + thumbnails
	InvalidFile *gallerydb.UpsertInvalidFileParams // Invalid file tracking
	CacheEntry  *cachelite.HTTPCacheEntry          // HTTP cache entries
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

	if bw.InvalidFile != nil {
		return int64(len(bw.InvalidFile.Path)) + 128
	}

	if bw.CacheEntry != nil {
		size := int64(len(bw.CacheEntry.Body))
		return size + 256
	}

	return overhead
}
