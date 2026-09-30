//go:build linux || darwin

package stat

import "syscall"

func platformLstat(pathBuf []byte, st *syscall.Stat_t) error {
	return platformFstatat(pathBuf, st, atSymlinkNoFollow)
}

func platformStat(pathBuf []byte, st *syscall.Stat_t) error {
	return platformFstatat(pathBuf, st, 0)
}

// LstatAt lstats pathBuf[:pathLen] into dest without allocating on the success path.
// pathBuf must have capacity at pathLen for a temporary NUL byte used by the syscall.
func LstatAt(pathBuf []byte, pathLen int, dest *FileMeta) error {
	if dest == nil {
		return ErrNilFileMeta
	}
	if pathLen < 0 || pathLen >= len(pathBuf) {
		return pathError("Lstat", pathBuf, pathLen, syscall.EINVAL)
	}

	saved := pathBuf[pathLen]
	pathBuf[pathLen] = 0
	defer func() { pathBuf[pathLen] = saved }()

	var st syscall.Stat_t
	if err := platformLstat(pathBuf, &st); err != nil {
		return pathError("Lstat", pathBuf, pathLen, err)
	}
	applyStatToFileMeta(dest, &st)
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
