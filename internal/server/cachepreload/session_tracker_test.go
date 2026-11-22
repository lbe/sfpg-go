package cachepreload

import (
	"testing"
	"testing/synctest"
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
	synctest.Test(t, func(t *testing.T) {
		st := &SessionTracker{}
		st.OnFolderOpen("sess1", 1)
		// Bubble time: the fake clock advances instantly when the root goroutine
		// is durably blocked, so 2ms elapses without any wall-clock waiting.
		time.Sleep(2 * time.Millisecond)
		st.Cleanup(1 * time.Millisecond)
		// sess1 should be removed; next open returns 0 for previous
		prev := st.OnFolderOpen("sess1", 2)
		if prev != 0 {
			t.Errorf("expected stale session to be removed, previous=%d", prev)
		}
	})
}

func TestSessionTracker_OnFolderOpen_SweepsStaleOncePerInterval(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	cur := t0
	st := &SessionTracker{now: func() time.Time { return cur }}
	hasSession := func(id string) bool {
		_, ok := st.sessions.Load(id)
		return ok
	}

	st.OnFolderOpen("a", 1) // first call sweeps (nothing stale yet)
	if got := st.lastCleanup.Load(); got != t0.UnixNano() {
		t.Fatalf("expected first call to record sweep time, got %d", got)
	}

	cur = t0.Add(26 * time.Minute)
	st.OnFolderOpen("b", 1) // interval elapsed: sweep, lastCleanup = t0+26m

	// "a" is now 30.5m old (stale), but the last sweep was only 4.5m ago.
	cur = t0.Add(30*time.Minute + 30*time.Second)
	st.OnFolderOpen("c", 1)
	if !hasSession("a") {
		t.Fatal("expected stale session 'a' to survive: sweep must not run before cleanupInterval")
	}

	// Interval has now elapsed since the last sweep: "a" is removed.
	cur = t0.Add(31 * time.Minute)
	st.OnFolderOpen("d", 1)
	if hasSession("a") {
		t.Error("expected stale session 'a' to be removed once cleanupInterval has passed")
	}
	if !hasSession("d") {
		t.Error("expected fresh session 'd' to remain after sweep")
	}
}
