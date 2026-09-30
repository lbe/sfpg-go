//go:build linux

package evalsymlinks

import (
	"syscall"
	"unsafe"
)

const atFDCWD = -0x64

func platformReadlink(pathBuf []byte, linkBuf []byte) (int, error) {
	if len(pathBuf) == 0 {
		return 0, syscall.EINVAL
	}
	return readlinkatSyscall(atFDCWD, pathBuf, linkBuf)
}

func readlinkatSyscall(dirfd int, pathBuf []byte, linkBuf []byte) (int, error) {
	n, _, errno := syscall.Syscall6(
		syscall.SYS_READLINKAT,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(&pathBuf[0])),
		uintptr(unsafe.Pointer(&linkBuf[0])),
		uintptr(len(linkBuf)),
		0,
		0,
	)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
