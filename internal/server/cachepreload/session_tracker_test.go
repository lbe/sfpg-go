package cachepreload

import (
	"testing"
	"time"
)

func TestSessionTracker_OnFolderOpen_ReturnsPreviousFolder(t *testing.T) {
	st := &SessionTracker{}
	prev := st.OnFolderOpen("sess1", 23)
	if prev != 0 {
		t.Errorf("expected previous 0 on first open, got %d", prev)
	}
	prev = st.OnFolderOpen("sess1", 42)
	if prev != 23 {
		t.Errorf("expected previous 23, got %d", prev)
	}
}

func TestSessionTracker_OnFolderOpen_IsolatedBySession(t *testing.T) {
	st := &SessionTracker{}
	st.OnFolderOpen("sess1", 10)
	st.OnFolderOpen("sess2", 20)
	prev := st.OnFolderOpen("sess1", 11)
	if prev != 10 {
		t.Errorf("expected previous 10 for sess1, got %d", prev)
	}
}

func TestSessionTracker_Cleanup_RemovesStaleSessions(t *testing.T) {
	st := &SessionTracker{}
	st.OnFolderOpen("sess1", 1)
	time.Sleep(2 * time.Millisecond) // ensure lastActivity is old
	st.Cleanup(1 * time.Millisecond)
	// sess1 should be removed; next open returns 0 for previous
	prev := st.OnFolderOpen("sess1", 2)
	if prev != 0 {
		t.Errorf("expected stale session to be removed, previous=%d", prev)
	}
}
