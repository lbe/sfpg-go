package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
)

// fakeWriter is the Service's Writer: it keeps what was submitted and holds
// the drain open while pending is above zero.
type fakeWriter struct {
	pending    atomic.Int64
	rebuildErr error // returned by RebuildFolderIndex
	rebuilt    bool  // RebuildFolderIndex ran; read after Run returns
	submitErr  error // returned by SubmitFile

	mu        sync.Mutex
	submitted []string // Path of each submitted file
	invalid   []files.InvalidFile
}

func (w *fakeWriter) SubmitFile(f *files.File) error {
	if w.submitErr != nil {
		return w.submitErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.submitted = append(w.submitted, f.Path)
	return nil
}

func (w *fakeWriter) SubmitInvalidFile(f files.InvalidFile) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalid = append(w.invalid, f)
	return nil
}

func (w *fakeWriter) PendingCount() int64 { return w.pending.Load() }

func (w *fakeWriter) RebuildFolderIndex(context.Context) error {
	w.rebuilt = true
	return w.rebuildErr
}

// submittedPaths returns the paths submitted so far.
func (w *fakeWriter) submittedPaths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.submitted...)
}

// invalidFiles returns the invalid records submitted so far.
func (w *fakeWriter) invalidFiles() []files.InvalidFile {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]files.InvalidFile(nil), w.invalid...)
}

// fakeProcessor is the Service's FileProcessor, with the processing supplied
// by each test.
type fakeProcessor struct {
	process func(ctx context.Context, path string) (*files.File, error)
}

func (p *fakeProcessor) ProcessDiscoveryFile(ctx context.Context, path string, _, _ int64) (*files.File, error) {
	return p.process(ctx, path)
}

func (p *fakeProcessor) Close() error { return nil }

// removePrefix is the Config.RemovePrefix of the tests.
func removePrefix(normalizedDir, path string) (string, error) {
	if path == normalizedDir {
		return "", nil
	}
	prefix := normalizedDir + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("path %q is not under %q", path, normalizedDir)
	}
	return path[len(prefix):], nil
}

// testDatabase is a migrated database with the parts discovery reads and
// writes. Its pools and writer close when the test ends.
type testDatabase struct {
	state   *modulestate.Service
	catalog parallelwalkdir.GetDirEntMapFunc
	writer  *database.Writer
}

func openTestDatabase(t *testing.T, normalizedImagesDir string) testDatabase {
	t.Helper()
	paths, rw, ro, err := database.Setup(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("database.Setup: %v", err)
	}
	t.Cleanup(func() {
		if err = rw.Close(); err != nil {
			t.Errorf("close rw pool: %v", err)
		}
		if err = ro.Close(); err != nil {
			t.Errorf("close ro pool: %v", err)
		}
	})
	writer, err := database.NewWriter(t.Context(), database.WriterConfig{Paths: paths, RW: rw, RO: ro})
	if err != nil {
		t.Fatalf("database.NewWriter: %v", err)
	}
	// Registered after the pools, so the writer closes first.
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close writer: %v", err)
		}
	})
	return testDatabase{
		state:   modulestate.NewService(rw),
		catalog: database.NewDiscoveryDirEntMapFunc(ro, normalizedImagesDir, removePrefix),
		writer:  writer,
	}
}

// newTestService returns a Service over a fresh backlog, with the given
// dependencies filled in where the test does not supply them.
func newTestService(t *testing.T, cfg Config, deps Deps) *Service {
	t.Helper()
	if cfg.BacklogDir == "" {
		cfg.BacklogDir = filepath.Join(t.TempDir(), "discovery-dque")
	}
	if cfg.RemovePrefix == nil {
		cfg.RemovePrefix = removePrefix
	}
	if deps.Catalog == nil {
		deps.Catalog = emptyCatalog
	}
	if deps.Stats == nil {
		deps.Stats = &Stats{}
	}
	svc, err := New(cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		svc.Wait()
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return svc
}

// writeFile writes data to rel under dir, creating parent directories, and
// sets its mtime to mtime. It returns the file's info.
func writeFile(t *testing.T, dir, rel string, data []byte, mtime int64) fs.FileInfo { //nolint:unparam // integration tests pass other mtimes
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	when := time.Unix(mtime, 0)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	return info
}

// queueContents drains the backlog and returns the paths in queue order.
func queueContents(t *testing.T, b *backlog) []string {
	t.Helper()
	var got []string
	for {
		w, err := b.dequeue()
		if errors.Is(err, queue.ErrEmptyQueue) {
			return got
		}
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		got = append(got, string(w.Path))
	}
}
