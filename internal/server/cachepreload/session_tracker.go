package cachepreload

import (
	"sync"
	"time"
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
	sessions sync.Map // map[sessionID]*sessionState
}

// OnFolderOpen is called when a user opens a folder.
// Returns the previous folderID (0 if none) for task cancellation.
func (s *SessionTracker) OnFolderOpen(sessionID string, folderID int64) (previousFolderID int64) {
	val, _ := s.sessions.LoadOrStore(sessionID, &sessionState{})
	state := val.(*sessionState)
	state.mu.Lock()
	defer state.mu.Unlock()
	previousFolderID = state.currentFolderID
	state.currentFolderID = folderID
	state.lastActivity = time.Now()
	return previousFolderID
}

// Cleanup removes stale session entries (call periodically or on session end).
func (s *SessionTracker) Cleanup(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
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
