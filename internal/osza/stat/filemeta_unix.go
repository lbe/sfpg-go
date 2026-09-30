//go:build linux || darwin

package stat

import (
	"io/fs"
	"syscall"
	"time"
)

// FileMeta holds metadata from LstatAt/StatAt without a heap path string.
// It implements fs.FileInfo; Name always returns "" (paths live in caller buffers).
type FileMeta struct {
	sys     syscall.Stat_t
	size    int64
	modTime time.Time
	mode    fs.FileMode
}

func (m *FileMeta) Name() string { return "" }

func (m *FileMeta) Size() int64 { return m.size }

func (m *FileMeta) Mode() fs.FileMode { return m.mode }

func (m *FileMeta) ModTime() time.Time { return m.modTime }

func (m *FileMeta) IsDir() bool { return m.mode.IsDir() }

func (m *FileMeta) Sys() any { return &m.sys }

var _ fs.FileInfo = (*FileMeta)(nil)

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
