// Package discovery finds new and changed images and imports them.
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// moduleName is discovery's row in module_state.
const moduleName = "discovery"

// drainPoll is how often a run checks whether its files are processed and
// written: the backlog and the writer have nothing to wait on.
const drainPoll = 100 * time.Millisecond

// Service finds new and changed images and imports them. A run walks the
// images directory, queues each file that is new or changed since the last
// import on a disk backlog, and returns when the workers have processed the
// queue and the writes are committed; it then saves the run's counters and
// rebuilds the folder index. Workers run for the life of the Service.
type Service struct {
	cfg     Config
	deps    Deps
	backlog *backlog
	senders atomic.Int64   // walks queueing files
	claimMu sync.Mutex     // a worker taking a file and counting it in flight; see claim
	workers sync.WaitGroup // the worker pool

	mu      sync.Mutex
	run     chan struct{} // closed when the current run ends; nil when none runs
	stopped bool          // Wait was called: no new runs
}

// Config is how discovery runs.
type Config struct {
	ImagesDir           string // the directory walked
	NormalizedImagesDir string // ImagesDir with forward slashes
	BacklogDir          string // <main db dir>/discovery-dque; wiped by New
	RemovePrefix        func(normalizedDir, path string) (string, error)
}

// Deps are the parts of the server discovery uses.
type Deps struct {
	Processor files.FileProcessor
	Writer    Writer
	Catalog   parallelwalkdir.GetDirEntMapFunc // what the database has per directory
	State     *modulestate.Service             // the "discovery" row: active flag, saved counters
	Pool      *workerpool.Pool
	Stats     *Stats

	// Optional callbacks.
	OnRunning      func(delta int)  // a run starts (+1) or ends (-1)
	OnFileInserted func(size int64) // a file was submitted for writing
}

// Writer is what discovery needs from the database writer.
// *database.Writer implements it.
type Writer interface {
	SubmitFile(f *files.File) error
	SubmitInvalidFile(f files.InvalidFile) error
	PendingCount() int64
	RebuildFolderIndex(ctx context.Context) error
}

// New opens a new, empty backlog in cfg.BacklogDir.
func New(cfg Config, deps Deps) (*Service, error) {
	b, err := openBacklog(cfg.BacklogDir)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, deps: deps, backlog: b}, nil
}

// Start starts the workers. They stop when the pool's context is canceled.
func (s *Service) Start() {
	s.workers.Go(func() { s.deps.Pool.StartWorkerPool(s.work, s.backlog.len) })
}

// Run runs discovery once and returns when it is done. A run already in
// progress makes it log "discovery already in flight" and return nil.
func (s *Service) Run(ctx context.Context) error {
	if !s.begin() {
		return nil
	}
	defer s.end()

	stats := s.deps.Stats
	stats.reset()
	if s.deps.OnRunning != nil {
		s.deps.OnRunning(1)
		defer s.deps.OnRunning(-1)
	}
	if err := s.deps.State.SetActive(ctx, moduleName, true); err != nil {
		slog.Error("failed to set discovery active in module_state", "err", err)
	}
	defer func() {
		// The run's context may be canceled; the row must still say inactive.
		if err := s.deps.State.SetActive(context.Background(), moduleName, false); err != nil {
			slog.Error("failed to set discovery inactive in module_state", "err", err)
		}
	}()

	if s.senders.Load() != 0 || s.backlog.len() != 0 {
		return errors.New("discovery walk refused: queue backlog or active senders must be empty before walk")
	}
	store := parallelwalkdir.NewWalkPathStore()
	s.walk(ctx, store)
	if err := s.waitDrained(ctx); err != nil {
		slog.Error("discovery drain cancelled during shutdown", "err", err)
		return err
	}
	store.Reset()

	slog.Info("File processing completed",
		"found", stats.TotalFound.Load(),
		"existing", stats.AlreadyExisting.Load(),
		"inserted", stats.NewlyInserted.Load(),
		"skipped_invalid", stats.SkippedInvalid.Load())
	if err := s.deps.State.SaveFileProcessing(ctx, moduleName, stats.GetStats()); err != nil {
		slog.Error("failed to persist file processing stats", "err", err)
	}
	if err := s.deps.Writer.RebuildFolderIndex(ctx); err != nil {
		slog.Error("failed to rebuild file_folder_index", "err", err)
		return err
	}
	return nil
}

// begin admits a run unless one is in progress or the Service is stopping.
func (s *Service) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	if s.run != nil {
		slog.Info("discovery already in flight")
		return false
	}
	s.run = make(chan struct{})
	return true
}

func (s *Service) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.run)
	s.run = nil
}

// waitDrained waits until no walk is queueing, the backlog is empty, no file
// is in flight and the writer has nothing pending.
func (s *Service) waitDrained(ctx context.Context) error {
	ticker := time.NewTicker(drainPoll)
	defer ticker.Stop()
	for !s.drained() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	// A canceled walk can leave nothing queued; that is not a finished run.
	return ctx.Err()
}

func (s *Service) drained() bool {
	if s.senders.Load() != 0 {
		return false
	}
	s.claimMu.Lock()
	workersDone := s.backlog.len() == 0 && s.deps.Stats.InFlight.Load() == 0
	s.claimMu.Unlock()
	return workersDone && s.deps.Writer.PendingCount() == 0
}

// Idle reports whether discovery has nothing to do: no run, nothing queued or
// in flight, and nothing waiting to be written.
func (s *Service) Idle() bool {
	s.mu.Lock()
	running := s.run != nil
	s.mu.Unlock()
	return !running && s.drained()
}

// QueueLen returns the number of files waiting for a worker.
func (s *Service) QueueLen() int { return s.backlog.len() }

// Hydrate loads the last run's saved counters into Stats. Startup calls it
// when it skips discovery, so the dashboard shows the last run.
func (s *Service) Hydrate(ctx context.Context) {
	fp, err := s.deps.State.LoadFileProcessing(ctx, moduleName)
	if err != nil {
		slog.Error("failed to hydrate file processing stats", "err", err)
		return
	}
	stats := s.deps.Stats
	stats.TotalFound.Store(fp.TotalFound)
	stats.AlreadyExisting.Store(fp.AlreadyExisting)
	stats.NewlyInserted.Store(fp.NewlyInserted)
	stats.SkippedInvalid.Store(fp.SkippedInvalid)
}

// Wait waits for the workers to stop and for a run in progress to end, and
// admits no new run. Cancel the pool's context and the run's first.
func (s *Service) Wait() {
	s.workers.Wait()
	s.mu.Lock()
	s.stopped = true
	run := s.run
	s.mu.Unlock()
	if run != nil {
		<-run
	}
}

// Close closes the backlog. Call it after Wait.
func (s *Service) Close() error { return s.backlog.close() }
