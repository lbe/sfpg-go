# Finish: design

Status: decisions 1 to 4 approved by the user on 2026-11-19. `default.pgo` is regenerated (§3).

The last refactorv3 component (spec §5, item 7): documentation, the third-party
licence notices, a regenerated `default.pgo`, and the run24 and run26 acceptance
runs, where peak memory is judged. It changes no Go code in the image stack. If
an acceptance run fails, this component stops and the evidence goes to the user;
a fix needs its own design.

## 1. Documentation

`docs/ARCHITECTURE.md` still describes `dev`'s image stack. Each stale section is
rewritten to match the code, and nothing else in it changes:

- **§1.1, §15.2 (stack and external dependencies).** `imagemeta` is gone. Metadata
  comes from ExifTool through `go-exiftool-wasm`; decoding uses `wuffs-wasm` and
  `go-scaled-jpeg`; pHash uses `go-image-phash`. Licences as in §2 below.
- **§2.2, §2.3 (package map).** Add `discovery`, `metadata` and `memlimit`.
  `database.Writer` owns the write path and uses `writebatcher` underneath; the
  `files` row says processing only.
- **§3.1, §4.1, §4.2 (diagrams and component table).** The same owners: discovery
  Service, FileProcessor, Writer, memlimit.
- **§5.1 (write path).** Replace "Unified WriteBatcher" with `database.Writer`:
  one per process, flushes on the RW pool, thumbnails released after commit,
  overflow to `<db>-dque/`, folder-index rebuild in chunks, maintenance.
- **§5.3 (pools).** `db_min_idle_connections` defaults to 1. Pools are
  reconfigured only before the Writer starts; a runtime change restarts the
  process.
- **§6.2, §13.4.5 (test seams).** Remove `TriggerDiscovery`,
  `RebuildFileFolderIndex`, `OnStartupDiscoveryRebuildFailedShutdown`,
  `BuildWriteBatcher` and `ShutdownWriteBatcher` rows, and any other seam the
  components removed; list the ones that remain.
- **§6.3, §7.1, §7.2 (lifecycle, discovery, pipeline).** Startup discovery runs on
  the runtime wait group; a failed folder-index rebuild makes `Run` return the
  error and the process exit 1; `restart_after_discovery` defaults to true. The
  pipeline table names the `discovery`, `files`, `metadata` and `database`
  functions that now do each step.
- **§10.4 (hot configuration).** Pool settings need a restart.
- **§11.1.10 (`imagemeta`).** Replaced by short `metadata` and `memlimit`
  sections.
- **§13.1, §15.1 (test and file layout).** Match the tree.
- **§12.4 (performance timeline).** One line with the acceptance results.

Dated history sections (§10.2, §13.6 to §13.8) stay as written.

`README.md`: the package list (`files`, `thumbnail`, `imagemeta`) and a License
section as in v2. `DEPLOYMENT.md`: v2's line that redistributing the binary means
shipping `LICENSE`, `THIRD_PARTY_NOTICES.md` and `licenses/`.
`ENV_CONFIGURATION.md` already has the new defaults.

## 2. Third-party notices

v3 has none of v2's notice files. The binary embeds ExifTool and a Perl
interpreter compiled to WebAssembly (Artistic or GPL-1.0-or-later), and the Wuffs
decoder compiled the same way.

- Carry v2's `THIRD_PARTY_NOTICES.md`, `licenses/Artistic-1.0-Perl.txt` and
  `licenses/GPL-1.0.txt` (commit 77f8054), with the README and DEPLOYMENT lines
  above.
- Check the ExifTool and Perl versions it states (13.55, 5.16.3) against what
  `go-exiftool-wasm` v0.6.0 embeds, by running ExifTool's version query through
  the module; correct them if they differ.
- Add Wuffs, embedded through `wuffs-wasm` v0.4.0, which ships only its own licence. Wuffs is Apache-2.0 OR MIT at the user's option; sfpg-go uses it under MIT, with `licenses/MIT-Wuffs.txt` (copyright 2023 The Wuffs Authors). v2's notice left it out.
- Add zeroperl, the Perl build for WebAssembly (MIT, copyright 2025 United States Writing Corportation, now at `6over3/zeroperl`), with `licenses/MIT-zeroperl.txt`.

