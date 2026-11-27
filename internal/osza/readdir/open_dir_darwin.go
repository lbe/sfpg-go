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

// openDirAt opens pathBuf[:pathLen] relative to dirfd. Darwin supports system
// calls only through libSystem, so it calls unix.Openat, which copies the path
// to add the NUL; pathBuf is not modified.
func openDirAt(dirfd int, pathBuf []byte, pathLen int) (int, error) {
	if pathLen <= 0 || pathLen >= len(pathBuf) {
		return -1, syscall.EINVAL
	}
	path := unsafe.String(unsafe.SliceData(pathBuf), pathLen)
	fd, err := unix.Openat(dirfd, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	return fd, nil
}
