package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/pathutil"
)

// TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages verifies that WalkImageDir()
// enqueues only non-zero-sized files with extensions matching (jpg|jpeg|png|gif),
// and skips zero-length and non-image files.
func TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages(t *testing.T) {
	roPool, _, imagesDir, _ := createTestPoolsAndDir(t)

	// Create a small set of files in the Images directory
	mustWrite := func(rel string, size int) string {
		p := filepath.Join(imagesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", p, err)
		}
		var data []byte
		if size > 0 {
			data = make([]byte, size)
			for i := range data {
				data[i] = byte(i%251 + 1)
			}
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write failed for %s: %v", p, err)
		}
		return p
	}

	// Supported and non-zero
	a := mustWrite("a.jpg", 10)
	b := mustWrite("b.jpeg", 1)
	c := mustWrite("c.png", 2)
	d := mustWrite("d.gif", 3)
	e := mustWrite("UPPER.JPG", 4)
	// Nested
	f := mustWrite("nested/x.jpg", 5)
	// Zero-length should be ignored
	_ = mustWrite("zero.jpg", 0)
	_ = mustWrite("nested/zero.png", 0)
	// Unsupported extensions should be ignored by WalkImageDir's regex
	_ = mustWrite("photo.webp", 8)
	_ = mustWrite("doc.txt", 12)
	_ = mustWrite("image.tiff", 14)

	q := queue.NewQueue[DiscoveryPathWork](100)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)

	// Execute WalkImageDir synchronously
	WalkImageDir(deps)

	// Collect queued items; order is not guaranteed, so sort for comparison
	gotPaths := make([]string, 0, len(q.Slice()))
	for _, item := range q.Slice() {
		gotPaths = append(gotPaths, string(item.Path))
	}
	sort.Strings(gotPaths)

	want := []string{a, b, c, d, e, f}
	sort.Strings(want)

	if len(gotPaths) != len(want) {
		t.Fatalf("unexpected queue length: got %d, want %d; got=%v", len(gotPaths), len(want), gotPaths)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Fatalf("mismatch at %d: got %q, want %q\nall got=%v\nall want=%v", i, gotPaths[i], want[i], gotPaths, want)
		}
	}

	if deps.QSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", deps.QSendersActive.Load())
	}
}

// TestWalkImageDir_CompletesWithFiles verifies that WalkImageDir completes
// successfully when there are files to process, enqueues the expected files,
// and resets sender accounting after completion.
// This is the walker-level equivalent of the original "UpdatesModuleState" test.
func TestWalkImageDir_CompletesWithFiles(t *testing.T) {
	roPool, _, imagesDir, _ := createTestPoolsAndDir(t)

	// Create a few image files
	for _, name := range []string{"a.jpg", "b.png", "sub/c.gif"} {
		p := filepath.Join(imagesDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	q := queue.NewQueue[DiscoveryPathWork](100)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)

	WalkImageDir(deps)

	if deps.QSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", deps.QSendersActive.Load())
	}

	// Verify at least the expected number of files were enqueued
	got := q.Slice()
	if len(got) < 3 {
		t.Errorf("expected at least 3 files in queue, got %d: %v", len(got), got)
	}
}

// TestWalkImageDir_CancelledContext verifies that WalkImageDir handles
// context cancellation gracefully.
func TestWalkImageDir_CancelledContext(t *testing.T) {
	roPool, _, imagesDir, _ := createTestPoolsAndDir(t)

	// Create some files
	p := filepath.Join(imagesDir, "test.jpg")
	if err := os.WriteFile(p, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Use a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	q := queue.NewQueue[DiscoveryPathWork](100)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	deps.Ctx = ctx

	WalkImageDir(deps)

	if deps.QSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after cancelled context: %d", deps.QSendersActive.Load())
	}
}

// TestWalkImageDir_BoundedQueue verifies that WalkImageDir works correctly with
// a bounded queue that is large enough to hold all discovered files.
func TestWalkImageDir_BoundedQueue(t *testing.T) {
	roPool, _, imagesDir, _ := createTestPoolsAndDir(t)

	for i := range 10 {
		p := filepath.Join(imagesDir, fmt.Sprintf("img%d.jpg", i))
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	q := queue.NewBoundedQueue[DiscoveryPathWork](16, 20)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)

	WalkImageDir(deps)

	got := q.Slice()
	if len(got) != 10 {
		t.Fatalf("expected 10 items in queue, got %d: %v", len(got), got)
	}
	if deps.QSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", deps.QSendersActive.Load())
	}
}

// TestWalkImageDir_BackpressureOnFullQueue verifies that WalkImageDir handles
// ErrQueueFull gracefully when the queue is bounded and full, by processing
// items concurrently (simulating a real consumer).
func TestWalkImageDir_BackpressureOnFullQueue(t *testing.T) {
	roPool, _, imagesDir, _ := createTestPoolsAndDir(t)

	// Create files — more than the queue can hold.
	for i := range 10 {
		p := filepath.Join(imagesDir, fmt.Sprintf("img%d.jpg", i))
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	q := queue.NewBoundedQueue[DiscoveryPathWork](8, 4)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)

	// Start a concurrent consumer that drains the queue as items arrive.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var consumerWg sync.WaitGroup
	consumerWg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, err := q.Dequeue()
			if errors.Is(err, queue.ErrEmptyQueue) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	})

	// Run the walker — it should complete despite the bounded queue
	// because the consumer drains items, creating space for backpressure.
	WalkImageDir(deps)

	// Stop the consumer.
	cancel()
	consumerWg.Wait()

	// The key verification: WalkImageDir completed without error and
	// all sender accounting was properly reset.
	if deps.QSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", deps.QSendersActive.Load())
	}

	// At least 4 items should have been enqueued (the queue capacity).
	// The consumer may have dequeued some, so remaining items may be fewer.
	remaining := q.Len()
	if remaining > 4 {
		t.Fatalf("unexpected remaining items in queue: %d (expected 0-4)", remaining)
	}
}

