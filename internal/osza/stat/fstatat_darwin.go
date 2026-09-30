//go:build darwin

package stat

import (
	"syscall"
	"unsafe"
)

// syscallSyscall6 calls a libSystem function through the runtime's libc entry
// point, the only supported way to reach darwin kernel services from Go.
//
//go:linkname syscallSyscall6 syscall.syscall6
func syscallSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

func platformFstatat(pathBuf []byte, st *syscall.Stat_t, flags int) error {
	if len(pathBuf) == 0 {
		return syscall.EINVAL
	}
	return fstatatSyscall(atFDCWD, pathBuf, st, flags)
}

func fstatatSyscall(dirfd int, pathBuf []byte, st *syscall.Stat_t, flags int) error {
	_, _, errno := syscallSyscall6(
		libcFstatatTrampolineAddr,
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
