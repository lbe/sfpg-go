//go:build windows

package stat

import "syscall"

// StatAt stats pathBuf[:pathLen] (following symlinks) into dest without allocating on the success path.
func StatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return pathError("Stat", pathBuf, pathLen, syscall.EINVAL)
	}

	if err := platformStat(pathBuf, pathLen, dest, true); err != nil {
		return pathError("Stat", pathBuf, pathLen, err)
	}
	return nil
}

// StatJoin joins parent and name in joinBuf via joinPacked, then stats the result into dest.
func StatJoin(joinBuf []byte, parentLen, nameLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	pathLen, err := joinPacked(joinBuf, parentLen, nameLen)
	if err != nil {
		return err
	}
	return StatAt(joinBuf, pathLen, dest)
}