func writeWalkTestImage(t *testing.T, imagesDir, rel string, size int, mtimeUnix int64) string {
	t.Helper()
	p := filepath.Join(imagesDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i%251 + 1)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mtime := time.Unix(mtimeUnix, 0)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

func folderIDForGalleryFilePath(t *testing.T, ctx context.Context, q *gallerydb.CustomQueries, galleryPath string) int64 {
	t.Helper()
	rootID, err := q.GetFolderIDByPath(ctx, "")
	if err != nil {
		t.Fatalf("GetFolderIDByPath root: %v", err)
	}
	parent := filepath.ToSlash(filepath.Dir(galleryPath))
	if parent == "." {
		return rootID
	}
	folderID, err := q.GetFolderIDByPath(ctx, parent)
	if err != nil {
		t.Fatalf("GetFolderIDByPath %q: %v", parent, err)
	}
	return folderID
}

func ensureGalleryFolderChain(t *testing.T, ctx context.Context, q *gallerydb.CustomQueries, galleryPath string) int64 {
	t.Helper()
	rootID, err := q.GetFolderIDByPath(ctx, "")
	if err != nil {
		t.Fatalf("GetFolderIDByPath root: %v", err)
	}
	parent := filepath.ToSlash(filepath.Dir(galleryPath))
	if parent == "." {
		return rootID
	}
	if _, lookupErr := q.GetFolderIDByPath(ctx, parent); lookupErr == nil {
		return folderIDForGalleryFilePath(t, ctx, q, galleryPath)
	}
	pathID, err := q.UpsertFolderPathReturningID(ctx, parent)
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID %q: %v", parent, err)
	}
	now := time.Now().Unix()
	folder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    pathID,
		Name:      filepath.Base(parent),
		Mtime:     sql.NullInt64{Int64: now, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder %q: %v", parent, err)
	}
	return folder.ID
}

func seedGalleryFileRow(t *testing.T, ctx context.Context, rwPool *dbconnpool.DbSQLConnPool, galleryPath string, mtimeUnix, sizeBytes int64) {
	t.Helper()
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer rwPool.Put(cpcRw)
	folderID := ensureGalleryFolderChain(t, ctx, cpcRw.Queries, galleryPath)
	pathID, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, galleryPath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	now := time.Now().Unix()
	if _, err := cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folderID, Valid: true},
		PathID:    pathID,
		Filename:  filepath.Base(galleryPath),
		Mtime:     sql.NullInt64{Int64: mtimeUnix, Valid: true},
		SizeBytes: sql.NullInt64{Int64: sizeBytes, Valid: true},
		Md5:       sql.NullString{String: "walk-test-md5", Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
}

func walkDepsWithCatalog(t *testing.T, imagesDir string, roPool *dbconnpool.DbSQLConnPool, q queue.Enqueuer[DiscoveryPathWork]) *WalkDeps {
	t.Helper()
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64
	normalized := filepath.ToSlash(imagesDir)
	return &WalkDeps{
		Wg:                  &wg,
		QSendersActive:      &qSendersActive,
		Ctx:                 context.Background(),
		ImagesDir:           imagesDir,
		Q:                   q,
		WalkPathStore:       parallelwalkdir.NewWalkPathStore(),
		NormalizedImagesDir: normalized,
		RemoveImagesPrefix:  pathutil.RemoveImagesDirPrefix,
		CatalogROPool:       roPool,
	}
}

// TestWalkImageDir_DirEntCatalog_skipsUnchangedEnqueues seeds DB+disk match → 0 enqueued.
func TestWalkImageDir_DirEntCatalog_skipsUnchangedEnqueues(t *testing.T) {
	const mtimeUnix = int64(1_700_000_100)
	const sizeBytes = int64(100)

	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	if err := os.MkdirAll(filepath.Join(imagesDir, "album"), 0o755); err != nil {
		t.Fatalf("mkdir album: %v", err)
	}
	writeWalkTestImage(t, imagesDir, "album/unchanged.jpg", int(sizeBytes), mtimeUnix)
	seedGalleryFileRow(t, ctx, rwPool, "album/unchanged.jpg", mtimeUnix, sizeBytes)

	q := queue.NewQueue[DiscoveryPathWork](8)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	WalkImageDir(deps)

	if got := len(q.Slice()); got != 0 {
		t.Fatalf("expected 0 enqueued unchanged files, got %d: %v", got, q.Slice())
	}
}

// TestWalkImageDir_DirEntCatalog_enqueuesModified: DB size mismatch → 1 enqueue.
func TestWalkImageDir_DirEntCatalog_enqueuesModified(t *testing.T) {
	const mtimeUnix = int64(1_700_000_200)
	const diskSize = int64(100)
	const dbSize = int64(999)

	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	if err := os.MkdirAll(filepath.Join(imagesDir, "album"), 0o755); err != nil {
		t.Fatalf("mkdir album: %v", err)
	}
	writeWalkTestImage(t, imagesDir, "album/modified.jpg", int(diskSize), mtimeUnix)
	seedGalleryFileRow(t, ctx, rwPool, "album/modified.jpg", mtimeUnix, dbSize)

	q := queue.NewQueue[DiscoveryPathWork](8)
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	WalkImageDir(deps)

	if got := len(q.Slice()); got != 1 {
		t.Fatalf("expected 1 enqueued modified file, got %d: %v", got, q.Slice())
	}
}
