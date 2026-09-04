package server

import (
	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// flushBatchedWrites processes a unified batch within a transaction.
// It segregates by type, honors cache route strategy, handles cache eviction,
// and cleans up resources.

// cleanupBatchedWriteResources returns pooled resources to pools and clears references.
// Called from OnError callback and after successful flush.
func cleanupBatchedWriteResources(batch []BatchedWrite) {
	for i := range batch {
		bw := &batch[i]
		if bw.File != nil && bw.File.Thumbnail != nil {
			thumbnail.PutBytesBuffer(bw.File.Thumbnail)
			bw.File.Thumbnail = nil
		}
		if bw.CacheEntry != nil {
			cachelite.PutHTTPCacheEntry(bw.CacheEntry)
			bw.CacheEntry = nil
		}
		bw.File = nil
		bw.FolderIndex = nil
	}
}
