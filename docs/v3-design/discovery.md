# Discovery: design

Status: approved by the user on 2026-11-19, with decisions 1 to 5 accepted.

The fourth refactorv3 component. It moves discovery out of `internal/server` and
`internal/server/files` into the approved package `internal/discovery`: the walk,
the disk backlog, the worker loop, the run counters and the run itself. Behavior
follows `dev`, with `docs/v3-spec.md` items 8, 9, 10, 11, 12 and 21 as decided, and
`docs/v3-spec/discovery.md` for the details.

## Where discovery lives in `dev`

| `dev` location                                                           | What it does                                  |
| ------------------------------------------------------------------------ | --------------------------------------------- |
| `server/server.go` `TriggerDiscovery`, `persistFileProcessingStats`      | one run: admit, walk, drain, persist, rebuild |
| `server/app_startup.go` `waitForFileProcessingDrain`, completion monitor | drain wait; "File processing completed" log   |
| `server/subsystem_manager.go` (queue, senders, pool, stats, hydrate)     | owns the state the run reads                  |
| `server/discovery_dque.go`                                               | the disk backlog as a `queue.Queuer`          |
| `files/walker.go`, `discovery_modification.go`, `discovery_work.go`      | walk, skip decision, counters, queued item    |
| `files/discovery_catalog.go`                                             | per-directory catalog read from the database  |
| `files/processor.go` worker loop, `ProcessingStats`                      | dequeue, process, record, submit, count       |

State that one run needs is spread over `App`, `SubsystemManager` and `files`, and
the run reaches into all three. In v3 the run, its state and its workers belong to
one `discovery.Service`.

## Package `internal/discovery` (new, approved in the spec)

```go
// Service finds new and changed images and imports them. A run walks the
// images directory, queues each file that is new or changed since the last
// import on a disk backlog, and returns when the workers have processed the
// queue and the writes are committed; it then saves the run's counters and
// rebuilds the folder index. Workers run for the life of the Service.
type Service struct{ /* unexported */ }

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

	OnRunning      func(delta int)   // a run starts (+1) or ends (-1)
	OnFileInserted func(size int64)  // a file was submitted for writing
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
func New(cfg Config, deps Deps) (*Service, error)

// Start starts the workers. They stop when ctx is canceled.
func (s *Service) Start(ctx context.Context)

// Run runs discovery once and returns when it is done. A run already in
// progress makes it log "discovery already in flight" and return nil.
func (s *Service) Run(ctx context.Context) error

// Hydrate loads the last run's saved counters into Stats. Startup calls it
// when it skips discovery, so the dashboard shows the last run.
func (s *Service) Hydrate(ctx context.Context)

// Wait waits for the workers to stop.
func (s *Service) Wait()

// Close closes the backlog. Call it after the workers have stopped.
func (s *Service) Close() error

// Stats are the counters of the current or last run.
type Stats struct {
	TotalFound, AlreadyExisting, NewlyInserted, SkippedInvalid atomic.Uint64
	InFlight                                                   atomic.Int64
}

func (s *Stats) GetStats() metrics.FileProcessingMetrics // metrics.FileProcessorSource, as dev

// PathWork is one queued file: its path and the mtime and size the walk saw.
type PathWork struct {
	Path      []byte
	MtimeUnix int64
	SizeBytes int64
}

func (w PathWork) GobEncode() ([]byte, error) // dev's format, byte for byte
func (w *PathWork) GobDecode(data []byte) error
```

The server creates the pool and the counters and passes them in, because the
metrics collector reads them too; that replaces `SubsystemManager`'s copies. The
`Catalog` function is built by `database` (below), so discovery does not touch
SQL (R16).

### A run, as `dev`

1. If a run is in progress: log `discovery already in flight`, return nil (spec item 8).
2. Reset the counters (spec item 9: always, as `dev`). `OnRunning(+1)`; set the
   `discovery` module-state row active, and inactive when the run ends, logging
   failures as `dev` does.
3. If the backlog is not empty or a walk is still sending, return `dev`'s error
   `discovery walk refused: queue backlog or active senders must be empty before
walk`. The workers keep draining the backlog; the next run walks.
4. Walk the images directory: JPEG, GIF and PNG names, non-empty files. For each
   directory, read the catalog and skip a file whose invalid record or valid row
   matches its mtime and size, counting `TotalFound`, `AlreadyExisting` and
   `SkippedInvalid` as `dev`. Queue the rest on the backlog.
5. Wait until no walk is sending, the backlog is empty, nothing is in flight and the
   writer has nothing pending. It polls every 100 ms, as `dev`; the backlog and the
   writer have no notification to wait on. Cancellation returns the context error
   and skips the rest.
6. Log `File processing completed` with the four counters (see decision 1), save
   them in module state (a failure is logged), and rebuild the folder index; its
   error is the run's error.

The run uses the Service's context, not the caller's, as `dev` does, so canceling
a request does not cancel a run it started.

### Workers, as `dev`

The pool and its scaling are `dev`'s `workerpool`. Each worker dequeues a
`PathWork`, counts it in flight, and passes the path, mtime and size to the
processor. On success it counts `NewlyInserted`, submits the file to the
writer and calls `OnFileInserted`; a failed submit returns the thumbnail buffer.
On failure it records the file as invalid with `dev`'s reason categories, unless
the work was canceled (spec item 10, done in the write path), and stays in
flight until the record is submitted.

Spec item 10 also says workers stop taking work once the run is canceled. In
`dev` the run's context is the workers' context, so a worker already stops
before its next dequeue. v2's per-run context swap and its lock around dequeue
are not needed.

