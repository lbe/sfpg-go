# refactorv3 image-stack spec

Status: decided by the user on 2026-11-18. Every recommendation is approved, with
the changes recorded in sections 1 and 2. Exact approval entries go into
`docs/v3-approvals.txt` as each component's design lands.

refactorv3 rebuilds the image-stack work from `dev` with `refactorv2/image-stack` (v2)
as the behavior and performance spec. This file records the decisions and the order of the work. The component files in `docs/v3-spec/` hold the
detail: v2 behavior with `path:line` sources, the exported API delta, a verdict per
v2 file, and open questions.

The component files were drafted by agents from the v2 source and have not all been
checked line by line. The claims in [Verified](#verified) were checked against the
code. Treat the rest as leads to confirm in each component's design.

## 1. Packages (R1, R11)

| Package                            | v2 role                                                                                             | Recommendation                                                                                                                 |
| ---------------------------------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `internal/discovery`               | Walk, queue, workers, run state and counters, moved out of `files` and `server`.                    | Approved. It owns real logic, not a cycle. Rewrite it: database types, a stored context, test seams and polling waits.         |
| `internal/metadata`                | ExifTool server lifecycle.                                                                          | Approved, and move the ExifTool tag mapping into it so it returns plain Go types. That removes `sql.Null*` from `files` (R16). |
| `internal/memlimit`                | Process memory limit, GC policy and decoder reservations.                                           | Approved. Give the budget numbers one owner; v2 duplicates the 256 MiB retention figure in `memlimit` and `thumbnail`.         |
| `internal/storagepayload`          | Payload types shared between six packages.                                                          | Rejected. It exists to break import cycles that v2 created by moving the write path.                                           |
| `internal/server/database/storage` | The write path, cache storage, module state and the discovery backlog, most of it moved from `dev`. | Rejected. Put the write path in the existing `internal/server/database` package (option B below).                              |

Two layouts were considered for the write path:

- **A: `dev`'s layout.** The batch envelope and flush stay in `internal/server`, cache
  storage stays in `cachelite`, and `files.UnifiedBatcher` stays the consumer's
  interface. No new package and almost no API change.
- **B: `internal/server/database` owns the write path. Selected by the user
  on 2026-11-18.** This keeps
  the v2 boundary the user wanted, persistence in the database layer, without a new
  package. `database` imports `files`, `cachelite` and `thumbnail`; none of them
  imports `database` in `dev`, so there is no cycle. `cachelite` keeps its cache
  types and operations, which removes v2's `cachelite` → `storage` inversion.
  `modulestate` stays as in `dev`; v2's duplicate module-state SQL in `storage` goes
  (R6).

## 2. Behavior beyond the four approved changes

Decided by the user on 2026-11-18: every recommendation below is accepted, with
items 4, 7, 9 and 18 as recorded in the table.

The approved changes are the new pHash, Wuffs decoding, ExifTool metadata and IPTC,
and malformed JPEGs failing in decode. v2 also changed the behavior below. Each needs
a yes or no. Most are v2's measured memory and performance work, which v3 needs for
performance parity.

| #   | v2 change                                                                                                                                                             | Recommendation                                                                                                                                                                                                                               |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | 192 MiB cap on in-flight source reads, 256 MiB scratch retention budget, one slot for oversized decodes.                                                              | Keep. Implement with `semaphore.Weighted` and an atomic counter.                                                                                                                                                                             |
| 2   | `memlimit` sets a soft memory limit and turns GC off unless `GOGC` is set; it can reduce workers, down to 1.                                                          | Keep.                                                                                                                                                                                                                                        |
| 3   | `file_folder_index` rebuilt in folder-bounded chunks (at least 2^18 pairs per chunk).                                                                                 | Keep; the output rows are the same.                                                                                                                                                                                                          |
| 4   | `RestartAfterDiscovery` default false → true.                                                                                                                         | Decided: default true.                                                                                                                                                                                                                       |
| 5   | `DBMinIdleConnections` default 10 → 1.                                                                                                                                | Keep (memory).                                                                                                                                                                                                                               |
| 6   | Upgrading from a schema below the pHash migration (v2 24, v3 25) deletes `<db>-dque` (queued writes still carry old hashes).                                          | Keep, using `m.Version()` instead of v2's `schema_migrations` parser.                                                                                                                                                                        |
| 7   | The migrator's connection opens with `sql.Open("sqlite", …)` (the modernc driver golang-migrate registers) instead of `"sqlite3"` (ncruces). No comment explains why. | Decided: golang-migrate supports only modernc (`database/sqlite`) and mattn (cgo), so ncruces cannot be used. With item 6, v3 opens no separate connection: `m.Version()` reads the version through golang-migrate's own modernc connection. |
| 8   | A concurrent discovery request is a silent no-op; `dev` logged "discovery already in flight".                                                                         | Restore the log.                                                                                                                                                                                                                             |
| 9   | Run counters are not reset when the backlog is still busy.                                                                                                            | Decided: revert to `dev`. Always reset counters at the start of a run and process any leftover backlog in that run; no `backlogBlocked` flag or context swap.                                                                                |
| 10  | Canceled work is not recorded in `invalid_files`, and workers stop dequeuing once the run is canceled.                                                                | Keep; it stops shutdown from marking good files invalid.                                                                                                                                                                                     |
| 11  | The worker pool starts before startup discovery.                                                                                                                      | Revert to `dev`'s order unless a design shows a need.                                                                                                                                                                                        |
| 12  | One processor and workspace per worker instead of one shared processor.                                                                                               | Keep; it is how scratch reuse works.                                                                                                                                                                                                         |
| 13  | Cache writes are dropped once shutdown starts, instead of racing the batcher close.                                                                                   | Keep; it fixes a race.                                                                                                                                                                                                                       |
| 14  | An ExifTool error fails the file (reason `exif`); `dev` ignored decode errors.                                                                                        | Keep.                                                                                                                                                                                                                                        |
| 15  | An ExifTool startup failure is sticky and fails every later file in the run.                                                                                          | Change: retry on the next file.                                                                                                                                                                                                              |
| 16  | The 5 s metadata extraction timeout is gone.                                                                                                                          | Restore a timeout unless the design finds a reason not to.                                                                                                                                                                                   |
| 17  | EXIF, IPTC and XMP are replaced per file (delete, then insert).                                                                                                       | Keep.                                                                                                                                                                                                                                        |
| 18  | ExifTool idle shutdown is 30 to 60 s, not 30 s: the janitor ticks every 30 s.                                                                                         | Decided: keep v2's 30 to 60 s shutdown. `New` starts one goroutine with a 30 s ticker that shuts down servers idle for 30 s or more; `Close` stops it and waits. Idle servers are a slice under the extractor's mutex.                       |
| 19  | PNG and GIF pixels come from Wuffs (premultiplied 8-bit RGBA; GIF frame 0 at its bounds), so thumbnails can differ from `dev`.                                        | Keep, as part of Wuffs decoding.                                                                                                                                                                                                             |
| 20  | The pHash of a JPEG is computed at the DCT-scaled decode size.                                                                                                        | Keep.                                                                                                                                                                                                                                        |
| 21  | Walk read buffers are trimmed after each directory.                                                                                                                   | Keep.                                                                                                                                                                                                                                        |
| 22  | `dbconnpool.Close` waits for the monitor goroutine.                                                                                                                   | Keep (R18).                                                                                                                                                                                                                                  |

## 3. Exported API changes in `dev` packages (R2)

Each component's design lists its exact changes for approval. The larger ones known
now:

- **`gallerydb` queries (R6).** Change the existing queries in place and drop the
  four v2 additions (`DeleteIPTCKeywordsByFile`, `InsertIPTCKeywordForFile`,
  `DeleteXMPPropertiesByFile`, `InsertXMPPropertyForFile`). The existing
  `InsertIPTCKeyword`, `DeleteIPTCKeyword`, `UpsertXMPProperty` and
  `DeleteXMPProperty` have no production caller in v2. Proposed:
  `DeleteIPTCKeyword` → `DeleteIPTCKeywords` (by file), `InsertIPTCKeyword` without
  `id`, `DeleteXMPProperty` → `DeleteXMPProperties` (by file), `UpsertXMPProperty` →
  `InsertXMPProperty`, and a new `DeleteExif`.
- **`files`.** Remove the discovery entry points that move to `internal/discovery`
  (`WalkImageDir`, `WalkDeps`, `ProcessingStats`, `DiscoveryPathWork`) and the JPEG
  marker functions.
- **`thumbnail`.** Results in plain types (`md5 string`, `phash int64`), not
  `sql.Null*` (R16).
- **`workerpool`.** The signature changes v2 made (no database pool parameter,
  `DefaultMaxWorkers`). `WaitingTasks` and `DroppedTasks` stay; their removal is v2
  dead-code work that stays on v2.

## 4. `go.mod` (R3)

- Add `lbe/go-exiftool-wasm`, `lbe/wuffs-wasm` and `lbe/go-image-phash` at v2's
  versions, plus whatever indirect modules they require.
- Remove `evanoberholster/imagemeta`, its `replace`, and the `internal/imagemeta`
  submodule.
- The `golang.org/x/*` upgrades in v2 are approved only if minimum version selection
  requires them; `go mod tidy` on v3 shows which.

## 5. Work order

Changed by the user on 2026-11-18: the query changes go with the metadata
component, v2's migration 024 (025 in v3) and the hash cutover with the thumbnail component, and the
folder-index query with the write path. The order is metadata, thumbnail and
memlimit, write path, discovery, server wiring, finish. See
`docs/v3-design/metadata.md`.

Each component: a design (Go types, exported API, the behavior rows above that apply),
user approval, implementation, then `v3-check.sh`, `v3-parity.sh` and
`v3-review.sh`.

1. **Schema and queries.** v2's migration 024 copied byte for byte as 025, the query changes in
   place, the hash cutover. Parity: schema and `files.phash`.
2. **metadata.** Extractor, tag mapping, persistence. Parity: EXIF, IPTC and XMP tables.
3. **thumbnail and memlimit.** Workspace, Wuffs, scaled JPEG, MD5 and pHash. Parity:
   `files`, `thumbnails`, `invalid_files`.
4. **Write path in `database`.** Batch envelope, flush, chunked folder index, cache
   storage left in `cachelite`. Parity: `file_folder_index`, `folders`.
5. **discovery.** Service, walker, workers, backlog. Parity: all tables, and time and
   peak RSS against v2.
6. **Server wiring.** One owner per subsystem; delete v2's duplicate state, flags and
   hook closures.
7. **Finish.** Documentation, ExifTool and Perl licence notices, regenerated
   `default.pgo`, the run24 and run26 acceptance runs.

## 6. Not carried from v2

- Doc-comment churn in packages the image stack does not touch (most of 7db86eb).
- Evidence and measurement code: `scripts/verify-image-stack-v2-rel.sh`, the
  `v2_rel_*` and characterization tests, `Stats`/`snapshot`, flush debug logs.
- `plans/**`.
- The v2 dead-code commits d25bff2 and 3a0816a.
- Comment-only edits in `gensyncpool` and other packages outside the image stack.

## 7. `dev` bugs found

- **Lost `invalid_files` rows.** Recording an invalid file races the creation of its
  folder ("no folder for gallery path parent"), so the row is sometimes lost. Found
  by the `v3-parity.sh` self-test. v3 should fix it in the discovery or write-path
  design.
- **`CleanupExpired` always returns 1**, so the "deleted" log is wrong. v2 kept it.
  Recommend returning `RowsAffected`.

## Verified

Checked against the code by the orchestrator:

- The four by-ID IPTC and XMP queries have no production caller in v2; the four
  by-file additions are called from `gallerylib/importer.go` and the storage package.
- v2 changed `sql.Open("sqlite3", …)` to `sql.Open("sqlite", …)` in `migrateDB`
  (commit da741c7), and changed the `RestartAfterDiscovery` and
  `DBMinIdleConnections` defaults.
- The import graph behind `storagepayload` and the `cachelite` → `storage` edge
  (`go list` on v2), and that option B creates no cycle (`go list` on `dev`).
- The `invalid_files` race (parity self-test log).
