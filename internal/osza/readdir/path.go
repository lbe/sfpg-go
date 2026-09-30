package readdir

import "syscall"

// CopyDirPath copies dir into buf and returns dirLen for ReadDir. buf must have
// capacity at len(dir) for the temporary NUL used by openDir on Unix.
func CopyDirPath(buf []byte, dir string) (int, error) {
	n := len(dir)
	if n == 0 || n >= len(buf) {
		return 0, syscall.EINVAL
	}
	copy(buf[:n], dir)
	return n, nil
}
