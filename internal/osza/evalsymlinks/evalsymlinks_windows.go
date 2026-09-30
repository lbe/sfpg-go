//go:build windows

package evalsymlinks

import (
	"path/filepath"
	"syscall"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

// EvalSymlinksAt resolves symbolic links using filepath.EvalSymlinks. Windows
// has no byte-oriented readlink in osza; this converts pathBuf[:pathLen] to
// string once per call and may allocate. Semantics match filepath.EvalSymlinks
// for discovery-style walks.
func EvalSymlinksAt(pathBuf []byte, pathLen int, dest []byte) (canonicalLen int, err error) {
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return 0, syscall.EINVAL
	}
	if len(dest) == 0 {
		return 0, stat.ErrPathBuffer
	}
	cleaned, err := filepath.EvalSymlinks(string(pathBuf[:pathLen]))
	if err != nil {
		return 0, err
	}
	n := len(cleaned)
	if n > len(dest) {
		return 0, stat.ErrPathBuffer
	}
	copy(dest, cleaned)
	return n, nil
}
