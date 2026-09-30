//go:build windows

package stat

import "syscall"

func lstatFollowSurrogates(pathBuf []byte, pathLen int) bool {
	if pathLen == 0 {
		return false
	}
	last := pathBuf[pathLen-1]
	return last == '/' || last == '\\'
}

// LstatAt lstats pathBuf[:pathLen] into dest without allocating on the success path.
// pathBuf must have capacity at pathLen for a temporary NUL byte used by joinPacked parity with Unix.
func LstatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return pathError("Lstat", pathBuf, pathLen, syscall.EINVAL)
	}

	follow := lstatFollowSurrogates(pathBuf, pathLen)
	if err := platformStat(pathBuf, pathLen, dest, follow); err != nil {
		return pathError("Lstat", pathBuf, pathLen, err)
	}
	return nil
}

// LstatJoin joins parent and name in joinBuf via joinPacked, then lstats the result into dest.
func LstatJoin(joinBuf []byte, parentLen, nameLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	pathLen, err := joinPacked(joinBuf, parentLen, nameLen)
	if err != nil {
		return err
	}
	return LstatAt(joinBuf, pathLen, dest)
}
