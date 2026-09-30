//go:build linux || darwin

package readdir

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

var errOverflow = errors.New("osza/readdir: entries or nameBuf too small")

var errInvalidDirent = errors.New("osza/readdir: invalid dirent record")

const minScratchBytes = 8192

var (
	offDirentReclen  = unsafe.Offsetof(syscall.Dirent{}.Reclen)
	sizeDirentReclen = unsafe.Sizeof(syscall.Dirent{}.Reclen)
	offDirentIno     = unsafe.Offsetof(syscall.Dirent{}.Ino)
	sizeDirentIno    = unsafe.Sizeof(syscall.Dirent{}.Ino)
	offDirentName    = unsafe.Offsetof(syscall.Dirent{}.Name)
	minDirentRec     = int(offDirentName) + 1
)

func readDir(dirPathBuf []byte, dirLen int, entries []Entry, nameBuf, scratch, joinBuf []byte) (int, error) {
	if len(scratch) < minScratchBytes {
		return 0, ErrShortScratch
	}
	fd, err := openDir(dirPathBuf, dirLen)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd)

	n := 0    // entries filled
	used := 0 // bytes of nameBuf consumed
	for {
		rn, err := syscall.ReadDirent(fd, scratch)
		if err != nil {
			return n, err
		}
		if rn <= 0 {
			return n, nil
		}
		b := scratch[:rn]
		for len(b) > 0 {
			reclen, ok := direntReclen(b)
			if !ok || reclen == 0 || int(reclen) > len(b) || int(reclen) < minDirentRec {
				return n, errInvalidDirent
			}
			rec := b[:reclen]
			b = b[reclen:]

			ino, ok := direntIno(rec)
			if !ok {
				return n, errInvalidDirent
			}
			if ino == 0 && runtime.GOOS != "linux" {
				continue
			}

			name := direntName(rec)
			if len(name) == 0 || bytes.Equal(name, nameDot) || bytes.Equal(name, nameDotDot) {
				continue
			}
			if n >= len(entries) || used+len(name) > len(nameBuf) {
				return n, errOverflow
			}
			dst := nameBuf[used : used+len(name)]
			copy(dst, name)
			used += len(name)

			typ := direntType(rec)
			var meta stat.FileMeta
			hasMeta := false
			if typ == ^fs.FileMode(0) {
				parentLen := dirLen
				nameLen := len(dst)
				parent := dirPathBuf[:dirLen]
				if packErr := packJoin(joinBuf, parent, dst); packErr != nil {
					return n, packErr
				}
				err = stat.LstatJoin(joinBuf, parentLen, nameLen, &meta)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return n, err
				}
				typ = meta.Mode().Type()
				hasMeta = true
			}
			entries[n] = Entry{Name: dst, dirPath: dirPathBuf, dirLen: dirLen, typ: typ, meta: meta, hasMeta: hasMeta}
			n++
		}
	}
}

func readFieldLE(b []byte, off, size uintptr) (uint64, bool) {
	if len(b) < int(off+size) {
		return 0, false
	}
	p := b[off:]
	switch size {
	case 1:
		return uint64(p[0]), true
	case 2:
		return uint64(binary.LittleEndian.Uint16(p)), true
	case 4:
		return uint64(binary.LittleEndian.Uint32(p)), true
	case 8:
		return binary.LittleEndian.Uint64(p), true
	default:
		return 0, false
	}
}

func direntReclen(rec []byte) (uint16, bool) {
	u, ok := readFieldLE(rec, offDirentReclen, sizeDirentReclen)
	return uint16(u), ok
}

func direntIno(rec []byte) (uint64, bool) {
	return readFieldLE(rec, offDirentIno, sizeDirentIno)
}

func direntName(rec []byte) []byte {
	if len(rec) < minDirentRec {
		return nil
	}
	nb := rec[offDirentName:]
	for i, c := range nb {
		if c == 0 {
			return nb[:i]
		}
	}
	return nb
}

func direntType(rec []byte) fs.FileMode {
	off := unsafe.Offsetof(syscall.Dirent{}.Type)
	if off >= uintptr(len(rec)) {
		return ^fs.FileMode(0)
	}
	typ := rec[off]
	switch typ {
	case syscall.DT_BLK:
		return fs.ModeDevice
	case syscall.DT_CHR:
		return fs.ModeDevice | fs.ModeCharDevice
	case syscall.DT_DIR:
		return fs.ModeDir
	case syscall.DT_FIFO:
		return fs.ModeNamedPipe
	case syscall.DT_LNK:
		return fs.ModeSymlink
	case syscall.DT_REG:
		return 0
	case syscall.DT_SOCK:
		return fs.ModeSocket
	}
	return ^fs.FileMode(0)
}
