# Write path: design

Status: approved by the user on 2026-11-19, with decisions 1 to 4 accepted.

The third refactorv3 component. It moves `dev`'s write path from `internal/server` and
`internal/server/files` into the existing `internal/server/database` package (spec
option B), with v2's chunked folder-index rebuild and its retry-safe thumbnail
lifetime. Behavior follows `dev`, plus `docs/v3-spec.md` items 3, 13 and 22, the
storage spec (`docs/v3-spec/storage.md`, 1.A and 1.B), and the lost `invalid_files`
rows from spec section 7.

## What moves

| `dev` location                                                              | v3 location                      |
| --------------------------------------------------------------------------- | -------------------------------- |
| `server/batched_write.go` (`BatchedWrite`, its gob, `Size`)                 | `database/batch.go`              |
| `server/infrastructure_batcher.go` (batcher config, flush)                  | `database/writer.go`, `flush.go` |
| `server/batcher_wiring.go` (`fileBatcher`, folder-index submit retry)       | `database/writer.go`             |
| `server/folder_index_protocol.go`                                           | `database/folder_index.go`       |
| `server/batched_write_flush.go` (`cleanupBatchedWriteResources`)            | `database/batch.go`              |
| `server/infrastructure_pool.go` WAL checkpoint and periodic optimize        | `database/maintenance.go`        |
| `server/infrastructure_cache.go` `submitCacheWrite`                         | `database/writer.go`             |
| `files/thumbnail.go` (`WriteFileInTx`, `UpsertThumbnailTxOnly`)             | `database/flush.go`              |
| `files/metadata.go` `writeMetadata`                                         | `database/flush.go`              |
| `files/folder_index.go` (`RebuildFileFolderIndex`, `FolderIndexRow`, error) | `database/folder_index.go`       |

What stays: pool setup and reconfigure on `InfrastructureService`, the cache size
counters and LRU eviction, startup and shutdown `PRAGMA optimize`, cache storage in
`cachelite`, `modulestate`, `writebatcher`, `tableswap`, and the discovery backlog
(the discovery component).

## Package `internal/server/database` (exists in `dev`)

```go
// Writer sends the gallery's writes through one batcher: processed files,
// invalid files, HTTP cache entries and folder-index rows. Each batch is one
// transaction. Writes that do not fit in memory overflow to a queue on disk,
// <main db>-dque, and survive a restart.
type Writer struct{ /* unexported */ }

// WriterConfig is what a Writer needs from the server.
type WriterConfig struct {
	Paths            DatabasePaths // main database: queue directory and WAL file
	RW, RO           *dbconnpool.DbSQLConnPool
	MaxDiskBytes     int64         // overflow queue quota; 0 is unlimited
	DeferDQueDrain   bool          // keep queued writes on disk until StartDQueDrain
	OptimizeInterval time.Duration // periodic PRAGMA optimize; 0 is 1 h

	OnFolderCreated func()                   // once per folder a batch creates
	OnCacheStored   func(bytes, entries int64) // after a batch with cache entries commits
	OnCacheFailed   func()                   // after a batch with cache entries fails
}

// NewWriter starts a Writer with dev's startup values: 10000 items or 8 MiB
// per batch, 50 ms flush interval.
func NewWriter(ctx context.Context, cfg WriterConfig) (*Writer, error)

func (w *Writer) SubmitFile(f *files.File) error               // files.UnifiedBatcher
func (w *Writer) SubmitInvalidFile(f files.InvalidFile) error   // files.UnifiedBatcher
func (w *Writer) PendingCount() int64                           // files.UnifiedBatcher

// SubmitCacheEntry queues e. If the Writer is closed or full, e goes back to
// the cache entry pool and the write is dropped.
func (w *Writer) SubmitCacheEntry(e *cachelite.HTTPCacheEntry)

func (w *Writer) StartDQueDrain()
func (w *Writer) SetMaxDiskBytes(n int64)          // hot reload, as dev
func (w *Writer) SetOptimizeInterval(d time.Duration) // hot reload, as dev

// Reconfigure closes the batcher and starts a new one on the new pools with
// dev's values after a reconfigure: 1000 items, 200 ms.
func (w *Writer) Reconfigure(ctx context.Context, rw, ro *dbconnpool.DbSQLConnPool, maxDiskBytes int64) error

// RebuildFolderIndex rebuilds file_folder_index from the files table.
func (w *Writer) RebuildFolderIndex(ctx context.Context) error

// Close stops the batcher. Submits after Close are dropped. Queued writes
// stay on disk for the next start.
func (w *Writer) Close() error

// ErrFolderIndexRebuild wraps every rebuild failure except cancellation.
var ErrFolderIndexRebuild = errors.New("file_folder_index rebuild failed")
```

`BatchedWrite` and `FolderIndexRow` become unexported (`batchedWrite`,
`folderIndexRow`): nothing outside the package needs them.

