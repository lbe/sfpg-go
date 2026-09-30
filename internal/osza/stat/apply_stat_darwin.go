//go:build darwin

package stat

import (
	"io/fs"
	"syscall"
	"time"
)

func applyStatToFileMeta(dst *FileMeta, st *syscall.Stat_t) {
	dst.sys = *st
	dst.size = st.Size
	dst.modTime = time.Unix(st.Mtimespec.Unix())
	dst.mode = modeFromStat(st)
}

func modeFromStat(st *syscall.Stat_t) fs.FileMode {
	mode := fs.FileMode(st.Mode & 0o777)
	switch st.Mode & syscall.S_IFMT {
	case syscall.S_IFBLK, syscall.S_IFWHT:
		mode |= fs.ModeDevice
	case syscall.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case syscall.S_IFDIR:
		mode |= fs.ModeDir
	case syscall.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= fs.ModeSymlink
	case syscall.S_IFREG:
	case syscall.S_IFSOCK:
		mode |= fs.ModeSocket
	}
	if st.Mode&syscall.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if st.Mode&syscall.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if st.Mode&syscall.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}
