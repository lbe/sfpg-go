// queue_internal_test.go
package dque

//
// White-box testing of DQue error paths that require seam injection.
//

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/flock"
)

// TestInitQueue_LoadReadDirFails verifies that a failure reading the queue
// directory during load is returned and the lock is released.
func TestInitQueue_LoadReadDirFails(t *testing.T) {
	orig := osReadDir
	osReadDir = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("read dir denied")
	}
	t.Cleanup(func() { osReadDir = orig })

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "test"), 0755); err != nil {
		t.Fatalf("failed to create queue dir: %s", err)
	}

	_, err := Open[item1]("test", dir, 3)
	if err == nil {
		t.Fatal("expected Open to fail")
	}
	if !strings.Contains(err.Error(), "unable to read files") {
		t.Fatalf("expected 'unable to read files' in error, got: %s", err)
	}
}

// TestInitQueue_LoadSegmentOpenFailsCleanup verifies that when load() opens the
// first segment successfully but fails to open the last segment, the opened
// first segment and the file lock are released during cleanup.
func TestInitQueue_LoadSegmentOpenFailsCleanup(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "test")
	if err := os.Mkdir(fullPath, 0755); err != nil {
		t.Fatalf("failed to create queue dir: %s", err)
	}

	// Create a valid segment 1 containing one item.
	seg1, err := newQueueSegment[item1](fullPath, 1, false)
	if err != nil {
		t.Fatalf("failed to create segment 1: %s", err)
	}
	if err = seg1.add(&item1{Name: "x"}); err != nil {
		t.Fatalf("failed to add item to segment 1: %s", err)
	}
	if err = seg1.close(); err != nil {
		t.Fatalf("failed to close segment 1: %s", err)
	}

	// Create a corrupted segment 2: a length prefix with no following data.
	seg2Path := filepath.Join(fullPath, "0000000000002.dque")
	f, err := os.Create(seg2Path)
	if err != nil {
		t.Fatalf("failed to create segment 2: %s", err)
	}
	if err = binary.Write(f, binary.LittleEndian, uint32(1)); err != nil {
		t.Fatalf("failed to write length prefix: %s", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("failed to close segment 2: %s", err)
	}

	_, err = Open[item1]("test", dir, 3)
	if err == nil {
		t.Fatal("expected Open to fail due to corrupted segment 2")
	}
}

// TestInitQueue_LoadFailureFlockCloseFails verifies that when load() fails and
// releasing the file lock also fails, both errors are reported.
func TestInitQueue_LoadFailureFlockCloseFails(t *testing.T) {
	origReadDir := osReadDir
	osReadDir = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("read dir denied")
	}
	t.Cleanup(func() { osReadDir = origReadDir })

	origFlockClose := flockClose
	flockClose = func(*flock.Flock) error {
		return errors.New("flock close denied")
	}
	t.Cleanup(func() { flockClose = origFlockClose })

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "test"), 0755); err != nil {
		t.Fatalf("failed to create queue dir: %s", err)
	}

	_, err := Open[item1]("test", dir, 3)
	if err == nil {
		t.Fatal("expected Open to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "read dir denied") {
		t.Fatalf("expected load error in joined error, got: %s", msg)
	}
	if !strings.Contains(msg, "flock close denied") {
		t.Fatalf("expected flock close error in joined error, got: %s", msg)
	}
}

