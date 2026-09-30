//go:build linux || darwin

package stat

import (
	"syscall"
)

// StatAt stats pathBuf[:pathLen] (following symlinks) into dest without allocating on the success path.
// pathBuf must have capacity at pathLen for a temporary NUL byte used by the syscall.
func StatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return pathError("Stat", pathBuf, pathLen, syscall.EINVAL)
	}

	saved := pathBuf[pathLen]
	pathBuf[pathLen] = 0
	defer func() { pathBuf[pathLen] = saved }()

	var st syscall.Stat_t
	if err := platformStat(pathBuf, &st); err != nil {
		return pathError("Stat", pathBuf, pathLen, err)
	}
	applyStatToFileMeta(dest, &st)
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
