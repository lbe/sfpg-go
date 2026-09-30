//go:build windows

package stat

import "syscall"

// FillMetaFromFindData fills meta from a FindFirstFile/FindNextFile record (ReadDir).
func FillMetaFromFindData(meta *FileMeta, d *syscall.Win32finddata) {
	meta.applyFromWin32finddata(d)
}
