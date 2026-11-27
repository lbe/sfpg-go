//go:build linux || darwin

package readdir

import (
	"syscall"
	"unsafe"
)

// Inode field location in syscall.Dirent (named Ino on Linux and Darwin).
var (
	offDirentIno  = unsafe.Offsetof(syscall.Dirent{}.Ino)
	sizeDirentIno = unsafe.Sizeof(syscall.Dirent{}.Ino)
)
