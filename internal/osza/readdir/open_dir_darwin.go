//go:build darwin

package readdir

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openDir(pathBuf []byte, pathLen int) (int, error) {
	return openDirAt(unix.AT_FDCWD, pathBuf, pathLen)
}

func openDirAt(dirfd int, pathBuf []byte, pathLen int) (int, error) {
	if pathLen <= 0 || pathLen >= len(pathBuf) {
		return -1, syscall.EINVAL
	}
	saved := pathBuf[pathLen]
	pathBuf[pathLen] = 0
	defer func() { pathBuf[pathLen] = saved }()

	fd, _, errno := unix.Syscall6(
		unix.SYS_OPENAT,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(&pathBuf[0])),
		uintptr(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
		0, 0, 0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}
