package osza

import "github.com/lbe/sfpg-go/internal/osza/readdir"

// Entry is one directory entry returned by ReadDir. The Name field is a sub-slice
// of the nameBuf passed to ReadDir; do not retain Name after reusing nameBuf.
//
// IsDir, Type, and Info mirror fs.DirEntry semantics. Call Info on the slice
// element (entries[i].Info(joinBuf)) so Unix lazy stat results are cached on the entry.
// For a string name, use string(entries[i].Name) (allocates).
type Entry = readdir.Entry

// ErrOverflow means entries or nameBuf were too small for the full listing.
// ReadDir still returns n entries filled before overflow. Compare with
// errors.Is(err, osza.ErrOverflow), not err == osza.ErrOverflow.
var ErrOverflow = readdir.ErrOverflow

// ErrShortScratch means scratch was shorter than 8192 bytes on Unix.
// Windows never returns this error (scratch may be nil).
var ErrShortScratch = readdir.ErrShortScratch

// CopyDirPath copies dir into buf for ReadDir (see readdir.CopyDirPath).
func CopyDirPath(buf []byte, dir string) (int, error) {
	return readdir.CopyDirPath(buf, dir)
}

// ReadDir reads a single directory into entries and nameBuf.
//
// It returns the number of entries written to entries[0:n]. "." and ".." are
// omitted. Order is undefined (not sorted). ReadDir does not allocate per
// entry on Unix when the dirent type is known.
//
// entries holds up to len(entries) results. nameBuf stores all entry names
// contiguously; each Entry.Name points into nameBuf. If either cap is exceeded,
// ReadDir returns (n, ErrOverflow) with partial results in entries[0:n].
//
// scratch is the OS directory-read buffer. On Linux and macOS, use at least
// 8192 bytes. On Windows, scratch is ignored and may be nil.
//
// dirPathBuf holds the directory path at dirPathBuf[:dirLen]. Capacity at dirLen
// for a temporary NUL used by openDir on Unix. Reuse per goroutine.
//
// joinBuf is reused for stat.LstatJoin (unknown dirent type on Unix) and for
// Entry.Info. Capacity >= dirLen+1+maxNameLen+1 for names in the listing.
// joinBuf must not overlap dirPathBuf, nameBuf, or scratch (packJoin/joinPacked
// and ReadDirent mutate buffers in place). All listed slices must be disjoint.
// They are not goroutine-safe: use one buffer set per goroutine with no
// concurrent ReadDir or *At/*Join on the same slices.
//
// On Unix, unknown dirent types may return (n, ErrPathBuffer) with partial
// entries[:n] when joinBuf is too small (same sizing as above). Grow joinBuf and
// re-read from scratch; do not retry only on ErrOverflow.
//
// For a full listing when buffers are too small, grow len(entries) and
// len(nameBuf) on ErrOverflow, and len(joinBuf) on ErrPathBuffer; call ReadDir
// again from scratch until err is nil (ReadDir bounds on len(entries), not cap).
// See internal/osza/example/read_dir_retry/main.go.
//
// Other errors (missing dir, permission, etc.) are returned from the underlying
// syscall layer and may be wrapped; use errors.Is/errors.As as usual.
func ReadDir(dirPathBuf []byte, dirLen int, entries []Entry, nameBuf, scratch, joinBuf []byte) (n int, err error) {
	return readdir.ReadDir(dirPathBuf, dirLen, entries, nameBuf, scratch, joinBuf)
}
