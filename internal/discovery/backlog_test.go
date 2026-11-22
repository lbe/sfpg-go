package discovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/queue"
)

func TestBacklogRoundTripIsFIFO(t *testing.T) {
	b, err := openBacklog(filepath.Join(t.TempDir(), "discovery-dque"))
	if err != nil {
		t.Fatalf("openBacklog: %v", err)
	}
	t.Cleanup(func() { _ = b.close() })

	items := []PathWork{
		{Path: []byte("/gallery/alpha.jpg"), MtimeUnix: 100, SizeBytes: 10},
		{Path: []byte("/gallery/nested/beta.png"), MtimeUnix: 200, SizeBytes: 20},
		{Path: []byte("/gallery/nested/deeper/gamma.gif"), MtimeUnix: 300, SizeBytes: 30},
	}
	for _, w := range items {
		if err := b.enqueue(w); err != nil {
			t.Fatalf("enqueue(%q): %v", w.Path, err)
		}
	}
	if got := b.len(); got != len(items) {
		t.Errorf("len() = %d, want %d", got, len(items))
	}
	for i, want := range items {
		got, err := b.dequeue()
		if err != nil {
			t.Fatalf("dequeue #%d: %v", i, err)
		}
		if !bytes.Equal(got.Path, want.Path) || got.MtimeUnix != want.MtimeUnix || got.SizeBytes != want.SizeBytes {
			t.Errorf("dequeue #%d = %q mtime %d size %d, want %q mtime %d size %d",
				i, got.Path, got.MtimeUnix, got.SizeBytes, want.Path, want.MtimeUnix, want.SizeBytes)
		}
	}
	if _, err := b.dequeue(); !errors.Is(err, queue.ErrEmptyQueue) {
		t.Errorf("dequeue on drained backlog: err = %v, want %v", err, queue.ErrEmptyQueue)
	}
}

func TestBacklogClosedReturnsClosedErrors(t *testing.T) {
	b, err := openBacklog(filepath.Join(t.TempDir(), "discovery-dque"))
	if err != nil {
		t.Fatalf("openBacklog: %v", err)
	}
	if err := b.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := b.enqueue(PathWork{Path: []byte("x")}); !errors.Is(err, queue.ErrClosedQueue) {
		t.Errorf("enqueue after close: err = %v, want %v", err, queue.ErrClosedQueue)
	}
	if _, err := b.dequeue(); !errors.Is(err, queue.ErrClosedQueue) {
		t.Errorf("dequeue after close: err = %v, want %v", err, queue.ErrClosedQueue)
	}
}

func TestNewWipesExistingBacklog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discovery-dque")
	old, err := openBacklog(dir)
	if err != nil {
		t.Fatalf("openBacklog: %v", err)
	}
	if err := old.enqueue(PathWork{Path: []byte("/gallery/left-over.jpg"), MtimeUnix: 1, SizeBytes: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := old.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	stray := filepath.Join(dir, "stray")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	s := newTestService(t, Config{BacklogDir: dir}, Deps{})
	if got := s.QueueLen(); got != 0 {
		t.Errorf("QueueLen() after New = %d, want 0", got)
	}
	if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stray file after New: stat err = %v, want not exist", err)
	}
}

// The fixture is one PathWork in the format dev wrote before the database
// writer: big-endian mtime, size, path length, then the path. It holds mtime
// 1700000100, size 4882816 and path "2010/vacation/IMG_001.jpg".
func TestPathWorkGobMatchesFixture(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "pre-db02-discovery.gob"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(fixture) != 45 {
		t.Fatalf("fixture is %d bytes, want 45", len(fixture))
	}

	var w PathWork
	if err = w.GobDecode(fixture); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}
	if want := "2010/vacation/IMG_001.jpg"; string(w.Path) != want {
		t.Errorf("Path = %q, want %q", w.Path, want)
	}
	if w.MtimeUnix != 1700000100 {
		t.Errorf("MtimeUnix = %d, want 1700000100", w.MtimeUnix)
	}
	if w.SizeBytes != 4882816 {
		t.Errorf("SizeBytes = %d, want 4882816", w.SizeBytes)
	}

	encoded, err := w.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}
	if !bytes.Equal(encoded, fixture) {
		t.Errorf("re-encoded = % x, want fixture % x", encoded, fixture)
	}
}

func TestPathWorkGobDecodeRejectsBadPayloads(t *testing.T) {
	valid, err := PathWork{Path: []byte("a.jpg"), MtimeUnix: 1, SizeBytes: 2}.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "shorter than the header", data: valid[:19]},
		{name: "path cut short", data: valid[:len(valid)-1]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w PathWork
			if err := w.GobDecode(tt.data); err == nil {
				t.Errorf("GobDecode(%d bytes) = nil, want error", len(tt.data))
			}
		})
	}
}
