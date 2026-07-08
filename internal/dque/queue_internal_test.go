// queue_internal_test.go
package dque

//
// White-box testing of DQue error paths that require seam injection.
//

import (
	"encoding/binary"
	"errors"
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