// TestDiskBytes_NormalSummation verifies that DiskBytes returns the sum of
// all non-directory files in the queue directory (lock.lock plus segments),
// matching the current on-disk contents. It enqueues REAL items (so the cache
// is maintained by Enqueue) and proves DiskBytes() does not re-stat the dir.
func TestDiskBytes_NormalSummation(t *testing.T) {
	dir := t.TempDir()
	q, err := New[item1]("test", dir, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Enqueue real items so DiskBytes is maintained by Enqueue, not a stub.
	for i := range 5 {
		if enqErr := q.Enqueue(&item1{Name: fmt.Sprintf("n%d", i)}); enqErr != nil {
			t.Fatalf("Enqueue: %v", enqErr)
		}
	}

	// Count osReadDir calls after the queue is open to prove DiskBytes is cached.
	readDirCalls := 0
	orig := osReadDir
	osReadDir = func(path string) ([]os.DirEntry, error) {
		readDirCalls++
		return os.ReadDir(path)
	}
	t.Cleanup(func() { osReadDir = orig })

	// Expected = sum of ALL non-directory files in q.fullPath (lock.lock + segments),
	// computed directly from disk (not via the osReadDir seam used by DiskBytes).
	var expected int64
	entries, err := os.ReadDir(q.fullPath)
	if err != nil {
		t.Fatalf("os.ReadDir(%s): %v", q.fullPath, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("e.Info(): %v", err)
		}
		expected += info.Size()
	}

	if got := q.DiskBytes(); got != expected {
		t.Errorf("expected DiskBytes = %d (all non-dir files incl lock.lock), got %d", expected, got)
	}
	if readDirCalls != 0 {
		t.Errorf("expected DiskBytes() to not call osReadDir, got %d calls", readDirCalls)
	}
}

// TestDiskBytes_DoesNotStatEveryFileOnEachCall verifies that after the queue
// is open, repeated DiskBytes() calls do not re-read the directory. The queue
// is seeded once on open; enqueue/dequeue maintain the cached byte total.
func TestDiskBytes_DoesNotStatEveryFileOnEachCall(t *testing.T) {
	dir := t.TempDir()

	readDirCalls := 0
	orig := osReadDir
	osReadDir = func(path string) ([]os.DirEntry, error) {
		readDirCalls++
		return os.ReadDir(path)
	}
	t.Cleanup(func() { osReadDir = orig })

	// Small ItemsPerSegment so a handful of enqueues create multiple segments.
	q, err := New[item1]("test", dir, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Enqueue enough to create >=3 segment files (ItemsPerSegment=2).
	for i := range 7 {
		if err := q.Enqueue(&item1{Name: fmt.Sprintf("n%d", i)}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	first, last := q.SegmentNumbers()
	if last-first+1 < 3 {
		t.Fatalf("expected >=3 segment files, got %d", last-first+1)
	}

	callsAfterOpen := readDirCalls
	if callsAfterOpen == 0 {
		t.Fatalf("expected at least the seed osReadDir during New")
	}

	// Many DiskBytes() calls must not re-stat the directory.
	for range 10 {
		_ = q.DiskBytes()
	}
	if readDirCalls != callsAfterOpen {
		t.Errorf("DiskBytes() re-read the dir: %d calls during open, %d after 10 calls", callsAfterOpen, readDirCalls)
	}
}

// TestDiskBytes_Closed verifies that DiskBytes returns 0 when the queue is closed.
func TestDiskBytes_Closed(t *testing.T) {
	dir := t.TempDir()
	q, err := New[item1]("test", dir, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bytes := q.DiskBytes()
	if bytes != 0 {
		t.Errorf("expected DiskBytes = 0 for closed queue, got %d", bytes)
	}
}

// TestDiskBytes_ReadDirError verifies that a seed-time osReadDir failure (the
// SECOND osReadDir, after load has already succeeded) does NOT fail New; the
// cached total stays 0 and subsequent DiskBytes() calls do not re-stat the dir.
func TestDiskBytes_ReadDirError(t *testing.T) {
	dir := t.TempDir()

	readDirCalls := 0
	orig := osReadDir
	osReadDir = func(path string) ([]os.DirEntry, error) {
		readDirCalls++
		if readDirCalls == 1 {
			// First call is load(); let it succeed with the real directory.
			return os.ReadDir(path)
		}
		// Second call is the seed after load; force it to fail.
		return nil, errors.New("read dir denied")
	}
	t.Cleanup(func() { osReadDir = orig })

	q, err := New[item1]("test", dir, 10)
	if err != nil {
		t.Fatalf("New should succeed even when seed ReadDir fails: %v", err)
	}
	defer q.Close()

	// Seed already happened: load (1) + seed (2). New must not have failed.
	if readDirCalls != 2 {
		t.Fatalf("expected 2 osReadDir calls during New (load+seed), got %d", readDirCalls)
	}

	bytes := q.DiskBytes()
	if bytes != 0 {
		t.Errorf("expected DiskBytes = 0 when seed ReadDir fails, got %d", bytes)
	}

	// Further DiskBytes() calls must not call ReadDir again.
	captured := readDirCalls
	for range 3 {
		if q.DiskBytes() != 0 {
			t.Fatalf("expected DiskBytes = 0 on subsequent calls, got %d", q.DiskBytes())
		}
	}
	if readDirCalls != captured {
		t.Errorf("expected no further osReadDir calls, got %d (was %d)", readDirCalls, captured)
	}
}
