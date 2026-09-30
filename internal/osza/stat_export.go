package osza

import "github.com/lbe/sfpg-go/internal/osza/stat"

// FileMeta holds metadata from LstatAt/StatAt/LstatJoin without a heap path
// string on the hot path. It implements fs.FileInfo; Name always returns "" (paths live
// in caller pathBuf/joinBuf).
type FileMeta = stat.FileMeta

// ErrPathBuffer is returned when joinBuf cannot hold parent, separator, name, and NUL.
var ErrPathBuffer = stat.ErrPathBuffer

// ErrNilFileMeta is returned when dest is nil on *At/*Join entry points.
var ErrNilFileMeta = stat.ErrNilFileMeta

// LstatAt lstats pathBuf[:pathLen] into dest. pathBuf must have capacity at pathLen for
// a temporary NUL byte used by the platform syscall on Unix.
func LstatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	return stat.LstatAt(pathBuf, pathLen, dest)
}

// StatAt stats pathBuf[:pathLen] into dest (follows symlinks). Same pathBuf contract as LstatAt.
func StatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	return stat.StatAt(pathBuf, pathLen, dest)
}

// LstatJoin joins parent and name in joinBuf in place, then lstats into dest.
func LstatJoin(joinBuf []byte, parentLen, nameLen int, dest *FileMeta) error {
	return stat.LstatJoin(joinBuf, parentLen, nameLen, dest)
}