Checked on 2026-11-20 through `go-exiftool-wasm` v0.6.0's `Command`: `-ver` prints `13.55`, and `-if 'print "Perl $^V\n"; 0'` on a test JPEG prints `Perl v5.16.3`, as v2's notice states.

The MIT and BSD Go modules (`go-scaled-jpeg` with Go's licence,
`go-image-phash`, `wuffs-wasm`, `go-exiftool-wasm`) stay in §15.2's table, as
`dev` treats its other Go dependencies.

## 3. `default.pgo`

The profile in v3 is `dev`'s (37 KB, before the image stack changed). v2 fixed
the generator (e66cd8f): with a restart after startup discovery, the server
writes one CPU profile per process, and the script kept only the last, so
discovery was missing from the profile.

- Carry v2's `scripts/generate_default_pgo.sh` and
  `generate_default_pgo-wait.sh`: wait for the post-discovery restart, retry
  login and dashboard polls, merge every CPU profile. Every log message they wait
  for exists in v3.
- **Port and data.** Playwright reads `http://localhost:8083` and `tmp/DB/sfpg.db`, Air's port and database, so the script runs with its defaults and Air stopped: with `SFPG_PGO_DELETE_DB=1` the script asks whether to delete an existing `tmp/DB` first (the recorded run answered yes), so startup discovery profiles a fresh database. Air rebuilds from the new database. Only the user stops and starts Air.
- The first build with the new profile recompiles every package. It is as heavy as a build with a cleared cache, so it runs only with the user's go-ahead, under `nice` with `-p 2`.

Done on 2026-11-19 from 6cd2169: discovery on a fresh `tmp/DB`, the restart, e2eweb, Playwright (105 passed, 2 skipped) and the cache batch load, four CPU profiles merged into an 896 KB `default.pgo`. `go build` with it passes.

## 4. Acceptance runs

The runs use `tmp/heap-watch/run-gallery.sh` on port 8084, the ZFS clones on
proxmox3 (rollback only, with the command already approved), and the gallery in
`/home/whgi/tmp/gallery` (16.12 M files). Its log markers (`tableswap completed
successfully`, `starting web server`, `process rss sample`) all exist in v3, and
its config has `restart-after-discovery: true`.

v2's measurements (version 0.17.137, peak RSS from the log's RSS samples, time
from the server log's start to the folder-index swap):

| Run   | Kind                         | Time      | Peak RSS |
| ----- | ---------------------------- | --------- | -------- |
| run22 | discovery only               | 13 m 02 s | 1.13 GB  |
| run23 | discovery only               | 18 m 09 s | 1.31 GB  |
| run24 | discovery only               | 11 m 54 s | 1.15 GB  |
| run25 | rollback, 124,485 thumbnails | 30 m 43 s | 1.47 GB  |
| run26 | rollback, 124,491 thumbnails | 30 m 56 s | 1.53 GB  |

Order, each run after the last finishes: thumbnail run with rollback, discovery
only, thumbnail run with rollback, discovery only. A discovery-only run follows a
thumbnail run, so every file already exists, as in run24. After the last run the
clones are rolled back.

The v3 binary is built from HEAD with the new `default.pgo` and copied over
`/home/whgi/tmp/gallery/sfpg`. The v2 binary there moves to
`tmp/heap-watch/sfpg-v2` first, so the baseline can be rerun.

**Pass:** each v3 run's time and peak RSS are within 10% of run24 (discovery
only) or run26 (thumbnails), and the row counts in the "File processing
completed" line match v2's. The corpus parity runs measured v3 18 to 25% above
v2 in peak RSS, so the memory check may fail. A failure is reported with the heap
profiles `watch.sh` captured, and the component stops there.

The results go in a Results section of this design and in §12.4.

## 5. Completion checks

For the branch as a whole, after the last commit:

- `deadcode . ./cmd/sfpg-go-dashboard` and
  `staticcheck -tests=false -checks U1000 ./...`, every finding outside the
  vendored packages resolved;
