package osza

import "github.com/lbe/sfpg-go/internal/osza/evalsymlinks"

// ErrTooManySymlinks is returned when EvalSymlinksAt encounters more than 255
// symbolic links (same limit as filepath.EvalSymlinks).
var ErrTooManySymlinks = evalsymlinks.ErrTooManySymlinks

// EvalSymlinksAt resolves symbolic links in pathBuf[:pathLen] into dest without
// converting the input path to string. pathBuf must have capacity at pathLen for
// temporary NUL bytes and enough total capacity for symlink-expanded paths; dest
// must hold the cleaned result. Grow pathBuf or dest and retry on ErrPathBuffer.
// pathBuf and dest must not overlap.
func EvalSymlinksAt(pathBuf []byte, pathLen int, dest []byte) (canonicalLen int, err error) {
	return evalsymlinks.EvalSymlinksAt(pathBuf, pathLen, dest)
}
