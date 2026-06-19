package flock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f1 := New(path)
	locked, err := f1.TryLock()
	if err != nil {
		t.Fatalf("first TryLock failed with error: %v", err)
	}
	if !locked {
		t.Fatal("first TryLock should have acquired the lock")
	}
	defer func() {
		if closeErr := f1.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	f2 := New(path)
	locked, err = f2.TryLock()
	if err != nil {
		t.Fatalf("second TryLock failed with error: %v", err)
	}
	if locked {
		t.Fatal("second TryLock should not have acquired the lock")
	}

	if err := f2.Close(); err != nil {
		t.Fatalf("closing second Flock failed: %v", err)
	}
}

func TestUnlockReleasesLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f1 := New(path)
	if _, err := f1.TryLock(); err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}

	if err := f1.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	if err := f1.Close(); err != nil {
		t.Fatalf("Close after Unlock failed: %v", err)
	}

	f2 := New(path)
	locked, err := f2.TryLock()
	if err != nil {
		t.Fatalf("second TryLock failed: %v", err)
	}
	if !locked {
		t.Fatal("second TryLock should have acquired the lock after Unlock")
	}
	if err := f2.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestCloseReleasesLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f1 := New(path)
	if _, err := f1.TryLock(); err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}

	if err := f1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	f2 := New(path)
	locked, err := f2.TryLock()
	if err != nil {
		t.Fatalf("second TryLock failed: %v", err)
	}
	if !locked {
		t.Fatal("second TryLock should have acquired the lock after Close")
	}
	if err := f2.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close on closed Flock failed: %v", err)
	}
}

func TestUnlockWhenNotLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if err := f.Unlock(); err != nil {
		t.Fatalf("Unlock on unlocked Flock should be safe: %v", err)
	}

	// Create the underlying file so Close has something to close.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	f.file = file

	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestTryLockAlreadyLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	locked, err := f.TryLock()
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	if !locked {
		t.Fatal("first TryLock should have acquired the lock")
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	locked, err = f.TryLock()
	if err != nil {
		t.Fatalf("second TryLock on already-locked Flock failed: %v", err)
	}
	if !locked {
		t.Fatal("TryLock on already-locked Flock should report locked")
	}
}

func TestTryLockOpenFailure(t *testing.T) {
	// Use a path inside a non-existent directory so OpenFile fails.
	f := New(filepath.Join(t.TempDir(), "missing", "lock.lock"))
	locked, err := f.TryLock()
	if err == nil {
		t.Fatal("expected TryLock to fail when the file cannot be created")
	}
	if locked {
		t.Fatal("expected locked to be false when OpenFile fails")
	}
}

func TestUnlockReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	// Close the underlying descriptor without updating state so Unlock fails.
	if err := f.file.Close(); err != nil {
		t.Fatalf("closing file handle failed: %v", err)
	}

	if err := f.Unlock(); err == nil {
		t.Fatal("expected Unlock to return an error when the file handle is closed")
	}
}

func TestCloseReturnsUnlockError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	// Close the underlying descriptor while the flock still believes it holds the lock.
	if err := f.file.Close(); err != nil {
		t.Fatalf("closing file handle failed: %v", err)
	}

	if err := f.Close(); err == nil {
		t.Fatal("expected Close to return an error when unlocking a closed file handle")
	}
}

func TestCloseReturnsFileCloseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := f.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	// Close the underlying descriptor so the subsequent file.Close fails.
	if err := f.file.Close(); err != nil {
		t.Fatalf("closing file handle failed: %v", err)
	}

	if err := f.Close(); err == nil {
		t.Fatal("expected Close to return an error when closing an already-closed file handle")
	}
}

func TestTryLockSystemError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	f.file = file

	// Close the descriptor so the platform-specific lock syscall fails.
	if err = f.file.Close(); err != nil {
		t.Fatalf("closing file handle failed: %v", err)
	}

	locked, err := f.TryLock()
	if err == nil {
		t.Fatal("expected TryLock to return an error when the file handle is closed")
	}
	if locked {
		t.Fatal("expected locked to be false when TryLock returns an error")
	}
}

// TestCloseNilsFile verifies that a successful Close sets f.file to nil.
func TestCloseNilsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	if f.file == nil {
		t.Fatal("expected f.file to be non-nil after TryLock")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if f.file != nil {
		t.Fatal("expected f.file to be nil after Close")
	}
}

// TestCloseClosesFileWhenUnlockFails verifies that the underlying file is
// closed even if unlocking fails.
func TestCloseClosesFileWhenUnlockFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.lock")

	f := New(path)
	if _, err := f.TryLock(); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	// Close the underlying descriptor while the flock still believes it holds
	// the lock. Close must still release the file descriptor.
	if err := f.file.Close(); err != nil {
		t.Fatalf("closing file handle failed: %v", err)
	}

	if err := f.Close(); err == nil {
		t.Fatal("expected Close to return an error when unlocking fails")
	}
	if f.file != nil {
		t.Fatal("expected f.file to be nil after Close even when unlock failed")
	}
}
