//go:build windows

package readdir

import (
	"bytes"
	"errors"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

var errOverflow = errors.New("osza/readdir: entries or nameBuf too small")

func readDir(dirPathBuf []byte, dirLen int, entries []Entry, nameBuf, _, _ []byte) (int, error) {
	var pathBuf [syscall.MAX_PATH + 4]uint16
	p := 0
	for off := 0; off < dirLen; {
		if p >= len(pathBuf)-4 {
			return 0, syscall.ENAMETOOLONG
		}
		r, size := utf8.DecodeRune(dirPathBuf[off:dirLen])
		if size == 0 {
			break
		}
		off += size
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			pathBuf[p], pathBuf[p+1] = uint16(r1), uint16(r2)
			p += 2
		} else {
			pathBuf[p] = uint16(r)
			p++
		}
	}
	pathBuf[p], pathBuf[p+1], pathBuf[p+2] = '\\', '*', 0

	var fd syscall.Win32finddata
	h, err := syscall.FindFirstFile(&pathBuf[0], &fd)
	if err != nil {
		return 0, err
	}
	defer syscall.FindClose(h)

	n, used := 0, 0
	for {
		name, ok := utf16ToBuf(fd.FileName[:], nameBuf[used:])
		if !(len(name) == 0 || bytes.Equal(name, nameDot) || bytes.Equal(name, nameDotDot)) {
			if !ok || n >= len(entries) {
				return n, errOverflow
			}
			used += len(name)
			var meta stat.FileMeta
			stat.FillMetaFromFindData(&meta, &fd)
			entries[n] = Entry{
				Name:    name,
				dirPath: dirPathBuf,
				dirLen:  dirLen,
				typ:     meta.Mode().Type(),
				meta:    meta,
				hasMeta: true,
			}
			n++
		}
		if err := syscall.FindNextFile(h, &fd); err != nil {
			if err == syscall.ERROR_NO_MORE_FILES {
				return n, nil
			}
			return n, err
		}
	}
}

// utf16ToBuf decodes a NUL-terminated UTF-16 name into buf.
// ok is false if buf was too small.
func utf16ToBuf(s []uint16, buf []byte) (name []byte, ok bool) {
	n := 0
	for i := 0; i < len(s) && s[i] != 0; i++ {
		r := rune(s[i])
		if utf16.IsSurrogate(r) && i+1 < len(s) {
			r = utf16.DecodeRune(r, rune(s[i+1]))
			i++
		}
		if n+utf8.UTFMax > len(buf) {
			return buf[:n], false
		}
		n += utf8.EncodeRune(buf[n:], r)
	}
	return buf[:n], true
}