The Writer is created once per process and owned by `InfrastructureService`.
`Reconfigure` swaps the batcher inside it under the Writer's lock, so request
goroutines never read a pointer that the reconfigure is replacing. In `dev` the
reconfigure reassigned `s.writeBatcher` while handlers read it.

The three callbacks are the server's real dependencies (R9): the folder count
metric, and the cache size counters and eviction, which the cache middleware also
reads. Everything else that `dev` passed through `InfrastructureService` (queries
held in closures, `lastFlushWroteDML`, the folder-index atomics, the optimize
clock) is the Writer's own state.

### Flush, as `dev`

- One transaction per batch, on a connection taken from the RW pool in `BeginTx` and
  returned on success or failure. The connection and its queries are fields of
  the flush, not variables shared by five closures.
- Order in a batch: files, invalid files, gallery cache entries (`/gallery/` paths),
  other cache entries, folder-index rows.
- Per file: `UpsertPathChain`, delete the stale `invalid_files` row (a failure is
  logged), replace EXIF, IPTC and XMP, upsert the thumbnail only if needed, update
  the folder tile once per directory per batch (a failure is logged).
- A batch of only folder-index rows while no rebuild runs is dropped before
  `BeginTx`. Rows from an older generation are skipped and logged.
- After a commit: cache counters and eviction through `OnCacheStored`, folder-index
  in-flight count, pooled buffers returned. After a failure: the log line
  `failed to flush unified batch` and, with cache entries in the batch, a resync
  through `OnCacheFailed`.
- After each commit, and every 5 minutes: WAL `TRUNCATE` checkpoint when the WAL
  is over 256 MiB or 5 minutes have passed, skipped while the rebuild's read cursor
  is open or when the last flush wrote nothing. `PRAGMA optimize` when the interval
  has passed.
- Log lines and their keys stay as `dev` has them.

### Retry-safe thumbnails (v2 1.B.2)

In `dev`, `WriteFileInTx` returns the thumbnail buffer to the pool before commit.
When the commit fails, the batcher re-enqueues the file and the buffer may already
belong to another file. In v3 the per-file write neither changes the queued
`files.File` nor releases its buffer; buffers go back to the pool only after a
commit. A file the batcher fails to re-enqueue is dropped and logged, as in `dev`,
and its buffer is left to the garbage collector.

v2 added `writebatcher.OnRelease` to return those dropped buffers to the pool, and
copied each file before writing it. Neither is needed: a buffer the collector frees
is not a bug, and a write that does not change its input needs no copy. So
`writebatcher` does not change.

### Folder-index rebuild (spec item 3)

v2's algorithm, unchanged in effect:

- Clone `file_folder_index_new` on a short-lived RW connection, then mark the
  rebuild active with a new generation.
- Read `(file_id, folder_id)` pairs on the RO pool in chunks of at least 2^18 pairs,
  each ending at a folder boundary, so no folder is split and a folder larger than
  a chunk is read whole. The cursor closes and the connection goes back before any
  row is submitted. The pairs buffer (4 MiB) is allocated once.
- Submit each folder's rows. When the batcher is full, retry while its pending count
  falls; give up after 30 s without progress.
- Wait for the in-flight count to reach 0, again with a 30 s stall limit, then
  create the indexes and swap on a new RW connection.
- Cancellation returns the bare context error; every other failure wraps
  `ErrFolderIndexRebuild`.

The chunk size is an argument of an unexported function, so tests use small
chunks without a package variable (R14). `RebuildFolderIndex` passes the constant.

The submit retry and the in-flight wait poll every 100 ms, as in `dev`, but on a
ticker that also returns on cancellation. `writebatcher` has no notification for a
falling pending count, and the full-batcher case is rare (the overflow queue
absorbs it). Tests run these under `synctest`.

### Shutdown (spec item 13)

`Close` takes the Writer's lock, marks it closed and closes the batcher. Submits hold
the read lock, so a submit either finishes before the close or sees it and drops
its write, returning a cache entry to its pool. The folder-index retry takes the
lock for each attempt, not across its waits, so `Close` never waits 30 s behind it.

## Package `internal/server/files` (exists in `dev`)

The processor stops touching the database.

```go
// UnifiedBatcher is where the processor sends its results. *database.Writer
// implements it.
type UnifiedBatcher interface {
	SubmitFile(f *File) error
	SubmitInvalidFile(f InvalidFile) error
	PendingCount() int64
}

// InvalidFile is a file the processor could not import.
type InvalidFile struct {
	Path        string // gallery path
	Mtime, Size int64
	Reason      string // empty is stored as NULL
}

func NewFileProcessor(imagesDir string, batcher UnifiedBatcher, limit *memlimit.Limit) FileProcessor
```

Removed: `WriteFileInTx`, `UpsertThumbnailTxOnly`, `ThumbnailTx`,
`RebuildFileFolderIndex`, `FolderIndexRow`, `ErrFolderIndexRebuild`, the
`Importer` interface and `ImporterFactory`, `FileProcessor.RecordInvalidFile`, and
the five folder-index methods of `UnifiedBatcher`. `NewFileProcessor` loses the two
pools and the importer factory: the factory was stored and never used, and the
pools served only `RecordInvalidFile`.

