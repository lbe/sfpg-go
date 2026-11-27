//go:build freebsd

package readdir

import (
	"syscall"
	"unsafe"
)

// Inode field location in syscall.Dirent (named Fileno on FreeBSD).
var (
	offDirentIno  = unsafe.Offsetof(syscall.Dirent{}.Fileno)
	sizeDirentIno = unsafe.Sizeof(syscall.Dirent{}.Fileno)
)
