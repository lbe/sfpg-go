package server

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/files"
)

func discoveryPathWorkEqual(a, b files.DiscoveryPathWork) bool {
	return a.MtimeUnix == b.MtimeUnix &&
		a.SizeBytes == b.SizeBytes &&
		bytes.Equal(a.Path, b.Path)
}

// newTestDiscoveryDQue opens a fresh discovery dque adapter in a dedicated
// per-test temp directory and registers its Close for cleanup.
func newTestDiscoveryDQue(t *testing.T) queue.Queuer[files.DiscoveryPathWork] {
	t.Helper()
	q, err := newDiscoveryDQueAdapter(filepath.Join(t.TempDir(), "discovery-dque"))
	if err != nil {
		t.Fatalf("newDiscoveryDQueAdapter: %v", err)
	}
	t.Cleanup(q.Close)
	return q
}

func TestDiscoveryDQueAdapter_RoundTripFIFO(t *testing.T) {
	q := newTestDiscoveryDQue(t)

	items := []files.DiscoveryPathWork{
		{Path: []byte("/gallery/alpha.jpg"), MtimeUnix: 100, SizeBytes: 10},
		{Path: []byte("/gallery/nested/beta.png"), MtimeUnix: 200, SizeBytes: 20},
		{Path: []byte("/gallery/nested/deeper/gamma.gif"), MtimeUnix: 300, SizeBytes: 30},
	}
	for _, item := range items {
		if err := q.Enqueue(item); err != nil {
			t.Fatalf("Enqueue(%q): %v", string(item.Path), err)
		}
	}

	for i, want := range items {
		got, err := q.Dequeue()
		if err != nil {
			t.Fatalf("Dequeue #%d: %v", i, err)
		}
		if !discoveryPathWorkEqual(got, want) {
			t.Errorf("Dequeue #%d = %+v, want %+v", i, got, want)
		}
	}

	if !q.IsEmpty() {
		t.Errorf("IsEmpty() = false after draining, want true")
	}
}

func TestDiscoveryDQueAdapter_EmptyDequeue(t *testing.T) {
	q := newTestDiscoveryDQue(t)

	if _, err := q.Dequeue(); !errors.Is(err, queue.ErrEmptyQueue) {
		t.Fatalf("Dequeue on empty queue: err = %v, want %v", err, queue.ErrEmptyQueue)
	}
}

func TestDiscoveryDQueAdapter_AfterClose(t *testing.T) {
	q := newTestDiscoveryDQue(t)
	q.Close()

	if err := q.Enqueue(files.DiscoveryPathWork{Path: []byte("x")}); !errors.Is(err, queue.ErrClosedQueue) {
		t.Errorf("Enqueue after Close: err = %v, want %v", err, queue.ErrClosedQueue)
	}
	if _, err := q.Dequeue(); !errors.Is(err, queue.ErrClosedQueue) {
		t.Errorf("Dequeue after Close: err = %v, want %v", err, queue.ErrClosedQueue)
	}
}

func TestDiscoveryDQueAdapter_CloseIdempotent(t *testing.T) {
	q := newTestDiscoveryDQue(t)

	q.Close()
	q.Close()
	q.Close()
}

func TestDiscoveryDQueAdapter_LenTracksSize(t *testing.T) {
	q := newTestDiscoveryDQue(t)

	if got := q.Len(); got != 0 {
		t.Fatalf("Len() = %d on fresh queue, want 0", got)
	}
	if !q.IsEmpty() {
		t.Fatalf("IsEmpty() = false on fresh queue, want true")
	}

	item := files.DiscoveryPathWork{Path: []byte("item"), MtimeUnix: 1, SizeBytes: 2}
	for i := 1; i <= 3; i++ {
		if err := q.Enqueue(item); err != nil {
			t.Fatalf("Enqueue #%d: %v", i, err)
		}
		if got := q.Len(); got != i {
			t.Errorf("Len() after %d enqueues = %d, want %d", i, got, i)
		}
	}

	for i := 2; i >= 0; i-- {
		if _, err := q.Dequeue(); err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if got := q.Len(); got != i {
			t.Errorf("Len() after draining to %d remaining = %d, want %d", i, got, i)
		}
	}
}

func TestMapDiscoveryDequeueResult(t *testing.T) {
	cleanupErr := errors.New("segment cleanup failed")
	unrelatedErr := errors.New("remove failed")

	tests := []struct {
		name    string
		ptr     *files.DiscoveryPathWork
		err     error
		wantVal files.DiscoveryPathWork
		wantErr error
	}{
		{
			name:    "empty maps to ErrEmptyQueue",
			err:     dque.ErrEmpty,
			wantErr: queue.ErrEmptyQueue,
		},
		{
			name:    "closed maps to ErrClosedQueue",
			err:     dque.ErrQueueClosed,
			wantErr: queue.ErrClosedQueue,
		},
		{
			name:    "nil ptr with unrelated error passes through",
			err:     unrelatedErr,
			wantErr: unrelatedErr,
		},
		{
			name:    "present item with nil err returns item",
			ptr:     &files.DiscoveryPathWork{Path: []byte("/a.jpg"), MtimeUnix: 1, SizeBytes: 2},
			wantVal: files.DiscoveryPathWork{Path: []byte("/a.jpg"), MtimeUnix: 1, SizeBytes: 2},
		},
		{
			name:    "present item with cleanup err keeps item (no drop)",
			ptr:     &files.DiscoveryPathWork{Path: []byte("/b.png"), MtimeUnix: 3, SizeBytes: 4},
			err:     cleanupErr,
			wantVal: files.DiscoveryPathWork{Path: []byte("/b.png"), MtimeUnix: 3, SizeBytes: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := mapDiscoveryDequeueResult(tt.ptr, tt.err)
			if !discoveryPathWorkEqual(val, tt.wantVal) {
				t.Errorf("val = %+v, want %+v", val, tt.wantVal)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
