package cachepreload

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	// staleSessionAge is how long a session may be idle before OnFolderOpen's sweep removes it.
	staleSessionAge = 30 * time.Minute
	// cleanupInterval is the minimum time between sweeps triggered by OnFolderOpen.
	cleanupInterval = 5 * time.Minute
)

// sessionState holds per-session preload state.
type sessionState struct {
	currentFolderID int64
	lastActivity    time.Time
	mu              sync.Mutex
}

// SessionTracker manages per-session preload state for folder navigation cancellation.
// When a user navigates to a new folder, outstanding tasks for the previous folder
// are cancelled to avoid wasting resources.
type SessionTracker struct {
	sessions    sync.Map         // map[sessionID]*sessionState
	lastCleanup atomic.Int64     // UnixNano of the last sweep; 0 if never swept
	now         func() time.Time // nil uses time.Now; tests inject a fake clock
}

func (s *SessionTracker) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// OnFolderOpen is called when a user opens a folder.
// Returns the previous folderID (0 if none) for task cancellation.
// It also sweeps stale sessions, at most once per cleanupInterval.
func (s *SessionTracker) OnFolderOpen(sessionID string, folderID int64) (previousFolderID int64) {
	now := s.currentTime()
	nowNano := now.UnixNano()
	if last := s.lastCleanup.Load(); nowNano-last >= int64(cleanupInterval) && s.lastCleanup.CompareAndSwap(last, nowNano) {
		s.Cleanup(staleSessionAge)
	}

	val, _ := s.sessions.LoadOrStore(sessionID, &sessionState{})
	state := val.(*sessionState)
	state.mu.Lock()
	defer state.mu.Unlock()
	previousFolderID = state.currentFolderID
	state.currentFolderID = folderID
	state.lastActivity = now
	return previousFolderID
}

// Cleanup removes session entries idle for longer than maxAge.
// OnFolderOpen calls it at most every cleanupInterval.
func (s *SessionTracker) Cleanup(maxAge time.Duration) {
	cutoff := s.currentTime().Add(-maxAge)
	s.sessions.Range(func(key, value any) bool {
		state := value.(*sessionState)
		state.mu.Lock()
		stale := state.lastActivity.Before(cutoff)
		state.mu.Unlock()
		if stale {
			s.sessions.Delete(key)
		}
		return true
	})
}
