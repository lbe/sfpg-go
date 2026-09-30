// Package readdir implements allocation-free directory reading into
// caller-provided storage.
//
// Production code should import github.com/lbe/sfpg-go/internal/osza and call
// osza.ReadDir. This package exists for platform-specific implementation and
// focused tests (readdir_test).
//
// Unix (linux, darwin): openat(AT_FDCWD) on dirPathBuf with O_DIRECTORY and ReadDirent into scratch.
// ReadDirent records are parsed by field offset; do not cast scratch to *syscall.Dirent
// (alignment / checkptr).
// Windows: FindFirstFile / FindNextFile; scratch is unused.
package readdir

var (
	nameDot    = []byte(".")
	nameDotDot = []byte("..")
)

// ErrOverflow is returned when entries or nameBuf are too small to
// hold the full directory listing. n still reflects entries filled.
var ErrOverflow = errOverflow

// ReadDir reads dir, filling entries[0:n] and storing names in nameBuf.
// It returns the number of entries filled. "." and ".." are skipped.
// Entries are unsorted. No allocations are performed per entry on Unix when
// the dirent type is known.
//
// scratch: OS read buffer, >= 8192 bytes required on Unix (ErrShortScratch
// otherwise); may be nil on Windows.
//
// dirPathBuf: directory path bytes at dirPathBuf[:dirLen]. Capacity at dirLen for a
// temporary NUL used by openDir on Unix.
//
// joinBuf: reused for stat.LstatJoin when dirent type is unknown (Unix) or for
// Entry.Info. Capacity >= dirLen+1+maxNameLen+1 for each entry in the listing.
func ReadDir(dirPathBuf []byte, dirLen int, entries []Entry, nameBuf, scratch, joinBuf []byte) (n int, err error) {
	return readDir(dirPathBuf, dirLen, entries, nameBuf, scratch, joinBuf)
}
