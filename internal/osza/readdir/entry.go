package readdir

import (
	"io/fs"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

// IsDir reports whether the entry describes a directory.
func (e Entry) IsDir() bool {
	return e.typ.IsDir()
}

// Type returns the file mode bits describing the entry type.
func (e Entry) Type() fs.FileMode {
	return e.typ
}

// Entry is a directory entry. Name points into the caller's nameBuf.
// Call Info on the slice element (entries[i].Info(joinBuf)), not on a copied Entry value.
type Entry struct {
	Name    []byte
	dirPath []byte // dirPathBuf[:dirLen] from the ReadDir that filled this entry
	dirLen  int
	typ     fs.FileMode
	meta    stat.FileMeta
	hasMeta bool
}
