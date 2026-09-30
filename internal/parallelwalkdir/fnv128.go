package parallelwalkdir

import (
	"fmt"
	"math/bits"
)

const (
	offset128Higher = 0x6c62272e07bb0142
	offset128Lower  = 0x62b821756295c58d
	prime128Lower   = 0x13b
	prime128Shift   = 24
)

// PathHash is a 128-bit FNV-1a digest of canonical path bytes, suitable as a
// comparable map key (for example map[PathHash]struct{}). Hi and Lo are the
// big-endian high and low 64-bit words respectively—the same layout as
// hash/fnv.New128a().Sum(nil).
type PathHash struct {
	Hi, Lo uint64
}

// HashPathBytes returns the FNV-1a 128-bit hash of path. The slice is read
// only for the duration of the call; callers must not mutate path concurrently.
func HashPathBytes(path []byte) PathHash {
	s := [2]uint64{offset128Higher, offset128Lower}
	for _, c := range path {
		s[1] ^= uint64(c)
		s0, s1 := bits.Mul64(prime128Lower, s[1])
		s0 += s[1]<<prime128Shift + prime128Lower*s[0]
		s[1] = s1
		s[0] = s0
	}
	return PathHash{Hi: s[0], Lo: s[1]}
}

// String returns a fixed-width hex encoding for logs and tests.
func (h PathHash) String() string {
	return fmt.Sprintf("%016x%016x", h.Hi, h.Lo)
}
