# thumbnail and memlimit: design

Status: approved by the user on 2026-11-18, with decisions 1 to 3 accepted.

The second refactorv3 component. It replaces `dev`'s thumbnail path with v2's: one
read of each file, Wuffs for PNG and GIF, the new pHash, reusable scratch memory
under v2's budgets, a process memory limit, and the pHash migration with its
cutover. Behavior follows v2 (`docs/v3-spec/thumbnail.md`) and `docs/v3-spec.md`,
items 1, 2, 6, 7, 12, 19, 20 and 22.

## Package `internal/thumbnail` (exists in `dev`)

```go
// Pool hands out Generators and owns the memory budgets they share.
type Pool struct{ /* unexported */ }

// NewPool returns an empty pool.
func NewPool() *Pool

// Get returns an idle Generator or a new one. The caller owns it until Put.
func (p *Pool) Get() *Generator

// Put ends the current file's work and keeps g for reuse.
func (p *Pool) Put(g *Generator)

// Close releases every idle Generator and its decoder.
func (p *Pool) Close()

// Generator makes the thumbnail and hashes for one file at a time. It keeps
// the file's bytes, and reuses its scratch memory and decoder for the next
// file. It is not safe for concurrent use.
type Generator struct{ /* unexported */ }

// Read reads the whole file from r. size is the size seen by the walk,
// used to pre-size the buffer and to charge the in-flight budget.
func (g *Generator) Read(ctx context.Context, r io.Reader, size int64) ([]byte, error)

// GenerateThumbnailAndHashes decodes the source, makes the gallery thumbnail,
// and hashes the source: MD5 of the bytes, pHash of the decoded pixels.
func (g *Generator) GenerateThumbnailAndHashes(ctx context.Context, srcW, srcH int) (thumb *bytes.Buffer, md5 string, phash int64, err error)

// RetainedBytes is the most scratch memory Generators keep between files.
const RetainedBytes = 256 << 20

func GetBytesBuffer() *bytes.Buffer   // unchanged
func PutBytesBuffer(buf *bytes.Buffer) // unchanged
```

`GenerateThumbnailAndHashes` keeps `dev`'s name. It becomes a method, because it
needs the generator, and it returns plain values instead of `sql.Null*` (R16).

### Behavior kept from v2

- **One read.** `Read` reads the file once into the generator. MIME detection,
  `image.DecodeConfig`, decoding and MD5 all use those bytes. `dev` opened and read
  each file several times.
- **Decode.** PNG and GIF through Wuffs (premultiplied 8-bit RGBA; GIF frame 0 at its
  bounds). JPEG through go-scaled-jpeg at the DCT scale that still covers the 200×150
  box, with no fallback. Anything else through `image.Decode`. Wuffs validates
  dimensions and stride before allocating.
- **Thumbnail.** Fit inside 200×150 with `draw.ApproxBiLinear`, upscaling small
  images; encode JPEG at quality 75 into a pooled buffer that does not alias
  the generator's memory.
- **Hashes.** MD5 of the source bytes as lower-case hex. pHash of the decoded source
  pixels with `go-image-phash` (`NewFromImage(src, 32)`, 64-bit average hash). A
  pHash error is logged and stores 0, as in `dev` and v2.
- **Memory budgets** (spec item 1), shared through `Pool`:
  - **In flight.** A 192 MiB `semaphore.Weighted`. Each file takes
    `min(max(size, 32 KiB), 192 MiB)` before reading and gives it back at `Put`, so
    a file of 192 MiB or more reads alone.
  - **Retention.** An atomic counter of scratch kept between files, capped at
    `RetainedBytes`. A generator keeps at most an 8 MiB source buffer and 16 MiB of
    decode memory. When the budget is spent, the file uses temporary buffers that are
    dropped at `Put`. Nothing waits on it.
  - **Oversized.** A `semaphore.Weighted` of 1, taken with the caller's context by a
    file whose source exceeds 8 MiB or whose decode exceeds 16 MiB, and released at
    `Put`. The decode estimate counts RGBA bytes at the DCT scale plus progressive
    JPEG coefficient storage, read from the SOF header.
- **Decoder.** Each generator makes its Wuffs decoder on its first PNG or GIF and keeps
  it for its later files. A Wuffs decode with more than 4 MiB of output is followed by
  `Trim`. The decoder is closed after a failed Wuffs decode and at `Close`.
- **Cancellation.** Checked before the read, between read chunks, and between decode
  stages. A canceled file returns the context's error, not a decode error.

### Removed from `dev`

- The test hooks `jpegEncodeHook`, `ioCopyHook`, `newPHash64Hook`,
  `fullImageDecodeHook`, `thumbResizeHook` and `decodeJPEGScaledHook` (R9).
- `GetNullString`, `PutNullString`, `GetNullInt64`, `PutNullInt64`, `GetMD5`, `PutMD5`
  and their pools. Results are plain values, and `md5.Sum` needs no pooled hasher.
- The package-level `GenerateThumbnailAndHashes(io.ReadSeeker, …)` and the 64×64 pHash
  canvas.
- imagemeta: its `imagehash` was the last use, so the dependency, its `replace`, and
  the `internal/imagemeta` submodule go.

## Package `internal/memlimit` (new, approved)

```go
// Need is what the process needs memory for.
type Need struct {
	Workers         int   // discovery workers
	ExifToolServers int   // metadata.PoolSize()
	RetainedBytes   int64 // thumbnail.RetainedBytes
}

// Limit is the process memory limit set by Apply.
type Limit struct {
	Workers int // the worker count that fits the limit: Need.Workers, or 1
	// unexported: the base limit and ceiling
}

// Apply sets the runtime memory limit and GC policy for need, unless
// GOMEMLIMIT is set, and returns the limit.
func Apply(need Need) *Limit
```

