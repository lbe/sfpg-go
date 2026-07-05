package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/server/files"
)

func TestBatchedWrite_DqueRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-test")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	q, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	t.Cleanup(func() { q.Close() })

	// Enqueue all variants
	items := []struct {
		name string
		bw   BatchedWrite
	}{
		{
			name: "file_with_thumbnail",
			bw:   BatchedWrite{File: fullyPopulatedFilesFile()},
		},
		{
			name: "file_without_thumbnail",
			bw: BatchedWrite{File: func() *files.File {
				f := fullyPopulatedFilesFile()
				f.Thumbnail = nil
				return f
			}()},
		},
		{
			name: "cache_entry",
			bw:   BatchedWrite{CacheEntry: fullyPopulatedCacheEntry()},
		},
		{
			name: "empty",
			bw:   BatchedWrite{},
		},
	}

	for _, item := range items {
		t.Run("enqueue_"+item.name, func(t *testing.T) {
			copy := item.bw
			if enqErr := q.Enqueue(&copy); enqErr != nil {
				t.Fatalf("dque.Enqueue (%s): %v", item.name, enqErr)
			}
		})
	}

	// Dequeue and verify each
	for _, item := range items {
		t.Run("dequeue_"+item.name, func(t *testing.T) {
			dequeued, deqErr := q.Dequeue()
			if deqErr != nil {
				t.Fatalf("dque.Dequeue (%s): %v", item.name, deqErr)
			}
			if dequeued == nil {
				t.Fatalf("dequeued item is nil (%s)", item.name)
			}

			switch {
			case item.bw.File != nil:
				if dequeued.File == nil {
					t.Fatalf("expected File, got nil (%s)", item.name)
				}
				assertFilesFileEqual(t, item.name, item.bw.File, dequeued.File)
			case item.bw.CacheEntry != nil:
				if dequeued.CacheEntry == nil {
					t.Fatalf("expected CacheEntry, got nil (%s)", item.name)
				}
				assertHTTPCacheEntryEqual(t, item.name, item.bw.CacheEntry, dequeued.CacheEntry)
			default:
				if dequeued.File != nil || dequeued.CacheEntry != nil {
					t.Fatalf("expected empty BatchedWrite, got File=%v CacheEntry=%v (%s)",
						dequeued.File != nil, dequeued.CacheEntry != nil, item.name)
				}
			}
		})
	}

	// Queue should now be empty
	_, err = q.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}

func TestBatchedWrite_DqueRoundTrip_CrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	queueDir := filepath.Join(tmpDir, "dque-crash")

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	// Phase 1: Enqueue items and close (simulating a clean shutdown with unprocessed items)
	q1, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (1st): %v", err)
	}

	fileItem := BatchedWrite{File: fullyPopulatedFilesFile()}
	cacheItem := BatchedWrite{CacheEntry: fullyPopulatedCacheEntry()}

	if enqErr := q1.Enqueue(&fileItem); enqErr != nil {
		t.Fatalf("enqueue file: %v", enqErr)
	}
	if enqErr := q1.Enqueue(&cacheItem); enqErr != nil {
		t.Fatalf("enqueue cache: %v", enqErr)
	}
	if cloErr := q1.Close(); cloErr != nil {
		t.Fatalf("close q1: %v", cloErr)
	}

	// Phase 2: Reopen in a new instance (simulating process restart)
	q2, err := dque.NewOrOpen[BatchedWrite]("overflow", queueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen (2nd): %v", err)
	}
	t.Cleanup(func() { q2.Close() })

	// Verify size
	if size := q2.Size(); size != 2 {
		t.Errorf("expected queue size 2, got %d", size)
	}

	// Dequeue and verify file item
	dequeuedFile, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue file: %v", err)
	}
	if dequeuedFile.File == nil {
		t.Fatal("expected File item, got nil")
	}
	assertFilesFileEqual(t, "recovered file", fileItem.File, dequeuedFile.File)

	// Dequeue and verify cache item
	dequeuedCache, err := q2.Dequeue()
	if err != nil {
		t.Fatalf("dequeue cache: %v", err)
	}
	if dequeuedCache.CacheEntry == nil {
		t.Fatal("expected CacheEntry item, got nil")
	}
	assertHTTPCacheEntryEqual(t, "recovered cache", cacheItem.CacheEntry, dequeuedCache.CacheEntry)

	// Queue should now be empty
	_, err = q2.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Errorf("expected ErrEmpty after draining, got: %v", err)
	}
}