Spec item 12 (one processor per worker) is already met differently: v3's
processor takes a thumbnail `Generator` from a shared `thumbnail.Pool` for each
file, which is how scratch memory is reused. No per-worker processor is needed.

Spec item 11: the worker pool starts after the startup discovery goroutine, as
`dev`.

### Backlog

`dev`'s disk queue: directory `discovery-dque` next to the main database, wiped
when the Service is created, `dque` name `discovery`, 1000 items per segment,
turbo on. `PathWork`'s gob format is `dev`'s `DiscoveryPathWork` format byte
for byte (big-endian mtime, size, path length, path). v2's 45-byte fixture
of that format is copied to pin it.

## Other packages

- **`internal/server/files`.** The processor only processes. `FileProcessor`
  becomes `ProcessDiscoveryFile(ctx, path string, mtime, size int64) (*File, error)`
  and `Close() error`; `NewFileProcessor(imagesDir, limit)`. Removed:
  `RecordInvalidFile`, `SubmitFileForWrite`, `PendingWriteCount` and
  `UnifiedBatcher` (the worker calls the writer itself; decision 3), the worker
  loop, `ProcessingStats`, `WalkImageDir`, `WalkDeps`, `DiscoveryPathWork`, the
  decision and catalog functions, and `NewPoolFuncWithProcessor`. `File` and
  `InvalidFile` stay: the writer stores them.
- **`internal/server/database`.** `NewDiscoveryDirEntMapFunc(ro, normalizedImagesDir,
removePrefix)` moves here from `files` unchanged: it is a database read.
- **`internal/workerpool`.** `PoolFunc`, `StartWorkerPool` and `MonitorPool` lose
  the two database pool parameters, which no worker uses (spec section 3).
  `WaitingTasks` and `DroppedTasks` stay: the dashboard copies them.
- **`internal/parallelwalkdir`.** Two changes from v2, no exported change:
  - Read buffers are trimmed after each directory, so idle walk workers do not
    keep a large listing's memory (spec item 21).
  - v2's fix for a walk that could end early: a directory the feeder held but had
    not counted let the coordinator decide the walk was done, and its files were
    never queued. It is a `dev` bug (decision 2).
- **`internal/server`.** `SubsystemManager` creates the pool, the counters and the
  Service, and drops its queue, senders counter and stats. `TriggerDiscovery` calls
  `Run`. The startup completion monitor, `waitForFileProcessingDrain`,
  `persistFileProcessingStats`, `HydrateFileProcessingStats`, `ResetStats`,
  `discovery_dque.go`, and the monitor's two test seams go.
  `AppTestSeams.TriggerDiscovery` and `RebuildFolderIndex` stay for the
  server-wiring component, which rewrites the tests that use them.

## Tests

- **Run.** Against a real database and images directory: a first run imports
  new files; a second skips them as unchanged (`AlreadyExisting`); a changed file
  is processed again; an invalid file is skipped while unchanged; the counters,
  the saved module-state row and the rebuilt folder index match. A second
  concurrent `Run` returns nil and leaves the first one's counters alone.
  Cancellation skips saving and rebuilding.
- **Workers.** Process, submit and count; a failure records an invalid file
  with its reason; canceled work is not recorded; a failed submit returns the
  thumbnail buffer.
- **Backlog.** `PathWork` round trip against v2's fixture of `dev`'s format; the directory is
  wiped by `New`.
- **Walk.** The skip decision table; only non-empty JPEG, GIF and PNG files are
  queued.
- **parallelwalkdir.** Buffer trimming through a walk; the early-end fix with a
  catalog loader that blocks, to hold a directory at the feeder without a test
  seam (R9).
- Waits are tested under `synctest`.

`dev`'s tests for the moved code move with it. Tests that check only a removed
seam or adapter go (R26).

## Parity

`v3-parity.sh` against v2: every row table should match as in the write-path run,
with `invalid_files` and `module_state` matching or v3 keeping rows v2's race
loses. Time and peak RSS should not move.

## Approvals needed

- **New package:** `internal/discovery` (approved in the spec; recorded with its
  API).
- **Exported API in `dev` packages:** the `files` removals and the new
  `FileProcessor` and `NewFileProcessor`; `database.NewDiscoveryDirEntMapFunc`;
  the `workerpool` signatures. Exact lines from `v3-check.sh` after approval.
- **Renames:** `files.DiscoveryPathWork` becomes `discovery.PathWork`, and
  `files.ProcessingStats` becomes `discovery.Stats` (decision 5).
- **No `go.mod` change.**

## Decisions

1. **One completion log, in the run.** `dev` logs `File processing completed`
   from a startup-only monitor goroutine that polls the counters, then schedules
   the post-discovery `PRAGMA optimize`. Recommended: the run logs it after the
   drain, for every run, and the startup goroutine schedules the optimize after a
   successful run. The monitor and its two test seams go.
2. **Carry v2's early-walk-end fix into `parallelwalkdir`.** Recommended: it is
   a `dev` bug that can silently skip files.
3. **The worker submits to the writer itself.** Recommended: the processor then
   only processes, and the three forwarding methods on `FileProcessor`, including
   `RecordInvalidFile` kept in the write path, go. The worker moved into
   discovery, which removes the reason they were kept.
4. **A leftover backlog refuses the walk, as `dev`.** Spec item 9 reverts to
   `dev`. Recommended: keep `dev`'s refusal error; the workers still process
   the leftover. The backlog is wiped at startup and the run's context is the
   process's, so a leftover only follows a cancellation.
5. **Names:** `discovery.PathWork` and `discovery.Stats`. Recommended: `dev`'s
   names would stutter as `discovery.DiscoveryPathWork`, and `ProcessingStats`
   no longer sits next to a processor.
