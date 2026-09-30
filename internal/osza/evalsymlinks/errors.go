package evalsymlinks

import "errors"

var errTooManySymlinks = errors.New("osza/evalsymlinks: too many links")

// ErrTooManySymlinks is returned when more than 255 symbolic links are encountered
// while resolving a path (same limit as filepath.EvalSymlinks).
var ErrTooManySymlinks = errTooManySymlinks
