//go:build linux || darwin

package evalsymlinks

import (
	"io/fs"
	"os"
	"syscall"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

const maxSymlinks = 255

// EvalSymlinksAt resolves symbolic links in pathBuf[:pathLen] and writes the
// canonical path into dest. The result matches filepath.EvalSymlinks for
// discovery-style Unix paths (absolute/relative, "..", symlink loops).
//
// pathBuf must have capacity at pathLen for temporary NUL bytes used during
// syscalls, and enough total capacity to hold the longest intermediate path
// while following symlinks (at least pathLen; grow and retry on ErrPathBuffer).
// dest must be large enough for the final cleaned path; grow and retry on
// ErrPathBuffer. pathBuf and dest must not overlap.
//
// The success path is heap-free when pathBuf and dest have sufficient capacity
// (no ErrPathBuffer); buffer growth and error returns may still allocate.
func EvalSymlinksAt(pathBuf []byte, pathLen int, dest []byte) (canonicalLen int, err error) {
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return 0, pathError("EvalSymlinks", pathBuf, pathLen, syscall.EINVAL)
	}
	if len(dest) == 0 {
		return 0, stat.ErrPathBuffer
	}

	pathEnd := pathLen
	volLen := 0
	if volLen < pathEnd && os.IsPathSeparator(pathBuf[volLen]) {
		volLen++
	}
	destLen, err := appendBytes(dest, 0, pathBuf[:volLen])
	if err != nil {
		return 0, err
	}

	linksWalked := 0
	var meta stat.FileMeta
	var linkBuf [4096]byte

	for start := volLen; start < pathEnd; {
		for start < pathEnd && os.IsPathSeparator(pathBuf[start]) {
			start++
		}
		end := start
		for end < pathEnd && !os.IsPathSeparator(pathBuf[end]) {
			end++
		}
		if end == start {
			break
		}
		if end-start == 1 && pathBuf[start] == '.' {
			start = end
			continue
		}
		if end-start == 2 && pathBuf[start] == '.' && pathBuf[start+1] == '.' {
			r := destLen - 1
			for r >= volLen && !os.IsPathSeparator(dest[r]) {
				r--
			}
			keepDotDot := r < volLen
			if !keepDotDot && destLen >= r+3 && dest[r+1] == '.' && dest[r+2] == '.' && (destLen == r+3 || os.IsPathSeparator(dest[r+3])) {
				keepDotDot = true
			}
			if keepDotDot {
				if destLen > volLen {
					destLen, err = appendByte(dest, destLen, os.PathSeparator)
					if err != nil {
						return 0, err
					}
				}
				destLen, err = appendDotDot(dest, destLen)
				if err != nil {
					return 0, err
				}
			} else {
				destLen = r
			}
			start = end
			continue
		}

		if destLen > volLen && !os.IsPathSeparator(dest[destLen-1]) {
			destLen, err = appendByte(dest, destLen, os.PathSeparator)
			if err != nil {
				return 0, err
			}
		}
		destLen, err = appendBytes(dest, destLen, pathBuf[start:end])
		if err != nil {
			return 0, err
		}

		if err := stat.LstatAt(dest, destLen, &meta); err != nil {
			return 0, err
		}
		mode := meta.Mode()
		if mode&fs.ModeSymlink == 0 {
			if !mode.IsDir() && end < pathEnd {
				return 0, pathError("EvalSymlinks", dest, destLen, syscall.ENOTDIR)
			}
			start = end
			continue
		}

		linksWalked++
		if linksWalked > maxSymlinks {
			return 0, ErrTooManySymlinks
		}

		linkLen, err := readlinkAt(dest, destLen, linkBuf[:])
		if err != nil {
			return 0, pathError("EvalSymlinks", dest, destLen, err)
		}
		link := linkBuf[:linkLen]

		restLen := pathEnd - end
		newPathLen := linkLen + restLen
		if newPathLen > len(pathBuf) {
			return 0, stat.ErrPathBuffer
		}
		if restLen > 0 {
			copy(pathBuf[linkLen:linkLen+restLen], pathBuf[end:pathEnd])
		}
		copy(pathBuf[:linkLen], link)
		pathEnd = newPathLen

		if linkLen > 0 && os.IsPathSeparator(link[0]) {
			destLen = 0
			destLen, err = appendBytes(dest, destLen, link[:1])
			if err != nil {
				return 0, err
			}
			volLen = 1
			start = 1
			continue
		}

		r := destLen - 1
		for r >= volLen && !os.IsPathSeparator(dest[r]) {
			r--
		}
		destLen = max(r, volLen)
		start = 0
	}

	return unixCleanInPlace(dest, destLen)
}

func readlinkAt(pathBuf []byte, pathLen int, linkBuf []byte) (int, error) {
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return 0, syscall.EINVAL
	}
	saved := pathBuf[pathLen]
	pathBuf[pathLen] = 0
	n, err := platformReadlink(pathBuf[:pathLen+1], linkBuf)
	pathBuf[pathLen] = saved
	if err != nil {
		return 0, err
	}
	if n >= len(linkBuf) {
		return 0, stat.ErrPathBuffer
	}
	return n, nil
}

func appendDotDot(dest []byte, n int) (int, error) {
	if n+2 > len(dest) {
		return 0, stat.ErrPathBuffer
	}
	dest[n] = '.'
	dest[n+1] = '.'
	return n + 2, nil
}

func appendByte(dest []byte, n int, b byte) (int, error) {
	if n+1 > len(dest) {
		return 0, stat.ErrPathBuffer
	}
	dest[n] = b
	return n + 1, nil
}

func appendBytes(dest []byte, n int, b []byte) (int, error) {
	if n+len(b) > len(dest) {
		return 0, stat.ErrPathBuffer
	}
	copy(dest[n:], b)
	return n + len(b), nil
}

func pathError(op string, pathBuf []byte, pathLen int, err error) error {
	if err == nil {
		return nil
	}
	path := ""
	if pathLen >= 0 && pathLen <= len(pathBuf) {
		path = string(pathBuf[:pathLen])
	}
	return &fs.PathError{Op: op, Path: path, Err: err}
}
