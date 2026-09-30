//go:build windows

package stat

import (
	"io/fs"
	"syscall"
	"time"
)

const (
	ioReparseTagAFUnix    = 0x80000023
	ioReparseTagDedup     = 0x80000013
	errorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
)

// FileMeta holds metadata from LstatAt/StatAt without a heap path string.
// It implements fs.FileInfo; Name always returns "" (paths live in caller buffers).
type FileMeta struct {
	attr       syscall.Win32FileAttributeData
	reparseTag uint32
	fileType   uint32
	size       int64
	modTime    time.Time
	mode       fs.FileMode
}

func (m *FileMeta) Name() string { return "" }

func (m *FileMeta) Size() int64 { return m.size }

func (m *FileMeta) Mode() fs.FileMode { return m.mode }

func (m *FileMeta) ModTime() time.Time { return m.modTime }

func (m *FileMeta) IsDir() bool { return m.mode.IsDir() }

func (m *FileMeta) Sys() interface{} {
	return &syscall.Win32FileAttributeData{
		FileAttributes: m.attr.FileAttributes,
		CreationTime:   m.attr.CreationTime,
		LastAccessTime: m.attr.LastAccessTime,
		LastWriteTime:  m.attr.LastWriteTime,
		FileSizeHigh:   m.attr.FileSizeHigh,
		FileSizeLow:    m.attr.FileSizeLow,
	}
}

var _ fs.FileInfo = (*FileMeta)(nil)

func pathError(op string, pathBuf []byte, pathLen int, err error) error {
	if err == nil {
		return nil
	}
	path := ""
	if pathLen >= 0 && pathLen <= len(pathBuf) {
		path = string(pathBuf[:pathLen])
	}
	return &fs.PathError{Op: op, Path: path, Err: mapWinErr(err)}
}

func mapWinErr(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(syscall.Errno); ok {
		if e == syscall.ERROR_FILE_NOT_FOUND || e == syscall.ERROR_PATH_NOT_FOUND {
			return fs.ErrNotExist
		}
	}
	return err
}

func (m *FileMeta) applyFromWin32FileAttributeData(d *syscall.Win32FileAttributeData, reparseTag uint32) {
	m.attr = *d
	m.reparseTag = reparseTag
	m.size = int64(d.FileSizeHigh)<<32 + int64(d.FileSizeLow)
	m.modTime = time.Unix(0, d.LastWriteTime.Nanoseconds())
	m.mode = modeFromWin32(d.FileAttributes, reparseTag, m.fileType)
}

func (m *FileMeta) applyFromWin32finddata(d *syscall.Win32finddata) {
	var tag uint32
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		tag = d.Reserved0
	}
	m.applyFromWin32FileAttributeData(&syscall.Win32FileAttributeData{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   d.FileSizeHigh,
		FileSizeLow:    d.FileSizeLow,
	}, tag)
}

func (m *FileMeta) applyFromByHandle(d *syscall.ByHandleFileInformation, reparseTag uint32) {
	m.applyFromWin32FileAttributeData(&syscall.Win32FileAttributeData{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   d.FileSizeHigh,
		FileSizeLow:    d.FileSizeLow,
	}, reparseTag)
}

func isReparseTagNameSurrogate(fileAttributes uint32, reparseTag uint32) bool {
	return fileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && reparseTag&0x20000000 != 0
}

func modeFromWin32(fileAttributes, reparseTag, fileType uint32) fs.FileMode {
	var m fs.FileMode
	if fileAttributes&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		m |= 0444
	} else {
		m |= 0666
	}
	if !isReparseTagNameSurrogate(fileAttributes, reparseTag) {
		if fileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
			m |= fs.ModeDir | 0111
		}
		switch fileType {
		case syscall.FILE_TYPE_PIPE:
			m |= fs.ModeNamedPipe
		case syscall.FILE_TYPE_CHAR:
			m |= fs.ModeDevice | fs.ModeCharDevice
		}
	}
	if fileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		switch reparseTag {
		case syscall.IO_REPARSE_TAG_SYMLINK:
			m |= fs.ModeSymlink
		case ioReparseTagAFUnix:
			m |= fs.ModeSocket
		case ioReparseTagDedup:
		default:
			if m&fs.ModeType == 0 {
				m |= fs.ModeIrregular
			}
		}
	}
	return m
}