- `scripts/v3-check.sh`, `scripts/v3-parity.sh tmp/corpus`, `make test-all`,
  `make validate-templates`;
- the Sol review of the finish diff, with no unresolved MUST findings.

## Results

Runs on 2026-11-19 and 2026-11-20. Time runs from the server log's start (its
file name) to its `file_folder_index_new tableswap completed successfully`
line, the method that gives run24's 11 m 54 s and run26's 30 m 56 s. Peak RSS
is the largest `process rss sample` in the log. The 10% limits are upper
bounds, computed from the raw seconds and bytes and shown rounded: 13 m 05 s and
1.26 GB for discovery only, 34 m 02 s and 1.68 GB for thumbnails.

| Run     | Build    | Kind                         | Time      | Peak RSS | Result          |
| ------- | -------- | ---------------------------- | --------- | -------- | --------------- |
| v3-run1 | 931db3f  | rollback, 127,073 thumbnails | 33 m 18 s | 2.50 GB  | fails on memory |
| v3-run2 | 931db3f  | discovery only               | 9 m 19 s  | 1.13 GB  | passes          |
| v3-run3 | 931db3f  | rollback, 127,073 thumbnails | 30 m 51 s | 2.94 GB  | fails on memory |
| v3-run4 | 931db3f  | discovery only               | 9 m 10 s  | 1.13 GB  | passes          |
| v3-run5 | 68c1aa5* | rollback, 127,073 thumbnails | 43 m 05 s | 1.54 GB  | fails on time   |
| v3-run6 | 68c1aa5* | rollback, 127,073 thumbnails | 31 m 27 s | 1.62 GB  | passes          |

\* Built from the working tree on 931db3f with the decoder-retention change,
before it was committed as 68c1aa5; the Go code is the same.

- The memory failures came from Wuffs decoder churn, fixed in 68c1aa5
  (`decoder-retention.md`): 91 and 100 decoders made per run, 29 after the fix,
  as in v2.
- v3-run5 took 26 m 26 s from start to `File processing completed` and 16 m 39 s
  from there to the swap; v3-run6 took 23 m 57 s and 7 m 30 s. v3-run5's writer
  logged 2,403,863 disk-queue enqueues; v3-run3 logged 1,182,595 and v3-run6
  1,207,853. Why v3-run5's writes were slower was not established.
- The discovery-only runs make no thumbnails, so 68c1aa5 does not change them;
  v3-run2 and v3-run4 stand.
- Counts against v2:

  | Kind          | Found      | Existing   | Inserted | Skipped invalid |
  | ------------- | ---------- | ---------- | -------- | --------------- |
  | run24 (v2)    | 16,120,580 | 16,113,540 | 0        | 3,079           |
  | v3-run2, run4 | 16,123,168 | 16,116,128 | 0        | 7,040           |
  | run26 (v2)    | 16,120,586 | 15,989,055 | 124,491  | 3,079           |
  | v3 thumbnails | 16,123,168 | 15,989,055 | 127,073  | 7,040           |

  The gallery grew by 2,582 files after run26. Against run26, the thumbnail
  runs find and insert 2,582 more files. Against run24, the discovery-only runs
  find and count as existing 2,588 more (run24 itself found 6 fewer than
  run26). The 3,961 more skipped-invalid files are v2's known race. In the
  thumbnail runs, v2 (run26) and v3 failed the same 3,965 files, but v2 lost
  most of their `invalid_files` rows ("no folder for gallery path parent").
  In the discovery-only runs, v3 skips all 7,040 recorded rows without
  processing them, while v2 (run24) processed the 3,961 files whose rows it had
  lost and failed them again. The count check accepts
  these deltas, and v3-run2 and v3-run4 pass the time and RSS limits.

### Wuffs memory (2026-11-21 and 2026-11-22)

Thumbnail runs after the gallery grew to 132,045 new files (16,128,140
found), for the wuffs-wasm upgrades in `wuffs-memory.md`. Same method and
limits as above.

