package parallelwalkdir

import (
	"context"
	"io/fs"
)

// DirEntState holds catalog fields needed to decide modified vs unchanged without a per-file DB call.
type DirEntState struct {
	InvalidPathValid bool
	InvalidMtime     int64
	InvalidSize      int64
	FileIDValid      bool
	FileMtimeValid   bool
	FileMtime        int64
	FileSizeValid    bool
	FileSize         int64
	FileMD5Valid     bool
}

// GetDirEntMapFunc loads per-directory catalog state keyed by HashPathBytes(basename).
type GetDirEntMapFunc func(ctx context.Context, reportedDir []byte) (map[PathHash]DirEntState, error)

// CheckIfFileModifiedFunc decides whether a regular file must be reported on results.
// modified == true means send on results (needs discovery processing).
type CheckIfFileModifiedFunc func(name []byte, info fs.FileInfo, state DirEntState, inMap bool) (modified bool, err error)

// WithDirEntCatalog enables per-directory catalog filtering before reporting files.
// Requires WithBasenameInclude and WithSizeNotZero. Mutually exclusive with
// WithRegexpInclude and WithValidationFunc.
func WithDirEntCatalog(getMap GetDirEntMapFunc, check CheckIfFileModifiedFunc) Option {
	return func(w *Walker) {
		w.getDirEntMap = getMap
		w.checkIfFileModified = check
	}
}
