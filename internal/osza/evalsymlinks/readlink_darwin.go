//go:build darwin

package evalsymlinks

import (
	"syscall"
	"unsafe"
)

func platformReadlink(pathBuf []byte, linkBuf []byte) (int, error) {
	if len(pathBuf) == 0 {
		return 0, syscall.EINVAL
	}
	n, _, errno := syscall.Syscall(
		syscall.SYS_READLINK,
		uintptr(unsafe.Pointer(&pathBuf[0])),
		uintptr(unsafe.Pointer(&linkBuf[0])),
		uintptr(len(linkBuf)),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
