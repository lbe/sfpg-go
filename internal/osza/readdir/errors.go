package readdir

import "errors"

var errShortScratch = errors.New("osza/readdir: scratch too small (Unix requires >= 8192 bytes)")

// ErrShortScratch is returned on Linux and macOS when len(scratch) < 8192 before
// the first ReadDirent. Windows ignores scratch and never returns this error.
var ErrShortScratch = errShortScratch
