package parallelwalkdir

import "sync"

// WalkPathStore bump-allocates reported path bytes for one or more walks on the
// same store. ReportedFile.Path values are subslices of the arena until Reset
// runs and nothing still references bytes from the prior bump generation.
type WalkPathStore struct {
	mu    sync.Mutex
	arena []byte
	used  int
}

// NewWalkPathStore returns an empty path arena.
func NewWalkPathStore() *WalkPathStore {
	return &WalkPathStore{}
}

// Reset clears the bump offset; retained capacity is reused on the next walk.
func (s *WalkPathStore) Reset() {
	s.mu.Lock()
	s.used = 0
	s.mu.Unlock()
}

// AppendPath copies src[:n] into the arena and returns a subslice valid until
// Reset or the store is discarded.
func (s *WalkPathStore) AppendPath(src []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	need := s.used + n
	if cap(s.arena) < need {
		newCap := cap(s.arena)
		if newCap == 0 {
			newCap = n
		}
		for newCap < need {
			newCap *= 2
		}
		next := make([]byte, newCap)
		copy(next, s.arena[:s.used])
		s.arena = next
	}
	copy(s.arena[s.used:s.used+n], src[:n])
	p := s.arena[s.used : s.used+n]
	s.used += n
	return p
}
