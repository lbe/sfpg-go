// segment_internal_test.go
package dque

//
// Additional white-box tests for qSegment error paths that require seam injection.
//

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// unencodableItem contains a channel, which gob cannot encode.
type unencodableItem struct {
	Ch chan int
}

// TestSegment_AddGobEncodeFails verifies that add reports an error when the
// object cannot be gob encoded.
func TestSegment_AddGobEncodeFails(t *testing.T) {
	testDir := t.TempDir()

	seg, err := newQueueSegment[unencodableItem](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.add(&unencodableItem{Ch: make(chan int)})
	if err == nil {
		t.Fatal("expected add to fail")
	}
	if !strings.Contains(err.Error(), "error gob encoding object") {
		t.Fatalf("expected 'error gob encoding object' in error, got: %s", err)
	}
}

// TestSegment_AddWriteLengthFails verifies that add reports an error and does
// not modify segment state when writing the length prefix fails.
func TestSegment_AddWriteLengthFails(t *testing.T) {
	orig := segmentFileWrite
	callCount := 0
	segmentFileWrite = func(f *os.File, b []byte) (int, error) {
		callCount++
		if callCount == 1 {
			return 0, errors.New("write length denied")
		}
		return f.Write(b)
	}
	t.Cleanup(func() { segmentFileWrite = orig })

	testDir := t.TempDir()
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.add(&item1{Name: "x"})
	if err == nil {
		t.Fatal("expected add to fail")
	}
	if !strings.Contains(err.Error(), "failed to write object length") {
		t.Fatalf("expected 'failed to write object length' in error, got: %s", err)
	}
	if seg.size() != 0 {
		t.Fatalf("expected size 0 after failed add, got %d", seg.size())
	}
}

// TestSegment_AddWriteObjectFails verifies that add reports an error and does
// not modify segment state when writing the object bytes fails.
func TestSegment_AddWriteObjectFails(t *testing.T) {
	orig := segmentFileWrite
	callCount := 0
	segmentFileWrite = func(f *os.File, b []byte) (int, error) {
		callCount++
		if callCount == 2 {
			return 0, errors.New("write object denied")
		}
		return f.Write(b)
	}
	t.Cleanup(func() { segmentFileWrite = orig })

	testDir := t.TempDir()
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.add(&item1{Name: "x"})
	if err == nil {
		t.Fatal("expected add to fail")
	}
	if !strings.Contains(err.Error(), "failed to write object") {
		t.Fatalf("expected 'failed to write object' in error, got: %s", err)
	}
	if seg.size() != 0 {
		t.Fatalf("expected size 0 after failed add, got %d", seg.size())
	}
}

// TestSegment_AddSyncFailsRollsBack verifies that add rolls back the in-memory
// state when syncing the file fails.
func TestSegment_AddSyncFailsRollsBack(t *testing.T) {
	orig := segmentFileSync
	segmentFileSync = func(f *os.File) error {
		return errors.New("sync denied")
	}
	t.Cleanup(func() { segmentFileSync = orig })

	testDir := t.TempDir()
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.add(&item1{Name: "x"})
	if err == nil {
		t.Fatal("expected add to fail")
	}
	if !strings.Contains(err.Error(), "unable to sync file changes") {
		t.Fatalf("expected 'unable to sync file changes' in error, got: %s", err)
	}
	if seg.size() != 0 {
		t.Fatalf("expected size 0 after failed sync, got %d", seg.size())
	}
}

// TestSegment_DeleteCloseFails verifies that delete reports an error when
// closing the segment file fails.
func TestSegment_DeleteCloseFails(t *testing.T) {
	orig := segmentFileClose
	segmentFileClose = func(f *os.File) error {
		return errors.New("close denied")
	}
	t.Cleanup(func() { segmentFileClose = orig })

	testDir := t.TempDir()
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.delete()
	if err == nil {
		t.Fatal("expected delete to fail")
	}
	if !strings.Contains(err.Error(), "unable to close the segment file before deleting") {
		t.Fatalf("expected 'unable to close the segment file before deleting' in error, got: %s", err)
	}
}

// TestSegment_DeleteRemoveFails verifies that delete reports an error when
// removing the segment file fails.
func TestSegment_DeleteRemoveFails(t *testing.T) {
	orig := osRemove
	osRemove = func(string) error {
		return errors.New("remove denied")
	}
	t.Cleanup(func() { osRemove = orig })

	testDir := t.TempDir()
	seg, err := newQueueSegment[item1](testDir, 1, false)
	if err != nil {
		t.Fatalf("newQueueSegment failed: %s", err)
	}

	err = seg.delete()
	if err == nil {
		t.Fatal("expected delete to fail")
	}
	if !strings.Contains(err.Error(), "error deleting file") {
		t.Fatalf("expected 'error deleting file' in error, got: %s", err)
	}
}
