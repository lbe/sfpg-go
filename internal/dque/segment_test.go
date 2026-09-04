// segment_test.go
package dque

//
// White box testing of the qSegment struct and methods.
//

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// item1 is the thing we'll be storing in the queue
type item1 struct {
	Name string
}

// TestSegment verifies the behavior of one segment.
func TestSegment(t *testing.T) {
	testDir := t.TempDir()

	// Create a new segment of the queue
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment('%s') failed with '%s'\n", testDir, err.Error())
	}

	//
	// Add some items and remove one
	//
	if seg.add(&item1{Name: "Number 1"}) != nil {
		t.Fatalf("failed to add item1")
	}
	if seg.size() != 1 {
		t.Fatalf("Expected size of 1")
	}

	if seg.add(&item1{Name: "Number 2"}) != nil {
		t.Fatalf("failed to add item2")
	}
	if seg.size() != 2 {
		t.Fatalf("Expected size of 2")
	}
	_, err = seg.remove()
	if err != nil {
		t.Fatalf("Remove() failed with '%s'\n", err.Error())
	}
	if seg.size() != 1 {
		t.Fatalf("Expected size of 1")
	}
	if seg.sizeOnDisk() != 2 {
		t.Fatalf("Expected sizeOnDisk of 2")
	}
	if seg.add(&item1{Name: "item3"}) != nil {
		t.Fatalf("failed to add item3")
	}
	if seg.size() != 2 {
		t.Fatalf("Expected size of 2")
	}
	_, err = seg.remove()
	if err != nil {
		t.Fatalf("Remove() failed with '%s'\n", err.Error())
	}
	if seg.size() != 1 {
		t.Fatalf("Expected size of 1")
	}

	//
	// Recreate the segment from disk and remove the remaining item
	//
	seg, err = openQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("openQueueSegment('%s') failed with '%s'\n", testDir, err.Error())
	}
	if seg.size() != 1 {
		t.Fatalf("Expected size of 1")
	}

	_, err = seg.remove()
	if err != nil {
		if !errors.Is(err, errEmptySegment) {
			t.Fatalf("Remove() failed with '%s'\n", err.Error())
		}
	}
	if seg.size() != 0 {
		t.Fatalf("Expected size of 0")
	}
}

// TestSegment_ErrCorruptedSegment tests error handling for corrupted data
func TestSegment_ErrCorruptedSegment(t *testing.T) {
	testDir := t.TempDir()
	expectedPath := (&qSegment[[]byte]{dirPath: testDir}).filePath()

	f, err := os.Create(expectedPath)
	if err != nil {
		t.Fatal(err)
	}

	// expect an 8 byte object, but only write 7 bytes
	// Write a length of 8 in little-endian, but only 7 bytes of data follow.
	if _, err = f.Write([]byte{8, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7}); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	_, err = openQueueSegment[[]byte](testDir, 0, false)
	if err == nil {
		t.Fatal("expected ErrCorruptedSegment but got nil")
	}
	var corruptedError ErrCorruptedSegment
	if !errors.As(err, &corruptedError) {
		t.Fatalf("expected ErrCorruptedSegment but got %T: %s", err, err)
	}
	if corruptedError.Path != expectedPath {
		t.Fatalf("unexpected file path: %s", corruptedError.Path)
	}
	expected := "segment file " + expectedPath + " is corrupted: error reading gob data from file: unexpected EOF"
	if !strings.HasPrefix(corruptedError.Error(), expected) {
		t.Fatalf("wrong error message prefix: %s", corruptedError.Error())
	}
}

// TestSegment_ErrCorruptedSegment_gobLenCap verifies the gobLen sanity cap in load().
func TestSegment_ErrCorruptedSegment_gobLenCap(t *testing.T) {
	testDir := t.TempDir()
	expectedPath := (&qSegment[[]byte]{dirPath: testDir}).filePath()

	f, err := os.Create(expectedPath)
	if err != nil {
		t.Fatal(err)
	}

	// Write a 4-byte length of 0xFFFFFFFF (max uint32) to trigger the cap.
	if _, err = f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	_, err = openQueueSegment[[]byte](testDir, 0, false)
	if err == nil {
		t.Fatal("expected ErrCorruptedSegment but got nil")
	}
	var corruptedError ErrCorruptedSegment
	if !errors.As(err, &corruptedError) {
		t.Fatalf("expected ErrCorruptedSegment but got %T: %s", err, err)
	}
	if corruptedError.Path != expectedPath {
		t.Fatalf("unexpected file path: %s", corruptedError.Path)
	}
	if !strings.Contains(corruptedError.Error(), "exceeds maximum") {
		t.Fatalf("expected 'exceeds maximum' in error, got: %s", corruptedError.Error())
	}
}

