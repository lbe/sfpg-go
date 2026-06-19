//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

//go:build !unix && !windows

package flock

import "errors"

func (f *Flock) tryLock() (bool, error) {
	return false, errors.New("flock: unsupported platform")
}

func (f *Flock) unlock() error {
	return errors.New("flock: unsupported platform")
}
