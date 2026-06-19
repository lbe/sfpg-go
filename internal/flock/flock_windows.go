//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

//go:build windows

package flock

import (
	"syscall"
	"unsafe"
)

const (
	lockFileExclusiveLock   = 0x00000002
	lockFileFailImmediately = 0x00000001

	errorLockViolation = 0x21
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func lockFileEx(handle syscall.Handle, flags uint32, reserved uint32, nNumberOfBytesToLockLow uint32, nNumberOfBytesToLockHigh uint32, overlapped *syscall.Overlapped) error {
	ret, _, err := procLockFileEx.Call(
		uintptr(handle),
		uintptr(flags),
		uintptr(reserved),
		uintptr(nNumberOfBytesToLockLow),
		uintptr(nNumberOfBytesToLockHigh),
		uintptr(unsafe.Pointer(overlapped)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

func unlockFileEx(handle syscall.Handle, reserved uint32, nNumberOfBytesToUnlockLow uint32, nNumberOfBytesToUnlockHigh uint32, overlapped *syscall.Overlapped) error {
	ret, _, err := procUnlockFileEx.Call(
		uintptr(handle),
		uintptr(reserved),
		uintptr(nNumberOfBytesToUnlockLow),
		uintptr(nNumberOfBytesToUnlockHigh),
		uintptr(unsafe.Pointer(overlapped)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

func (f *Flock) tryLock() (bool, error) {
	var ov syscall.Overlapped
	err := lockFileEx(
		syscall.Handle(f.file.Fd()),
		lockFileExclusiveLock|lockFileFailImmediately,
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		&ov,
	)
	if err == nil {
		return true, nil
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errorLockViolation {
		return false, nil
	}
	return false, err
}

func (f *Flock) unlock() error {
	var ov syscall.Overlapped
	return unlockFileEx(
		syscall.Handle(f.file.Fd()),
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		&ov,
	)
}
