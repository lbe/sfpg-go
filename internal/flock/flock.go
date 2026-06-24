//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

package flock

import (
	"errors"
	"os"
	"time"
)

// Flock is a file-backed advisory lock.
type Flock struct {
	path   string
	file   *os.File
	locked bool
}

// New returns a new Flock for the given path.
// The file is not opened until TryLock is called.
func New(path string) *Flock {
	return &Flock{path: path}
}

// TryLock attempts to acquire an exclusive, non-blocking advisory lock.
// It returns (true, nil) on success, (false, nil) if the file is already
// locked by another process, and (false, err) for other errors.
//
// If this Flock already holds the lock, TryLock short-circuits and returns
// (true, nil). If the lock cannot be acquired, the underlying file handle is
// left open so subsequent calls can retry efficiently. Callers must use
// Unlock or Close to release the lock and close the file descriptor.
//
// On contention, TryLock retries a few times with short delays to tolerate
// filesystem-level lag (e.g., ZFS) where the kernel may not immediately
// release a flock after the owning process dies.
func (f *Flock) TryLock() (bool, error) {
	if f.locked {
		return true, nil
	}

	if f.file == nil {
		file, err := os.OpenFile(f.path, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return false, err
		}
		f.file = file
	}

	// Retry with backoff to handle transient lock contention
	// (e.g., ZFS releasing flock after process termination).
	const retries = 3
	for i := range retries {
		locked, err := f.tryLock()
		if err != nil {
			return false, err
		}
		if locked {
			f.locked = true
			return true, nil
		}
		if i < retries-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return false, nil
}

// Unlock releases the lock. It is safe to call when the lock is not held.
func (f *Flock) Unlock() error {
	if !f.locked {
		return nil
	}
	if err := f.unlock(); err != nil {
		return err
	}
	f.locked = false
	return nil
}

// Close releases the lock if held and closes the underlying file. Any errors
// from unlocking and closing are joined so the file descriptor is always
// released even if the unlock step fails.
func (f *Flock) Close() error {
	if f.file == nil {
		return nil
	}

	var errs []error
	if f.locked {
		if err := f.unlock(); err != nil {
			errs = append(errs, err)
		}
		f.locked = false
	}

	if err := f.file.Close(); err != nil {
		errs = append(errs, err)
	}
	f.file = nil

	return errors.Join(errs...)
}