| Run      | wuffs-wasm               | Load at start | Time      | Peak RSS | Result          |
| -------- | ------------------------ | ------------- | --------- | -------- | --------------- |
| v3-run7  | v0.5.0                   | 11.65         | 33 m 42 s | 1.62 GB  | passes          |
| v3-run8  | v0.5.0                   | 0.67          | 31 m 43 s | 1.78 GB  | fails on memory |
| v3-run9  | v0.4.0 (v3-run6's build) | 0.24          | 36 m 09 s | 1.75 GB  | fails on both   |
| v3-run10 | v0.6.0                   | 3.81          | 33 m 25 s | 1.30 GB  | passes          |
| v3-run11 | v0.6.0                   | 0.76          | 32 m 04 s | 1.30 GB  | passes          |

- On v0.4.0 and v0.5.0, peak RSS varied by about 150 MB between runs of one
  build; the two v0.6.0 peaks are 1.6 MB apart.
- Every run inserted 132,045 files and logged 3,983 `ERROR` lines (v3-run9:
  3,984, the extra one an HTTP cache cleanup failure).
- v3-run9 ran beside other work on the host, which may explain its time.

### Native ExifTool (2026-11-22)

Thumbnail runs with ExifTool running natively (one `exiftool` 13.55 server)
instead of the embedded WASM pool, same workload, method and limits. Peak
tree RSS sums the server and its `exiftool` child, sampled every 2 s.

| Run      | ExifTool | Load at start | Time      | Peak RSS (server log) | Peak tree RSS | Result |
| -------- | -------- | ------------- | --------- | --------------------- | ------------- | ------ |
| v3-run10 | WASM     | 3.81          | 33 m 25 s | 1.30 GB               |               | passes |
| v3-run11 | WASM     | 0.76          | 32 m 04 s | 1.30 GB               |               | passes |
| v3-run12 | native   | 3.59          | 31 m 12 s | 1.04 GB               | 1.09 GB       | passes |
| v3-run13 | native   | 0.90          | 30 m 18 s | 1.05 GB               | 1.09 GB       | passes |

- Time to `File processing completed`: 23 m 50 s and 22 m 50 s native, against
  24 m 57 s and 25 m 04 s with WASM.
- Every run inserted 132,045 files; v3-run12 logged 3,983 `ERROR` lines and
  v3-run13 3,984 (the extra one an HTTP cache cleanup failure).

## Approvals needed

- No new Go package, rename, exported API change or `go.mod` change.
- New repository files: `THIRD_PARTY_NOTICES.md`, `licenses/*.txt`.

## Decisions

1. **Notices: carry v2's and add zeroperl and Wuffs.** Approved; Wuffs under its MIT option, confirmed by the user.
2. **PGO: you stop Air, the script runs with its defaults and `SFPG_PGO_DELETE_DB=1`, you restart Air.** Approved and done.
3. **Acceptance: two runs of each kind, pass within 10% of run24 and run26 on
   time and peak RSS.** Approved: v2's own discovery-only runs spread 16% in
   peak RSS, so one run or a tighter margin judges noise.
4. **The v3 binary replaces `/home/whgi/tmp/gallery/sfpg`, with the v2 binary
   kept in `tmp/heap-watch/`.** Approved: `run-gallery.sh` runs `./sfpg` in
   that directory, and writing there is the one exception to working only under
   `./tmp` this needs.
5. **Thumbnail gate: v3-run6 is the passing run on the 68c1aa5 code.** Accepted by the
   user on 2026-11-20, with v3-run5's time failure recorded as an exception.
6. **Dead code inherited from `dev`.** Accepted by the user on 2026-11-20 as a
   known exception: `deadcode` and U1000 report 41 findings outside the vendored
   packages, all present in `dev`; v3 adds none.
7. **PGO script review findings rejected.** The user rejected on 2026-11-20 the
   review's findings on the no-restart wait, the restart detection and skipped
   CPU profiles: the script is v2's, carried unchanged as §3 requires, and none
   of those paths affected the run that produced `default.pgo`.
8. **Release assets carry the notices.** Requested by the user on 2026-11-21:
   the release workflow's publish job zips `LICENSE`, `THIRD_PARTY_NOTICES.md`
   and `licenses/` into `sfpg-notices.zip` and attaches it beside the binaries.
