//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

// Package flock is a lightweight, standard-library-only replacement for
// github.com/gofrs/flock tailored to the needs of dque.
//
// It provides exclusive, non-blocking advisory file locking through a small
// Flock type. The supported API is intentionally minimal:
//
//   - New(path) creates a new Flock for the given path.
//   - TryLock() attempts to acquire the lock without blocking.
//   - Unlock() releases the lock.
//   - Close() releases the lock and closes the underlying file handle.
//
// The following features from gofrs/flock are intentionally not implemented:
// shared locks (RLock/TryRLock), blocking locks (Lock), timeouts, and retry
// logic. Only Unix-like systems and Windows are supported.
package flock
