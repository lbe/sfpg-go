//go:build linux

package stat

import (
	"syscall"
	"unsafe"
)

func platformFstatat(pathBuf []byte, st *syscall.Stat_t, flags int) error {
	if len(pathBuf) == 0 {
		return syscall.EINVAL
	}
	return fstatatSyscall(atFDCWD, pathBuf, st, flags)
}

func fstatatSyscall(dirfd int, pathBuf []byte, st *syscall.Stat_t, flags int) error {
	_, _, errno := syscall.Syscall6(
		fstatatTrap,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(&pathBuf[0])),
		uintptr(unsafe.Pointer(st)),
		uintptr(flags),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