Behavior is v2's (spec item 2):

- RAM is the smaller of the machine's memory and the cgroup v2 limit. The ceiling is
  75% of RAM.
- The limit is `20 MiB + 13 MiB × workers + 64 MiB × servers + retained + 96 MiB`.
- If that exceeds the ceiling, it is recomputed with one worker. If that still doesn't
  fit, the limit is the ceiling and workers is 1.
- `SetMemoryLimit`, and `SetGCPercent(-1)` unless `GOGC` is set.
- With `GOMEMLIMIT` set, nothing changes and the workers are unchanged.
- One `memory limit` log line with v2's keys, at Warn when workers were cut.

Two changes from v2:

- **No package state.** v2 kept the limit in package variables behind a `sync.Once`
  (R14). Here `Apply` returns a `Limit` that its caller owns and passes to
  `thumbnail.NewPool`.
- **One owner for the retained-bytes figure.** v2 repeated thumbnail's 256 MiB as a
  constant in memlimit. Here the caller passes `thumbnail.RetainedBytes` in `Need`.

The subsystem manager calls `Apply` once, before it builds the worker pool, with
`metadata.PoolSize()`. That makes `PoolSize` an exported addition to `metadata`.

## Changes in `internal/server/files`

- `fileProcessor` owns one `*thumbnail.Pool`, made in `NewFileProcessor` from a
  `*memlimit.Limit` argument and closed in `Close`.
- `processFileContents` takes a generator with `Get`, reads the file with
  `Read`, detects MIME on the first 512 bytes of the snapshot, runs the
  ExifTool extraction (which reads the path itself), `DecodeConfig` on the snapshot,
  then `GenerateThumbnailAndHashes`, and `Put`s the generator on every return.
- `File.File.Md5` and `Phash` are set from the plain results.

Using a pool rather than a generator per worker goroutine keeps
`FileProcessor`'s methods unchanged. It reuses memory the same way, because at most
one generator is in use per running worker. The discovery component can tie
generators to workers if its design needs that.

## Schema: pHash migration and cutover

- **Migration 025** is v2's `024_clear_legacy_phash`, content unchanged:
  `UPDATE files SET phash = NULL WHERE phash IS NOT NULL;`, down `SELECT 1;`.
- **Cutover** (spec item 6), in `migrateDB`:
  1. Read the version with golang-migrate's `m.Version()`.
  2. If it is dirty, fail startup.
  3. If there is no version yet or it is below 25, remove `<db>-dque`, the write
     overflow queue, whose entries still carry old hashes. A missing directory is fine;
     a failed removal fails startup.
  4. Run `m.Up()`.

  The separate `sql.Open` connection goes (spec item 7). Unchanged files keep a NULL
  pHash until they are processed again.

A database that v2 migrated to its own 24 would read as already having v3's index
migration, and would run 025 again. v2 was never released, so this affects only
test databases from v2 runs, which need rebuilding.

## `go.mod`

Adds `github.com/lbe/wuffs-wasm v0.4.0` and `github.com/lbe/go-image-phash v0.2.0`, as
in v2, with what they require. Removes `github.com/evanoberholster/imagemeta` and its
`replace`.

## Approvals this design asks for

In the form `scripts/v3-check.sh` will print them, added after you approve:

- `thumbnail`:
  - new: `Pool`, `NewPool`, `Generator` and its methods, `RetainedBytes`;
  - removed: the package-level `GenerateThumbnailAndHashes`, the `Null*` pool
    functions, `GetMD5` and `PutMD5`.
- the new package `internal/memlimit` and its API above;
- `metadata.PoolSize`;
- `files.NewFileProcessor` gaining a `*memlimit.Limit` parameter;
- the `go.mod` changes above;
- migration 025.

## Decisions

All three accepted by the user.

1. **Generators from a pool owned by the processor**, not one per worker
   goroutine. I recommend it: no change to `FileProcessor`, same reuse. The discovery
   component can revisit it.
2. **memlimit without package state**: `Apply` returns a `Limit` the caller owns. I
   recommend it (R14).
3. **The retained-bytes figure passed into `memlimit`** from
   `thumbnail.RetainedBytes`, not duplicated. I recommend it.

## Tests

- `thumbnail`, unit:
  - fit-box sizes;
  - DCT scale choice;
  - the decode estimate, including progressive JPEG;
  - Wuffs PNG and GIF against stdlib pixels where they should match;
  - budget behavior: in-flight blocking and release, retention falling back to
    temporary buffers, the oversized slot honoring cancellation;
  - decoder kept and dropped by the rules above.
- `thumbnail`, integration: real files from the corpus through `Read` and
  `GenerateThumbnailAndHashes`. MD5 and pHash values are pinned against v2's output for
  the same files.
- `memlimit`: the limit computation as a pure function of RAM and `Need`, and `Apply`
  with `GOMEMLIMIT` set (no process changes).
- `database`: the cutover with real migrations: a fresh database, one at 23 with an
  overflow queue that is removed, one at 25 left alone, and a dirty one that fails.
- Tests of `dev`'s hooks, pools and the package-level `GenerateThumbnailAndHashes` are
  deleted with them (R5).

## Verification

- `scripts/v3-check.sh` passes once the approvals are recorded.
- `scripts/v3-parity.sh` against v2: `files` (MD5, pHash, width, height),
  `thumbnails`, `invalid_files` and the metadata tables match v2, apart from the race
  that loses some `invalid_files` rows in both. Time and peak RSS are comparable to
  v2's.
- `scripts/v3-review.sh`, then the commit hook's full suite.
