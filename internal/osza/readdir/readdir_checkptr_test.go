//go:build linux || darwin

package readdir_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza/readdir"
	"github.com/lbe/sfpg-go/internal/osza/stat"
)

// Integration canary after the Unix parser fix:
// internal/parallelwalkdir.TestParallelWalk_NoDeadlockWideSubdirSchedule

const (
	checkptrScratchBytes = 8192
	// Same caps as internal/parallelwalkdir/readdir_buffers.go
	checkptrInitialEntries   = 256
	checkptrInitialNameBytes = 64 << 10
)

// setupWideSubdirParent mirrors parallelwalkdir wide fixtures: one parent with
// many immediate subdirectories (names subdir%04d) each holding a small file.
func setupWideSubdirParent(t testing.TB, subdirs int) string {
	t.Helper()
	root := t.TempDir()
	for i := range subdirs {
		sub := filepath.Join(root, fmt.Sprintf("subdir%04d", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("jpg"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readDirWithCaps(root string, entryCap int) (int, error) {
	entries := make([]readdir.Entry, entryCap)
	nameBuf := make([]byte, checkptrInitialNameBytes)
	scratch := make([]byte, checkptrScratchBytes)
	joinBuf := make([]byte, checkptrInitialNameBytes)
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		return 0, err
	}
	return readdir.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
}

// TestReadDirWideSubdirParent reads a 512-child directory using production-sized
// buffers. Under -race, the unsafe *syscall.Dirent cast on misaligned record
// offsets in scratch triggers runtime.checkptr (see tmp/osza-handover-race.md).
func TestReadDirWideSubdirParent(t *testing.T) {
	const subdirs = 512
	root := setupWideSubdirParent(t, subdirs)

	std, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(std) != subdirs {
		t.Fatalf("os.ReadDir children=%d want %d", len(std), subdirs)
	}

	n, err := readDirWithCaps(root, subdirs+16)
	if err != nil {
		t.Fatal(err)
	}
	if n != subdirs {
		t.Fatalf("n=%d want %d", n, subdirs)
	}
}

// TestReadDirConcurrentWideSubdirParent reproduces parallelwalkdir's concurrent
// ReadDir stress (many workers, wide parents, reused scratch).
func TestReadDirConcurrentWideSubdirParent(t *testing.T) {
	runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(0)

	const wideParents = 4
	const subdirsPerParent = 512
	roots := make([]string, wideParents)
	base := t.TempDir()
	for p := range wideParents {
		parent := filepath.Join(base, fmt.Sprintf("wide%02d", p))
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range subdirsPerParent {
			sub := filepath.Join(parent, fmt.Sprintf("subdir%04d", i))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("jpg"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		roots[p] = parent
	}

	const workers = 24
	const rounds = 40

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		root := roots[w%len(roots)]
		go func() {
			defer wg.Done()
			entries := make([]readdir.Entry, subdirsPerParent+32)
			nameBuf := make([]byte, checkptrInitialNameBytes)
			scratch := make([]byte, checkptrScratchBytes)
			joinBuf := make([]byte, checkptrInitialNameBytes)
			for range rounds {
				dirPath := make([]byte, len(root)+1)
				dirLen, derr := readdir.CopyDirPath(dirPath, root)
				if derr != nil {
					t.Error(derr)
					return
				}
				n, err := readdir.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
				if err != nil {
					t.Error(err)
					return
				}
				if n != subdirsPerParent {
					t.Errorf("n=%d want %d", n, subdirsPerParent)
					return
				}
			}
		}()
	}
	wg.Wait()
}

type readDirBufs struct {
	entries []readdir.Entry
	dirPath []byte
	dirLen  int
	nameBuf []byte
	scratch []byte
	joinBuf []byte
}

func newReadDirBufs(root string) *readDirBufs {
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		panic(err)
	}
	b := &readDirBufs{
		entries: make([]readdir.Entry, checkptrInitialEntries),
		dirPath: dirPath,
		dirLen:  dirLen,
		nameBuf: make([]byte, checkptrInitialNameBytes),
		scratch: make([]byte, checkptrScratchBytes),
		joinBuf: make([]byte, checkptrInitialNameBytes),
	}
	return b
}

// readDirAll mirrors parallelwalkdir/readdir_buffers.readDirAll (grow-and-retry).
func readDirAll(root string, b *readDirBufs) (int, error) {
	for {
		n, err := readdir.ReadDir(b.dirPath, b.dirLen, b.entries, b.nameBuf, b.scratch, b.joinBuf)
		if err == nil {
			return n, nil
		}
		if errors.Is(err, readdir.ErrOverflow) {
			b.entries = make([]readdir.Entry, len(b.entries)*2)
			b.nameBuf = make([]byte, len(b.nameBuf)*2)
			continue
		}
		if errors.Is(err, stat.ErrPathBuffer) {
			b.joinBuf = make([]byte, len(b.joinBuf)*2)
			continue
		}
		return n, err
	}
}

// TestReadDirAllConcurrentGrowRetry matches production discovery: workers start
// with 256-entry caps on 512-wide parents and grow until the listing completes.
func TestReadDirAllConcurrentGrowRetry(t *testing.T) {
	runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(0)

	const wideParents = 4
	const subdirsPerParent = 512
	roots := make([]string, wideParents)
	base := t.TempDir()
	for p := range wideParents {
		parent := filepath.Join(base, fmt.Sprintf("wide%02d", p))
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range subdirsPerParent {
			sub := filepath.Join(parent, fmt.Sprintf("subdir%04d", i))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("jpg"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		roots[p] = parent
	}

	const workers = 24
	const rounds = 30

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		root := roots[w%len(roots)]
		go func() {
			defer wg.Done()
			b := newReadDirBufs(root)
			for range rounds {
				n, err := readDirAll(root, b)
				if err != nil {
					t.Error(err)
					return
				}
				if n != subdirsPerParent {
					t.Errorf("n=%d want %d", n, subdirsPerParent)
					return
				}
			}
		}()
	}
	wg.Wait()
}
