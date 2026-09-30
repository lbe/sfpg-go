//go:build windows

package stat

import (
	"io/fs"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const maxUTF16Path = syscall.MAX_PATH + 12

func utf8PathToUTF16(pathBuf []byte, pathLen int, wcharBuf []uint16) (int, error) {
	if pathLen < 0 || pathLen > len(pathBuf) {
		return 0, syscall.EINVAL
	}
	n := 0
	for i := 0; i < pathLen; {
		if n >= len(wcharBuf)-1 {
			return 0, syscall.ENAMETOOLONG
		}
		r, size := utf8.DecodeRune(pathBuf[i:pathLen])
		if r == utf8.RuneError && size == 1 {
			return 0, syscall.Errno(1113) // ERROR_NO_UNICODE_TRANSLATION
		}
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			if n+1 >= len(wcharBuf)-1 {
				return 0, syscall.ENAMETOOLONG
			}
			wcharBuf[n], wcharBuf[n+1] = uint16(r1), uint16(r2)
			n += 2
		} else {
			wcharBuf[n] = uint16(r)
			n++
		}
		i += size
	}
	wcharBuf[n] = 0
	return n, nil
}

func reparseTagFromFindFirst(namep *uint16) (uint32, error) {
	var fd syscall.Win32finddata
	h, err := syscall.FindFirstFile(namep, &fd)
	if err != nil {
		return 0, err
	}
	syscall.FindClose(h)
	if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return 0, nil
	}
	return fd.Reserved0, nil
}

func statMetaFromHandle(h syscall.Handle, namep *uint16, dest *FileMeta) error {
	ft, err := syscall.GetFileType(h)
	if err != nil {
		return err
	}
	dest.fileType = ft
	if ft == syscall.FILE_TYPE_PIPE || ft == syscall.FILE_TYPE_CHAR {
		dest.size = 0
		dest.modTime = time.Time{}
		dest.mode = modeFromWin32(0, 0, ft)
		return nil
	}
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return err
	}
	var tag uint32
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		tag, err = reparseTagFromFindFirst(namep)
		if err != nil {
			return err
		}
	}
	dest.applyFromByHandle(&d, tag)
	return nil
}

// platformStat fills dest from pathBuf[:pathLen] (UTF-8). followSurrogates matches os.Lstat vs os.Stat symlink semantics.
func platformStat(pathBuf []byte, pathLen int, dest *FileMeta, followSurrogates bool) error {
	if pathLen == 0 {
		return syscall.Errno(syscall.ERROR_PATH_NOT_FOUND)
	}
	var wcharBuf [maxUTF16Path]uint16
	_, err := utf8PathToUTF16(pathBuf, pathLen, wcharBuf[:])
	if err != nil {
		return err
	}
	namep := &wcharBuf[0]

	var fa syscall.Win32FileAttributeData
	err = syscall.GetFileAttributesEx(namep, syscall.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&fa)))
	if err != nil && mapWinErr(err) == fs.ErrNotExist {
		return fs.ErrNotExist
	}
	if err == nil && fa.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		dest.fileType = 0
		dest.applyFromWin32FileAttributeData(&fa, 0)
		return nil
	}

	if err == errorSharingViolation {
		var fd syscall.Win32finddata
		sh, ferr := syscall.FindFirstFile(namep, &fd)
		if ferr != nil {
			return ferr
		}
		syscall.FindClose(sh)
		if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			dest.fileType = 0
			dest.applyFromWin32finddata(&fd)
			return nil
		}
	}

	flags := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS | syscall.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := syscall.CreateFile(namep, 0, 0, nil, syscall.OPEN_EXISTING, flags, 0)
	if err == syscall.Errno(87) { // ERROR_INVALID_PARAMETER
		h, err = syscall.CreateFile(namep, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, flags, 0)
	}
	if err != nil {
		return err
	}
	if err := statMetaFromHandle(h, namep, dest); err != nil {
		syscall.CloseHandle(h)
		return err
	}
	syscall.CloseHandle(h)

	if followSurrogates && isReparseTagNameSurrogate(dest.attr.FileAttributes, dest.reparseTag) {
		h, err = syscall.CreateFile(namep, 0, 0, nil, syscall.OPEN_EXISTING, uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS), 0)
		if err != nil {
			return err
		}
		defer syscall.CloseHandle(h)
		return statMetaFromHandle(h, namep, dest)
	}
	return nil
}
