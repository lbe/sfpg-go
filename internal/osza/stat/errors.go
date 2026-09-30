package stat

import "errors"

var errPathBuffer = errors.New("osza/stat: joinBuf too small")

var errNilFileMeta = errors.New("osza/stat: nil FileMeta")

var (
	// ErrPathBuffer is returned when joinBuf cannot hold parent, separator, name, and NUL.
	ErrPathBuffer = errPathBuffer
	// ErrNilFileMeta is returned when dest is nil on *At/*Join entry points.
	ErrNilFileMeta = errNilFileMeta
)
