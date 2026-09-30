package stat

// joinBuf layout for LstatJoin and StatJoin
//
// Callers pack parent and name into one reusable buffer before *Join. Capacity must be at
// least parentLen + 1 + nameLen + 1 so joinPacked can insert '/' and a trailing NUL for
// the platform stat syscall (same NUL-at-pathLen convention as *At).
//
// Before LstatJoin/StatJoin (caller setup):
//
//	joinBuf[0:parentLen]                    — parent path bytes (UTF-8, no trailing '/')
//	joinBuf[parentLen:parentLen+nameLen]    — final path component bytes (no leading '/')
//	len(joinBuf) >= parentLen + 1 + nameLen + 1
//
// joinPacked (used by *Join) reshapes joinBuf in place without heap strings:
//
//	joinBuf[0:parentLen]                    — parent (unchanged)
//	joinBuf[parentLen]                      — '/' separator (inserted)
//	joinBuf[parentLen+1:pathLen]            — name (moved right by one byte when nameLen > 0)
//	joinBuf[pathLen]                        — NUL (0) for syscall
//	pathLen = parentLen + 1 + nameLen       — logical path length excluding NUL
//
// After a successful *Join, joinBuf[:pathLen] is the joined path; the name bytes no longer
// occupy joinBuf[parentLen:parentLen+nameLen]. Re-pack parent+name before the next *Join.
//
// Errors: joinPacked returns ErrPathBuffer when lengths are negative or capacity is
// insufficient. *Join returns ErrNilFileMeta when dest is nil; syscall failures become
// *fs.PathError with Op Lstat or Stat and Path set from joinBuf[:pathLen] on the error path.
//
// joinPacked inserts a path separator between parent and name segments packed in joinBuf
// and NUL-terminates the path for syscalls.
func joinPacked(joinBuf []byte, parentLen, nameLen int) (pathLen int, err error) {
	if parentLen < 0 || nameLen < 0 {
		return 0, ErrPathBuffer
	}
	if parentLen+nameLen > len(joinBuf) {
		return 0, ErrPathBuffer
	}
	pathLen = parentLen + 1 + nameLen
	if pathLen >= len(joinBuf) {
		return 0, ErrPathBuffer
	}
	if nameLen > 0 {
		copy(joinBuf[parentLen+1:pathLen], joinBuf[parentLen:parentLen+nameLen])
	}
	joinBuf[parentLen] = '/'
	joinBuf[pathLen] = 0
	return pathLen, nil
}
