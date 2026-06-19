//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

//go:build unix

package flock

import "syscall"

func (f *Flock) tryLock() (bool, error) {
	err := syscall.Flock(int(f.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return false, nil
	}
	return false, err
}

func (f *Flock) unlock() error {
	return syscall.Flock(int(f.file.Fd()), syscall.LOCK_UN)
}