## Lost `invalid_files` rows (spec section 7)

In `dev` and v2 the processor writes an invalid file directly. It looks up the
parent folder first and fails with `no folder for gallery path parent` when the
folder's valid files are still in the batcher. The row is lost; the parity
self-test and both thumbnail parity runs showed it.

In v3 the processor submits an `InvalidFile` to the Writer. The flush creates the
parent folder chain if needed, with the batch's importer, and upserts the row. A
folder holding only invalid files gets a folder row too, as it would if one of its
files were valid.

This needs the folder-chain half of `UpsertPathChain` as its own method:

```go
// UpsertFolderChain returns the ID of the folder at dir, creating it and its
// parents if needed.
func (imp *Importer) UpsertFolderChain(ctx context.Context, dir string) (int64, error)
```

`UpsertPathChain` calls it. Its SQL and folder cache are unchanged.

## Package `internal/server` (exists in `dev`)

- `InfrastructureService` holds `writer *database.Writer` instead of the batcher,
  the queries, the folder-index protocol, `lastFlushWroteDML`, the optimize clock
  and interval.
- The rebuild at discovery completion calls `writer.RebuildFolderIndex`.
- Removed test seams (R9): `BuildWriteBatcher`, `FlushBatchedWrites`,
  `BeginTxPoolGetErr`, `OnBeginTx`, `OnPut`, `PerformWALCheckpoint`,
  `WALCheckpointQuery`, `PragmaOptimize`, `RebuildFileFolderIndex`,
  `ShutdownWriteBatcher`. `App.ImporterFactory` and
  `InfrastructureService.ImporterFactory` go with the factory.

## Other packages

- **`gallerydb`.** v2's chunked query, copied:
  `QueryFilesForFolderIndexRebuild(ctx, afterFolderID int64)`, and
  `CountFilesForFolderIndexRebuild` removed with its SQL.
- **`dbconnpool`** (spec item 22). `Close` waits for the monitor goroutine after
  closing `done`. No other change.
- **`writebatcher`, `tableswap`, `modulestate`, `cachelite` storage.** Unchanged.

## Tests

All against a real database (`internal/testdb`), no injected failures:

- **Flush.** Files, invalid files, cache entries and folder-index rows in one batch
  produce the expected rows. Re-importing a file replaces its metadata.
- **Retry.** A SQLite trigger aborts the first flush. The files are re-enqueued and
  committed with their thumbnails intact, byte for byte.
- **Invalid files.** An invalid file whose folder has no committed row yet is
  recorded with the right `folder_id`. So is a folder of only invalid files.
- **Rebuild.** Small chunks give the same rows as one chunk; a folder larger than a
  chunk is not split; an empty table; cancellation leaves the old table in place.
  Navigation columns match dev's for a fixed set of files.
- **Shutdown.** Submits after `Close` are dropped and cache entries go back to their
  pool. Submits keep working across `Reconfigure`.
- **Maintenance.** The checkpoint decision (WAL size, elapsed time, scan held, no DML)
  as a function of its inputs; the checkpoint itself against a real WAL file.
- **`dbconnpool`.** `Close` returns after the monitor goroutine exits (`synctest`).

`dev` tests that check only a removed seam or call sequence go (R26). Tests that
check rows move with the code; Haiku does the mechanical moves. About 22 test
files mention the moved code.

## Parity

`v3-parity.sh` against v2: `files`, `thumbnails`, `folders` and `file_folder_index`
should match on every row. `invalid_files` should gain the rows v2 loses, which
the parity report will show as v3-only rows; each will be checked to have a reason
and the right folder. Time and peak RSS should match the thumbnail component's run.

## Approvals needed

- **Exported API in `dev` packages:** the `database` additions above; the `files`
  removals, `InvalidFile`, the smaller `UnifiedBatcher` and the new
  `NewFileProcessor` signature; `gallerylib.Importer.UpsertFolderChain`; the
  `gallerydb` query change. The exact lines go into `docs/v3-approvals.txt` from
  `v3-check.sh` once approved.
- **No new package, no rename, no `go.mod` change.**

## Decisions

1. **Fix the lost `invalid_files` rows here**, by sending invalid files through the
   Writer as above. Recommended: the write path is where the race is, and the fix
   removes the processor's last database access.
2. **No `writebatcher.OnRelease`.** Recommended, for the reasons in "Retry-safe
   thumbnails". The alternative is v2's API addition for pool reuse on a rare
   failure path.
3. **Fix `CleanupExpired`** to return `RowsAffected` instead of 1 (spec section 7),
   so the "deleted" log is true. Recommended: a one-line change in `cachelite`.
4. **Drop the debug `batched flush: file batch path stats` log.** Recommended: it
   builds a map and splits every path on each flush only for a debug line (R21).