// TestSegment_ErrorTypes verifies the Error and Unwrap methods of the exported error types.
func TestSegment_ErrorTypes(t *testing.T) {
	root := errors.New("root cause")

	corrupted := ErrCorruptedSegment{Path: "/tmp/segment.dque", Err: root}
	if !strings.HasPrefix(corrupted.Error(), "segment file /tmp/segment.dque is corrupted: root cause") {
		t.Fatalf("unexpected corrupted error message: %s", corrupted.Error())
	}
	if !errors.Is(corrupted, root) {
		t.Fatal("ErrCorruptedSegment.Unwrap() should return the wrapped error")
	}

	decode := ErrUnableToDecode{Path: "/tmp/segment.dque", Err: root}
	if !strings.HasPrefix(decode.Error(), "object in segment file /tmp/segment.dque cannot be decoded: root cause") {
		t.Fatalf("unexpected decode error message: %s", decode.Error())
	}
	if !errors.Is(decode, root) {
		t.Fatal("ErrUnableToDecode.Unwrap() should return the wrapped error")
	}
}

// TestSegment_newQueueSegmentErrors verifies error paths in newQueueSegment.
func TestSegment_newQueueSegmentErrors(t *testing.T) {
	testDir := t.TempDir()

	// Invalid directory.
	seg, err := newQueueSegment[item1](filepath.Join(testDir, "does-not-exist"), 1, false)
	if err == nil {
		t.Fatal("expected newQueueSegment to fail with invalid directory")
	}
	if seg != nil {
		t.Fatal("expected nil segment on error")
	}

	workingDir := filepath.Join(testDir, "working")
	if err = os.Mkdir(workingDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %s", err)
	}

	// Create a file with the segment name so newQueueSegment fails because it already exists.
	segPath := (&qSegment[item1]{dirPath: workingDir, number: 1}).filePath()
	if _, err = os.Create(segPath); err != nil {
		t.Fatalf("failed to create segment file: %s", err)
	}

	seg, err = newQueueSegment[item1](workingDir, 1, false)
	if err == nil {
		t.Fatal("expected newQueueSegment to fail when file already exists")
	}
	if seg != nil {
		t.Fatal("expected nil segment on error")
	}

	// Make the directory read-only so creating a new segment file fails.
	// Permissions are ignored on Windows and when running as root, so skip
	// this sub-case there.
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		return
	}
	readOnlyDir := filepath.Join(testDir, "readonly")
	if err = os.Mkdir(readOnlyDir, 0500); err != nil {
		t.Fatalf("failed to create read-only directory: %s", err)
	}

	seg, err = newQueueSegment[item1](readOnlyDir, 1, false)
	if err == nil {
		t.Fatal("expected newQueueSegment to fail when file cannot be created")
	}
	if seg != nil {
		t.Fatal("expected nil segment on error")
	}
}

// TestSegment_openQueueSegmentErrors verifies error paths in openQueueSegment.
func TestSegment_openQueueSegmentErrors(t *testing.T) {
	testDir := t.TempDir()

	// Invalid directory.
	seg, err := openQueueSegment[item1](filepath.Join(testDir, "does-not-exist"), 1, false)
	if err == nil {
		t.Fatal("expected openQueueSegment to fail with invalid directory")
	}
	if seg != nil {
		t.Fatal("expected nil segment on error")
	}

	workingDir := filepath.Join(testDir, "working")
	if err = os.Mkdir(workingDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %s", err)
	}

	// File does not exist.
	seg, err = openQueueSegment[item1](workingDir, 1, false)
	if err == nil {
		t.Fatal("expected openQueueSegment to fail when file does not exist")
	}
	if seg != nil {
		t.Fatal("expected nil segment on error")
	}
}

// TestSegment_loadFileOpenError verifies load() fails when the segment file cannot be opened.
func TestSegment_loadFileOpenError(t *testing.T) {
	testDir := t.TempDir()

	seg := &qSegment[item1]{dirPath: testDir, number: 1}
	if err := seg.load(); err == nil {
		t.Fatal("expected load() to fail when the file does not exist")
	}
}

// TestSegment_ErrUnableToDecode tests decoding failure for invalid gob data.
func TestSegment_ErrUnableToDecode(t *testing.T) {
	testDir := t.TempDir()

	f, err := os.Create((&qSegment[[]byte]{dirPath: testDir}).filePath())
	if err != nil {
		t.Fatal(err)
	}
	// Write a length prefix for a 4-byte object but invalid gob bytes.
	if _, err = f.Write([]byte{4, 0, 0, 0, 1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	_, err = openQueueSegment[[]byte](testDir, 0, false)
	if err == nil {
		t.Fatal("expected error for undecodable object")
	}
	if _, ok := errors.AsType[ErrUnableToDecode](err); !ok {
		t.Fatalf("expected ErrUnableToDecode but got %T: %s", err, err)
	}
}

// TestSegment_ExcessDeletionRecords tests corruption detection for deletion records without items.
func TestSegment_ExcessDeletionRecords(t *testing.T) {
	testDir := t.TempDir()

	f, err := os.Create((&qSegment[[]byte]{dirPath: testDir}).filePath())
	if err != nil {
		t.Fatal(err)
	}
	// A zero-length record signifies a deletion, but no items were enqueued.
	if _, err = f.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	_, err = openQueueSegment[[]byte](testDir, 0, false)
	if err == nil {
		t.Fatal("expected error for excess deletion records")
	}
	if _, ok := errors.AsType[ErrCorruptedSegment](err); !ok {
		t.Fatalf("expected ErrCorruptedSegment but got %T: %s", err, err)
	}
}

// TestSegment_turboOffWhenOff verifies turboOff is safe when turbo is already off.
func TestSegment_turboOffWhenOff(t *testing.T) {
	testDir := t.TempDir()

	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	if err := seg.turboOff(); err != nil {
		t.Fatalf("turboOff on non-turbo segment should be safe: %s", err)
	}
}

// TestSegment_turboSyncWhenOff verifies turboSync is safe when turbo is off.
func TestSegment_turboSyncWhenOff(t *testing.T) {
	testDir := t.TempDir()

	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	if err := seg.turboSync(); err != nil {
		t.Fatalf("turboSync on non-turbo segment should be safe: %s", err)
	}
}

// TestSegment_Open verifies the behavior of the openSegment function.
func TestSegment_openQueueSegment_failIfNew(t *testing.T) {
	testDir := t.TempDir()

	seg, err := openQueueSegment[item1](testDir, 1, false)
	if err == nil {
		t.Fatalf("openQueueSegment('%s') should have failed because it should be new\n", testDir)
	}
	if seg != nil {
		t.Fatalf("segment after failure must be nil")
	}
}

// TestSegment_Turbo verifies the behavior of the turboOn() and turboOff() methods.
func TestSegment_Turbo(t *testing.T) {
	testDir := t.TempDir()

	seg, err := newQueueSegment[item1](testDir, 10, false)
	if err != nil {
		t.Fatalf("newQueueSegment('%s') failed\n", testDir)
	}

	// turbo is off so expect syncCount to change
	if seg.add(&item1{Name: "Number 1"}) != nil {
		t.Fatalf("failed to add item1")
	}
	if seg.size() != 1 {
		t.Fatalf("Expected size of 1")
	}
	if seg.syncCount != 1 {
		t.Fatalf("syncCount must be 1")
	}

	// Turn on turbo and expect sync count to stay the same.
	seg.turboOn()
	if seg.add(&item1{Name: "Number 2"}) != nil {
		t.Fatalf("failed to add item2")
	}
	if seg.size() != 2 {
		t.Fatalf("Expected size of 2")
	}
	if seg.syncCount != 1 {
		t.Fatalf("syncCount must still be 1")
	}

	// Turn off turbo and expect the syncCount to increase when remove is called.
	if err = seg.turboOff(); err != nil {
		t.Fatalf("Unexpecte error turning off turbo('%s')\n", testDir)
	}

	// seg.turboOff() calls seg.turboSync() which increments syncCount
	if seg.syncCount != 2 {
		t.Fatalf("syncCount must be 2 now")
	}

	_, err = seg.remove()
	if err != nil {
		t.Fatalf("Remove() failed with '%s'\n", err.Error())
	}
	// seg.remove() calls seg._sync() which increments syncCount
	if seg.syncCount != 3 {
		t.Fatalf("syncCount must be 3 now")
	}
}

// TestSegment_closeNilsFile verifies that close() sets seg.file to nil.
func TestSegment_closeNilsFile(t *testing.T) {
	testDir := t.TempDir()

	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	if seg.file == nil {
		t.Fatalf("expected seg.file to be non-nil before close")
	}
	if err := seg.close(); err != nil {
		t.Fatalf("close failed: %s", err)
	}
	if seg.file != nil {
		t.Fatalf("expected seg.file to be nil after close")
	}

	// close() is idempotent when seg.file is nil.
	if err := seg.close(); err != nil {
		t.Fatalf("second close on nil file failed: %s", err)
	}
}
