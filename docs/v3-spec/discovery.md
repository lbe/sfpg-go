# DISCOVERY component spec (v2 -> v3)

Source = `refactorv2/image-stack` (HEAD, includes post-delivery 455daef and 0d9ae27). Paths are relative to the repo root. `file:line` refers to that branch. "dev" = branch `dev`. Evidence = read from code; inference is marked "(inference)".

Scope note: in dev there is no `internal/discovery`, `internal/storagepayload` or `internal/server/database/storage`; all three are NEW packages in v2 (R1 counts them as additions). In dev the walk, worker loop, counters and `TriggerDiscovery` lived in `internal/server/files/{walker,processor,discovery_modification,discovery_work}.go`, `internal/server/server.go`, `app_startup.go`, `subsystem_manager.go`.

## 1. Behavior

### 1.1 Overall run (`discovery.Service.Run`, `internal/discovery/service.go:154-252`)

Input: effective settings (`ImagesDir`, `NormalizedImagesDir`, `RemoveImagesPrefix`, `MaxWorkers`, `MinIdle`, `MaxIdleTime`) (service.go:33-38). Output: `Result{Admitted, Snapshot}` (service.go:64-67) and an error. Entry point in the app: `App.TriggerDiscovery` (internal/server/server.go:392-396) which runs `ensureDiscovery(...).Run(app.getCtx())`. It deliberately ignores the caller's ctx (`_ = ctx`, server.go:393; test `TestTriggerDiscovery_CallerCancellationDoesNotCancelAdmittedRun`). Dev behaved the same (dev server.go:427 `lifecycleCtx := app.getCtx()`).

Sequence of one run (all service.go):

1. Under `s.mu`: if `closed` return error `"discovery closed"` (156-159). If `activity.Running` CAS false->true fails, return `Result{Admitted:false, Snapshot}` and nil error, with no log (160-163). NEW in v2: dev logged `"discovery already in flight"` and returned nil (dev server.go:396-399).
2. Derive `runCtx` from the caller ctx and cancel it when the service lifetime ctx ends (`context.AfterFunc(s.ctx, cancel)`, 164-165).
3. `backlogBlocked = senders != 0 || InFlight != 0 || Queue.Len() != 0` computed under `counters.workMu` (168-172). Only when not blocked: `counters.Reset()` (zeroes TotalFound, AlreadyExisting, NewlyInserted, SkippedInvalid, InFlight, status.go:22-28) and `s.runContext = runCtx` (173-176). Dev reset unconditionally after winning the CAS (dev server.go:406); v2 does not reset when blocked.
4. `OnActivity(+1)` / deferred `OnActivity(-1)` (195-198); the server wires it to `GalleryStats().markRunning` (server.go:408-410 `configureDiscoveryAdapter`).
5. `State.SetActive(runCtx,"discovery",true)`; error logged `"failed to set discovery active in module_state"` and ignored (199-202). Deferred `SetActive(context.Background(),"discovery",false)`; error logged `"failed to set discovery inactive in module_state"` (203-207). Same as dev (dev server.go:411-421). Writes the `module_state` row (see 1.7).
6. Deferred: `cancel()` then `waitForProcessingStop()` (209-212), which polls every 100 ms until `InFlight == 0` (256-268). Purpose: a canceled run stays admitted until in-flight processing returns. NEW in v2.
7. `Override(runCtx)` (test seam, `server.go:399-404` `testSeams.TriggerDiscovery`): if it returns handled=true, Run returns its error (213-217). Dev had the same seam inline (dev server.go:423-425).
8. If `backlogBlocked`: error `"discovery walk refused: queue backlog or active senders must be empty before walk"` (218-220). Same text as dev (dev server.go:431).
9. If `runCtx.Err() != nil` return it (221-223).
10. `StartWorkers(nil,nil)` (once; see 1.4) then new `WalkPathStore`, remembered in `s.arenas` (224-228). Walk via `WalkImageDir` and `walkers.Wait()` (229-231).
11. `s.Wait(runCtx)` (232): poll every 100 ms until `Senders==0 && Queued==0 && InFlight==0`, then `WaitWrites(ctx)` (271-291). `WaitWrites` = `Store.WaitWrites`: poll every 100 ms until `writeBatcher.PendingCount()==0` or ctx done, nil store/batcher returns nil (internal/server/database/storage/discovery.go:169-186). Dev did the same with `waitForFileProcessingDrain` including `PendingWriteCount()==0` (dev app_startup.go:90-111).
12. Re-check `runCtx.Err()` (235), `arena.Reset()` and drop from `s.arenas` (238-241). Dev: `walkDeps.WalkPathStore.Reset()` after drain (dev server.go:453).
13. `Persist(runCtx)`: `State.SaveFileProcessing(ctx,"discovery",FileProcessingRunState{TotalFound,AlreadyExisting,NewlyInserted,SkippedInvalid})`; failure logged `"failed to persist file processing stats"` and ignored (242-244, 293-300). InFlight is not persisted (storagepayload/run_state.go:3-4). Same as dev.
14. Re-check ctx (245-247), then `FinalizeIndex(runCtx)` and return its error (248-251). `FinalizeIndex` = `Store.FinalizeIndex`: `ctx.Err()`; nil store/pools/batcher returns `ErrFolderIndexRebuild`-wrapped error `"storage resources unavailable"`; else `RebuildFileFolderIndex(ctx, rwPool, roPool, NewUnifiedWriteBatcher(...))` (storage/drain.go:9-17). The app adds a wrapper that returns nil with `slog.Error("cannot rebuild file_folder_index: RW pool is nil")` when `app.dbRwPool==nil` (server.go:420-423 area) and honors `testSeams.RebuildFileFolderIndex`. Dev logic equals this (dev server.go:462-485). Startup caller treats `files.ErrFolderIndexRebuild` as fatal (app_startup.go ~305, same as dev).
15. Errors: `ctx` cancellation at any wait skips persist and finalize (232-247). A persist error never fails the run. A SetActive error never fails the run. A finalize error is returned.

### 1.2 Walk (`discovery.WalkImageDir`, internal/discovery/walker.go:147-217)

- Logs `"walkImageDir for all images Started"` (`dir`) and `"walkImageDir for all images Ended"` (148, 216). Same strings as dev.
- `Wg.Add(1)`; `QSendersActive.Add(1)`/`Add(-1)` bracket the walk (149, 161, 214). Without `CatalogLoader`, logs error `"WalkImageDir requires CatalogLoader or CatalogROPool"` and returns without walking and without incrementing senders (152-155). In v2 the message still names CatalogROPool, which no longer exists in this package.
- Image filter: `(?i)(?:jpe?g|gif|png)$` via `WithBasenameInclude`, `WithSizeNotZero`, `WithWalkPathStore`, `WithDirEntCatalog(loader, DiscoveryDirEntModifiedWithStats(Stats))` (159, 169-178). Same set as dev. Parallelism is the parallelwalkdir default `runtime.GOMAXPROCS(0)` (comment 137-139); dirCh capacity `workers*1024` (parallelwalkdir.go:214).
- `errgroup.WithContext(deps.Ctx)`; one goroutine maps each `ReportedFile` to `DiscoveryPathWork{Path, MtimeUnix, SizeBytes}` and enqueues with backpressure; on enqueue failure logs `"failed to enqueue file"`, drains the results channel, returns error (183-197). Second goroutine logs walk errors at Warn `"Error during directory walk"` (199-204). `eg.Wait()` error logged `"Error during parallel directory walk"` (206-208); an enqueue failure never fails the run (the run proceeds to Wait; (inference) a closed queue would make later steps wait on drained work).
- `EnqueueWithBackpressure` (225-246): on `queue.ErrQueueFull` poll every 100 ms until ctx done; any other error returns. (inference) The production backlog never returns ErrQueueFull (storage/discovery.go:60-71 returns only `ErrClosedQueue` or dque errors), so this branch only serves the generic `queue.Queue`.

### 1.3 Walk-time catalog decision (skip or process)

Per directory the walker calls `GetDirEntMapFunc(ctx, reportedDir)` before reading the dir (parallelwalkdir.go:31-35 doc). Loader = `Store.NewCatalogDirEntMapFunc(normalizedImagesDir, removePrefix)` (storage/catalog.go:117-129): maps the reported dir to a gallery-relative folder via `removePrefix`, then `CatalogDirectory` (78-107):

- nil store/pool returns empty map; `GetFolderIDByPath` ErrNoRows returns empty map (no error); otherwise reads `ListDiscoveryFilesByFolderID` and `ListDiscoveryInvalidByFolderID` through a pooled RO connection (`defer roPool.Put`).
- Keys are `parallelwalkdir.HashPathBytes(basename)` (FNV-1a 128). Files: `FileIDValid=true`, mtime/size/md5 validity from `files.mtime`, `files.size_bytes`, `files.md5` columns. Invalid rows: key = `path.Base(invalid_path)`, merged onto an existing file entry or standalone (43-74). Collisions: last row wins (tests `..._duplicateBasenameLastWins`, `..._mergeCollision`).
  Decision `ModificationDecision` (walker.go:54-74), order matters:

1. not in map -> Process.
2. `InvalidPathValid` and invalid mtime/size == walk mtime (unix seconds)/size -> SkipUnchangedInvalid.
3. `FileIDValid && FileMtimeValid && mtime equal && FileSizeValid && size equal && FileMD5Valid` -> SkipUnchangedValid.
4. else Process.
   Counting (`DirEntModifiedCore`, 79-111): `TotalFound+=1` for every candidate; `AlreadyExisting+=1` on SkipUnchangedValid; `SkippedInvalid+=1` on SkipUnchangedInvalid. nil `FileInfo` returns an error (80-82). Identical to dev (dev files/discovery_modification.go) except moved. Skipped files never reach the queue. Collisions in FNV-128 may mis-skip (accepted, parallelwalkdir.go:34-36).

### 1.4 Workers (`internal/discovery/worker.go`, `service.go:110-130`)

- `StartWorkers(processor, onInserted)` runs once (`sync.Once`); a goroutine runs `pool.StartWorkerPool(newPoolFunc(...), Queue.Len)` and closes `workersDone` when it returns; nothing starts if Queue or processor is nil (110-126). App calls `StartPool` -> `s.StartWorkers(processor,onFileInserted); go func(){defer close(poolDone); s.WaitWorkers()}()` (subsystem_manager.go ~205-208). v2 now starts workers BEFORE the startup discovery goroutine (app_startup.go diff: "Worker pool startup" moved above `if runDiscovery`). Dev started workers after.
- `Service.Run` also calls `StartWorkers(nil,nil)` (224) so a run works even if the app has not started the pool.
- `WaitWorkers` = StartWorkers(nil,nil) then wait on `workersDone` (130). Do not use it from Close (0d9ae27, below).
- Pool: `workerpool.NewPool(life, MaxWorkers, MinIdle, MaxIdleTime)` (service.go:103). Worker limits come from `SubsystemManager.Start` (subsystem_manager.go:170-188): `maxWorkers` = arg, overridden by `cfg.WorkerPoolMax>0`; `minIdle` = arg, overridden by `cfg.WorkerPoolMinIdle>0`; if `maxWorkers==0` -> `workerpool.DefaultMaxWorkers()`; then `maxWorkers = memlimit.Apply(maxWorkers, metadata.PoolSize())` (NEW in v2; memlimit/metadata are other components); `maxIdleTime` = 10 s default, `cfg.WorkerPoolMaxIdleTime` when cfg != nil. In `ensureDiscovery` the settings are overwritten from an existing `m.pool` when present (subsystem_manager.go ~326-330).
- `DefaultMaxWorkers()` (workerpool.go:~86-97, NEW exported): `numCPU>4 -> numCPU-2`; `2<numCPU<=4 -> 2`; else 1. Dev had the same logic inline in `getMinMaxPoolWorkers`; max==0 selects it, min is never auto-set.
- Pool loop (workerpool.go): initial `MinWorkers` workers started with `time.Sleep(rand(50..149) ms)` stagger; `MonitorPool` ticks every 100 ms and scales up via `shouldScaleUp(queueLen, running, max)`; `ShouldIStop(queueLen+1)`: false when running<=MinWorkers, false when queueLen+1 > running, false while `TimeSinceLastCompletion < MaxIdleTime`, else true. `StartWorkerPool` blocks until all workers and the monitor return, logs `"workerpool Go returned error"` on error. Unchanged from dev apart from removed DB pool params and removed counters (see 3).
- Per-worker loop (`runPoolWorkerWithProcessor`, worker.go:78-221):
  - If processor implements `WorkerProcessorFactory`, create a worker-local processor + cleanup (95-99) (NEW; `fileProcessor.NewWorkerProcessor` gives each worker its own `thumbnail.Workspace`, files/service.go:~223-236).
  - Loop: stop on ctx done; `wc.ShouldIStop(queueLength()+1)`; `wc.AddSubmitted()`.
  - Under `counters.workMu`: pick `workCtx` (= run context when a run is active, else service ctx via `processingContextLocked`, service.go:358-363); if `workCtx.Err()!=nil` sleep 100 ms and retry (no dequeue); else `Dequeue()` and, if it returned an item, `InFlight+=1`, all inside the lock (119-145). Purpose: a canceled run cannot claim work after its join observed zero. NEW in v2 (dev incremented InFlight after dequeue without a lock).
  - Dequeue errors: `ErrEmptyQueue` -> `time.Sleep(100ms)`; `ErrClosedQueue` -> worker returns nil; other -> `slog.Error("failed to dequeue file")` and worker returns nil (146-156). Same as dev.
  - `removePrefix(normalizedImagesDir, fullPath)` failure: `InFlight-=1`, log `"invalid file path detected"`, `AddFailed`, `AddCompleted`, continue (158-168). Same as dev.
  - Builds `storagepayload.File{Path: relPath, File: gallerydb.File{Mtime, SizeBytes (walk values, Valid)}}` (170-176): walk mtime/size are copied, workers do not re-stat for the modification check (same as dev).
  - `processor.ProcessDiscoveryFile(workCtx, file)` (178). On error: `slog.Error("failed to process file")`; if the error is not context.Canceled/DeadlineExceeded and `workCtx.Err()==nil`: `RecordInvalidFileFromPath` (stat the full path; `RecordInvalidFile(ctx, relPath, mtime, size, reason, folderID=0)`) then `SkippedInvalid+=1`; record failure logged `"record invalid file"`. `InFlight-=1`, `AddFailed`, `AddCompleted` (180-195). NEW in v2: canceled/deadline errors are not recorded as invalid (dev recorded every failure, dev processor.go:~250-260; test `TestV2CORECanceledProcessingIsNotInvalid`).
  - Success: `NewlyInserted+=1` (counted before submit, "walk-only gate", 197-200). `SubmitFileForWrite(file)`; on error log `"submit file for write"`, return thumbnail buffer via `thumbnail.PutBytesBuffer`, `AddFailed`; on success `AddSuccessful` and `onFileInserted(size)` (-> `GalleryStats().addFile`). `InFlight-=1`, `AddCompleted` (202-221). Same as dev.
- Failure reason categories (`CategorizeProcessError`, worker.go:34-53; substring match on `err.Error()`, first match wins): `non-image`, `decode` (contains "decode" or "DecodeConfig"), `thumbnail`, `exif` (contains "EXIF"/"exif"), `open` (contains "open"/"Open"), else `unknown`; nil -> `unknown`. v2 removed `jpeg-markers` (455daef). Dev had the same function lowercase (dev processor.go:~58) plus `jpeg-markers`.
- Invalid recording path: `fileProcessor.RecordInvalidFile` -> `DiscoveryStorageClient.RecordInvalid(ctx, galleryPath, mtime, size, reason)` (files/service.go:165-168, discovery_storage_client.go) -> `invalid_files` row (columns `invalid_path`, `invalid_mtime`, `invalid_size`, `reason`, folder via parent folder id; written by storage writer, outside this component's file list). A missing parent folder defers the row to a later run (test `TestServiceIntegration_InvalidMissingParentWaitsForLaterRun`; mechanism is in storage writer, not read here).

### 1.5 Malformed JPEGs (post-delivery 455daef, v2 behavior)

`ValidateJpegMarkers` pre-filter and the `jpeg-markers` category are gone. A malformed JPEG now fails in the processing pipeline (ExifTool, or `image.DecodeConfig`, files/processor.go:~92-106) and is recorded in `invalid_files` with the category from `CategorizeProcessError`. Pipeline order is: open, read source, mime sniff (`non-image`), `extractFileMetadata` (ExifTool; wrapped `"failed to extract EXIF data: ..."` -> reason `exif` unless the text also contains "decode"), `image.DecodeConfig` (`"failed to decode image config: ..."` -> `decode`), thumbnail (`"failed to generate thumbnail: ..."` -> `thumbnail`). The commit message and the brief say "decode"; which reason a given file gets depends on which stage rejects it and on string matching. UNVERIFIED which stage rejects each kind of malformed JPEG; run the corpus.

### 1.6 Close and lifecycle (service.go:323-356, 0d9ae27)

- `New` creates `life, cancel := context.WithCancel(ctx)` and the pool; it opens no DB resources (91-104).
- `Close` is once-only (`closeOnce`): set `closed`, cancel lifetime ctx and the active run ctx, wait for `runDone`, then `workersOnce.Do(func(){close(workersDone)})` so an unstarted pool is marked done WITHOUT starting it (0d9ae27, test `TestService_CloseWithoutStartedWorkersDoesNotStartPool`), wait `workersDone`, reset arenas, then `CloseProcessor()` (set only when the app owns the file processor) and return its error. Does not close the backlog queue.
- Shutdown order in app: `SubsystemManager.Shutdown` closes preload, `discovery.Close()`, the file processor if not owned, then `dbComponent.CloseDiscoveryBacklog()` (subsystem_manager.go:~213-235).
- `Hydrate(ctx)` (302-321): when idle, loads `FileProcessingRunState` from module_state and stores into the live counters. Called from startup when discovery is skipped (app_startup.go:263-269) so the dashboard shows last-run totals. Same effect as dev's hydrate (dev stats hydrate in subsystem_manager).
- Snapshot (`Snapshot`, status.go:31-38): Active, Senders, Queued, TotalFound, AlreadyExisting, NewlyInserted, SkippedInvalid, InFlight, Workers (`workerpool.Stats`). Consumed by `discoveryMetricsAdapter.GetStats()` for the dashboard `metrics.FileProcessingMetrics` (subsystem_manager.go:~390-395) and by `WireMetrics` (`collector.SetWorkerPool(m.pool)`).

### 1.7 Discovery backlog queue (disk)

- Directory: `<dir of main DB>/discovery-dque` (infrastructure_pool.go:86-89; `storage.DefaultDiscoveryDQueDir` duplicates this, storage/discovery.go:162-167). Same dir name as dev.
- Startup: `ResetDiscoveryBacklogAt` closes any open backlog, `os.RemoveAll(dir)` (wipe), then opens a fresh dque named `"discovery"`, `discoveryDQueItemsPerSegment = 1000`, `TurboOn()`; MkdirAll 0o755 (storage/discovery.go:18, 40-56, 111-134). Empty/whitespace dir -> error `"discovery dque: empty parent dir"`. App panics (`panic("main")`) on empty dir or open failure (subsystem_manager.go:117-128). Dev: same wipe+open in subsystem_manager (dev subsystem_manager.go:88-100).
- Semantics: `Dequeue` returns an item even if dque reports a cleanup error (logged `"discovery dque: item dequeued despite cleanup error; continuing with item"`); `dque.ErrEmpty` -> `queue.ErrEmptyQueue`; `dque.ErrQueueClosed` -> `queue.ErrClosedQueue`. `Close` swallows ErrQueueClosed, logs `"discovery dque: close failed"` otherwise (storage/discovery.go:73-109). Same as dev.
- Persistence format: `DiscoveryPathWork.GobEncode`: 8 B big-endian mtime, 8 B size, 4 B path length, path bytes (20-byte header); decode errors `"discovery path work: short gob payload (%d bytes)"` and `"...truncated path in gob payload"`; path is cloned on decode (storagepayload/discovery.go:17-42). Byte-identical to dev (dev files/discovery_work.go). Fixture `storagepayload/testdata/pre-db02-discovery.gob` (45 bytes) pins it. This is a wire format: keep.

### 1.8 parallelwalkdir changes (internal/parallelwalkdir/parallelwalkdir.go)

Behavior of the walk output is unchanged. NEW in v2:

- Coordination rewritten: one `outstanding` directory counter incremented before enqueue and decremented in `finish`, guarded by `idleMu`/`idleCond`, replacing `pending` WaitGroup + `processing`/`feeding` atomics + `pendingMu` (diff 217-340). Fixes a window where a directory held by the feeder but not yet counted let the coordinator decide the walk was complete (test `TestParallelWalk_FeederHeldDirectoryRemainsOutstanding`).
- Cancel: `context.AfterFunc(w.ctx, ...)` wakes the coordinator; feeder has its own `feederCtx`; after cancel queued work is dequeued and `finish`ed.
- Read buffers: after each directory a worker calls `dirBufs.trim()`; if `len(entries) > 4*256 (=1024)` or `len(nameBuf) > 4*(64<<10) (=262144)`, both are replaced by initial-size buffers (`initialReadDirEntries=256`, `initialReadDirNameBytes=64<<10`, `retainReadDirEntries`, `retainReadDirNameBytes`, lines 74-82, 1127-1134). Purpose: idle workers do not pin large listings (memory limit).
- Documented contract change: a `Walker` is single-use ("visited set is not cleared between calls, so create a new Walker per walk", doc at `ParallelWalk`). Dev doc said "may be reused" (dev doc only; code unchanged here).
- `Walker.testSeams.AfterDequeue/BeforeWait` added (parallelwalkdir.go:~187-190, 255, 307) only for the completion test.
- Doc-only: `fnv128.go` comment punctuation.
  Limits unchanged: `readDirScratchBytes=8192`, `maxReadDirEntries=1<<20`, `maxReadDirNameBytes=64<<20`, `results` channel cap 100, dirCh `workers*1024`, `dirQueue = queue.NewQueue(64)`.

### 1.9 Config keys and flags touched

`cfg.WorkerPoolMax`, `cfg.WorkerPoolMinIdle`, `cfg.WorkerPoolMaxIdleTime` (read in `SubsystemManager.Start`), `cfg.RunFileDiscovery` (startup gate, `discovery_effective_enabled` log), `cfg.RestartAfterDiscovery`. No new keys in this component. Constants: backoff/poll interval 100 ms in the worker loop, `Wait`, `waitForProcessingStop`, `WaitWrites`, `EnqueueWithBackpressure`, `MonitorPool`; segment size 1000; default idle time 10 s.

### 1.10 Log lines operators/tests depend on

`walkImageDir for all images Started|Ended`; `File processing completed` (found/existing/inserted/skipped_invalid, app_startup.go:360, emitted by the monitor goroutine, unchanged); `failed to process file`; `record invalid file`; `invalid file path detected`; `failed to dequeue file`; `submit file for write`; `failed to enqueue file`; `Error during directory walk`; `failed to persist file processing stats`; `failed to set discovery active|inactive in module_state`; `discovery walk refused: ...` (error text); the two dque lines in 1.7. REMOVED vs dev: `discovery already in flight`.

## 2. Approved changes versus dev and other differences

Approved:

- pHash: a worker stores `file.File.Phash` produced by `workspace.Generate` (processor.go:~108-120); this component only moves the value to the writer. No discovery code changes beyond that.
- Wuffs decoding: `thumbnail.NewWorkspace` per worker (`WorkerProcessorFactory`) replaces per-file allocation; `ReadSource`, `Generate`. Discovery owns the per-worker workspace lifetime (worker.go:95-99).
- ExifTool metadata and IPTC: `extractFileMetadata` stage in the worker pipeline (failure -> reason `exif`); fed by `MetadataExtractor` borrowed by worker-local processors. Not discovery code, but its failure shapes `invalid_files` reasons.
- Malformed JPEG -> decode failure: see 1.5.

Unapproved change: needs a decision (these alter observable behavior and are not in R4's list):

1. Concurrent `Run` is a silent no-op returning `Admitted:false`; dev logged `"discovery already in flight"` (service.go:160-163).
2. `counters.Reset()` is skipped when the backlog/senders/InFlight are non-zero (service.go:173-176); dev always reset after the CAS.
3. Canceled/deadline errors are not recorded in `invalid_files` and do not bump `SkippedInvalid` (worker.go:182). Dev recorded every failure.
4. Run is joined to in-flight processing on cancel (`waitForProcessingStop`), and workers do not dequeue when the run ctx is canceled (worker.go:119-145). Dev could dequeue after cancel.
5. Worker pool starts before the startup discovery goroutine (app_startup.go diff).
6. Per-worker `Processor` instances (`NewWorkerProcessor`) instead of one shared processor (resource/perf change; approved only as part of Wuffs/pHash (inference)).
7. `memlimit.Apply(maxWorkers, metadata.PoolSize())` caps worker count (subsystem_manager.go:183; other component).
8. Metrics: `workerpool.Stats` lost `WaitingTasks` and `DroppedTasks` JSON fields (`waiting_tasks`, `dropped_tasks`) (workerpool.go diff). Check whether the dashboard consumes them.
9. Walk read buffers are trimmed per directory (1.8); memory behavior change.
10. Error message texts: `"discovery closed"` new; `"WalkImageDir requires CatalogLoader or CatalogROPool"` changed from `CatalogROPool`.

## 3. Exported API delta versus dev

Packages new in v2 (all exported identifiers):

- `internal/discovery`: types `Backlog`, `ModuleState`, `Settings`, `Activity`, `Dependencies`, `Result`, `Service`, `Counters`, `Snapshot`, `Processor`, `WorkerProcessorFactory`, `DiscoveryWalkOutcome`, `DiscoveryCompareInput`, `WalkDeps`; consts `DiscoveryWalkProcess`, `DiscoveryWalkSkipUnchangedValid`, `DiscoveryWalkSkipUnchangedInvalid`; funcs `New`, `ModificationDecision`, `DirEntModifiedCore`, `DiscoveryDirEntModifiedWithStats`, `WalkImageDir`, `DrainReportedFiles`, `EnqueueWithBackpressure`, `CategorizeProcessError`, `RecordInvalidFileFromPath`, `NewPoolFunc`; methods `(*Service).WorkerPool, StartWorkers, WaitWorkers, Snapshot, Run, Wait, Persist, Hydrate, Close`; `(*Counters).Reset`.
- `internal/storagepayload` (discovery part): `DiscoveryPathWork{Path,MtimeUnix,SizeBytes}` with `GobEncode/GobDecode`; `FileProcessingRunState{TotalFound,AlreadyExisting,NewlyInserted,SkippedInvalid uint64}`. (Other storagepayload files belong to other components.)
- `internal/server/database/storage` (discovery part): `DiscoveryBacklog`, `MapDiscoveryDequeueResult`, `OpenDiscoveryBacklog`, `(*DiscoveryBacklog).Enqueue/Dequeue/Len/IsEmpty/Close`, `(*Store).ResetDiscoveryBacklog/DiscoveryBacklog/CloseDiscoveryBacklog/DiscoveryDQueDir/WaitWrites/FinalizeIndex/CatalogDirectory/NewCatalogDirEntMapFunc/SetROPool`, `DefaultDiscoveryDQueDir`, `GalleryDirPrefixFromReported`, `BuildDirEntMapFromRows`.

Changed packages that exist in dev:

- `internal/workerpool`: ADDED `DefaultMaxWorkers() int`. CHANGED `PoolFunc` signature (removed `dbRoPool, dbRwPool dbconnpool.ConnectionPool`), `(*Pool).MonitorPool` (removed the two pool params), `(*Pool).StartWorkerPool` (removed the two pool params). REMOVED fields `PoolStats.WaitingTasks`, `PoolStats.DroppedTasks`, `Stats.WaitingTasks`, `Stats.DroppedTasks` (JSON `waiting_tasks`, `dropped_tasks`). Package no longer imports `dbconnpool`. All of this needs R2 approval.
- `internal/parallelwalkdir`: no exported API change (`Walker.testSeams` is unexported; added unexported consts and `readDirBuffers.trim`).
- `internal/server/files` (dev package): REMOVED type `DiscoveryPathWork` as a struct (now alias of `storagepayload.DiscoveryPathWork`, same name); REMOVED `GobEncode/GobDecode` on it (moved); ADDED `WalkDeps.CatalogLoader`; CHANGED `DiscoveryWalkOutcome`, `DiscoveryWalkProcess`, `DiscoveryWalkSkipUnchangedValid`, `DiscoveryWalkSkipUnchangedInvalid`, `DiscoveryCompareInput` to aliases of `discovery.*`; ADDED `CatalogLoaderFromStore`, `DiscoveryStorageClient` (new interface); CHANGED `ProcessingStats` to `struct{ discovery.Counters }` (was a struct with 5 atomics + `Reset`, `GetStats`); CHANGED `NewPoolFuncWithProcessor` to delegate; `NewDiscoveryDirEntMapFunc` kept with changed internals; `WalkImageDir` kept as adapter.
- `internal/server` (`discovery_dque.go`): unexported only; `discoveryDQueAdapter` now wraps `*storage.DiscoveryBacklog`.

## 4. File verdicts

Verdict key: COPY / REWRITE / DROP. I checked R1-R28 per file for the files marked COPY.

### internal/discovery

- `service.go` REWRITE. Violations: R18 `ctx`, `runContext` stored in struct (75, 83); R9 `Dependencies.Override` "retain its existing whole-run test seam" (59-60, 213-217); R10 `Dependencies` struct of 5 func fields (`WaitWrites`, `FinalizeIndex`, `OnActivity`, `OnFileInserted`, `CloseProcessor`) plus nil-defaulting in `New` (94-103), `StartWorkers(processor, onInserted)` with override args "for a compatibility adapter" (109-117); R15/R19 hand-rolled lifecycle flags (`closed`, `closeOnce`, `workersOnce`, `workersDone`, `runDone`, `runCancel`, `backlogBlocked`, `arenas`); R19 polling `time.Ticker` waits (256-291) where a sync primitive (`sync.WaitGroup`, `sync.Cond` or errgroup) joins; R20 repeated `ctx.Err()` (221, 235, 245); R5/R10 `WorkerPool()` "compatibility metrics adapters" (106-107); R23 comments reference compatibility/test seams. Keep: all behavior in 1.1, 1.6 incl. 0d9ae27 semantics, order of finalize after persist, Wait-then-WaitWrites, snapshot fields.
- `status.go` REWRITE (small). R15/R19: `Counters.workMu` embedded inside a struct of exported atomics and locked from other files (worker.go:120, service.go:144); R21: `Snapshot.Workers` and `Snapshot.Senders/Queued/Active` are used only by the dashboard adapter and by Wait; keep only what the dashboard and Wait read. Keep: 4 persisted counters + `InFlight`, `Reset`.
- `walker.go` REWRITE. R6 `DiscoveryCompareInput` (12 fields) duplicates `parallelwalkdir.DirEntState`; R15 `*Valid` booleans; R12/R20 `ModificationDecision` returns an error that is always nil (54-74) and `DirEntModifiedCore` propagates it; R22 stutter `discovery.DiscoveryWalkOutcome`, `DiscoveryDirEntModifiedWithStats`; R19 `QSendersActive atomic.Int64` + `Wg` pair in `WalkDeps` (hand-rolled wait group, mutated `WalkPathStore` out-param, 121-135); R14 regexp compiled per call (159); R7/R20 `EnqueueWithBackpressure` polling for an error the production queue never returns (225-246); R5 `DrainReportedFiles` exported but used only locally (220); R23 comment references `TriggerDiscovery`/tests. Keep: the three-way decision and its order, counter updates, walk log lines, errgroup cancel semantics.
- `worker.go` REWRITE. R16 imports `database/sql` and `gallerydb` and builds `storagepayload.File{File: gallerydb.File{Mtime: sql.NullInt64...}}` (170-176) (depguard rule for `discovery`); R10 constructor chain `NewPoolFunc` -> `newPoolFunc` -> `runPoolWorkerWithProcessor` with 11 positional args (68-87); R8/R10 optional `WorkerProcessorFactory` via type assertion (29-31, 95-99) and a 3-method `Processor` interface returning `*storagepayload.File`; R12 `CategorizeProcessError` matches error strings (34-53) (use typed/sentinel errors but keep the category strings); R20 `stats != nil` checks (119-189), `ctx` select at loop head plus `ShouldIStop`; R9 `processingContext func()`; R5 `NewPoolFunc` exported only so `files` can call it. Keep: workMu-paired dequeue/InFlight semantics, cancellation not recorded as invalid, per-worker workspace, category strings, log lines, counter points.
- `dependency_test.go` DROP (enforces import bans that `depguard` now covers).
- `service_test.go` KEEP the 7 behavior tests (empty/second run, admission keeps counters, canceled drain skips completion, cancellation holds admission, canceled backlog cannot resume, Close joins processing, Close without started workers); REWRITE helpers if the Dependencies shape changes. They test Service API (R26 ok).
- `lifecycle_integration_test.go`, `service_integration_test.go`, `workspace_integration_test.go` KEEP the 4+2+1 behavior tests; review for `Override`/seam uses.
- `metadata_integration_test.go` (3), `wuffs_integration_test.go` (1), `hash_cutover_integration_test.go` (1) KEEP assertions that belong to approved features (metadata/Wuffs/hash) in the discovery flow; they may belong to the other components' specs.
- `metadata_lifecycle_test.go` (1: close processor after run), `worker_workspace_test.go` (3: factory, canceled not invalid, active cancellation) KEEP, behavior-level.

### internal/parallelwalkdir

- `parallelwalkdir.go` REWRITE (targeted): keep the `outstanding`-counter coordination and `trim()`; R9 remove `Walker.testSeams` (AfterDequeue/BeforeWait, 187-190, 255, 307); the pre-existing `dirChCapacity`/`WithDirChCapacity` "tests only" option is dev code, leave. In v3 this file starts from dev and applies the coordination fix and trim as a minimal change (R6, R10 in-place change).
- `fnv128.go` DROP (comment-only change; keep dev's file).
- `completion_test.go` REWRITE: asserts a real race (feeder-held directory) but only through the production seam hooks (R9/R26); a v3 test needs another way to hold the feeder (for example a blocking `WithValidationFunc`/slow catalog loader) or be dropped if not expressible.
- `parallelwalkdir_test.go` (+91 lines): KEEP `TestReadDirBuffersTrim_*` (3) and `TestParallelWalk_GrownReadDirBuffersTrimmedBetweenDirs` (behavior: memory retention) but review: `TestReadDirBuffersTrim_*` exercise the unexported `trim` (R26 SHOULD; fold into the walk-level test).

### internal/workerpool

- `workerpool.go` REWRITE as a dev edit: keep removal of DB pool params and `DefaultMaxWorkers`; the removal of `WaitingTasks`/`DroppedTasks` needs approval (R2) or is not needed (R6 "change in place" suggests no unrelated removals; they are "never-incremented counters" per commit d25bff2, R21).
- `interfaces.go` REWRITE as a dev edit (drop dbconnpool import/params; doc fix is noise).
- `workerpool_test.go` KEEP behavior tests, adjust to the signature; `TestDefaultMaxWorkers` KEEP.

### internal/server/files (discovery files)

- `discovery_catalog.go` DROP: `NewDiscoveryDirEntMapFunc` is only used by the `walker.go` adapter; `CatalogLoaderFromStore` has no production caller (grep); `galleryDirPrefixFromReported` is a pass-through (R5, R10).
- `discovery_modification.go` DROP: aliases and pass-throughs of `discovery.*` (R5, R10); `discoveryModificationDecision`/`discoveryDirEntModifiedCore` unused outside tests.
- `discovery_work.go` DROP: type alias of `storagepayload.DiscoveryPathWork` (R5, R10); callers use the real type.
- `walker.go` DROP: adapter `WalkImageDir` mapping `files.WalkDeps` to `discovery.WalkDeps`, `CatalogROPool` fallback (R5, R10, R16: exposes `dbconnpool`). Callers are tests.
- `discovery_storage_client.go` COPY if the file processor keeps a storage interface: 2 methods, consumer-defined, one production implementer (`database.Component`) and test fakes (R8 permits an interface that keeps the consumer off the DB). It depends on `storagepayload.ProcessedFile`, which is another component's decision; revisit there.
- Tests `walker_test.go` (7) and `discovery_modification_test.go` (2) unchanged from dev; they test the `files` adapter functions that are DROPped. Move the behavior (enqueue only supported non-zero images, bounded queue, backpressure, catalog skip/enqueue, decision table) to `internal/discovery` against its API; delete the originals (R5, R26). `discovery_catalog_test.go` (deleted in v2) stays deleted.
- `processor.go`: `ProcessingStats` embedding `discovery.Counters`, `NewPoolFuncWithProcessor`, `runPoolWorkerWithProcessor` are pass-through wrappers (processor.go:22-30, 126-138): DROP in v3 (callers should use discovery directly). Rest of processor.go belongs to the processing component.

### internal/server

- `discovery_dque.go` DROP. `discoveryDQueAdapter` wraps `*storage.DiscoveryBacklog`, which already satisfies `queue.Queuer[DiscoveryPathWork]` (Dequeue, Len, IsEmpty, Enqueue, Close()). `newDiscoveryDQueAdapter` and `mapDiscoveryDequeueResult` have no production caller (only `discovery_walk_bench_test.go`, `discovery_dque_test.go`); nil checks (R20) duplicate those in `DiscoveryBacklog`. `subsystem_manager.go:127` should assign the backlog directly. Tests: `discovery_dque_test.go` (6) assert dque round-trip/close behavior already covered by `storage/discovery_test.go`; DROP.
- `trigger_discovery_test.go` (10 tests, includes the reentrancy, rebuild-once, shutdown-skips-rebuild, ctx independence and stats reset cases): KEEP the behavior intent; they go through `App.TriggerDiscovery` and `testSeams`.

### internal/storagepayload

- `discovery.go` COPY (`DiscoveryPathWork`, wire format pinned by a fixture; checked R9, R14, R16, R22, R23; exported identifiers documented). `Path []byte` plus `Gob*` is a fixed on-disk format. One nit: doc comment cites `parallelwalkdir.ReportedFile`, fine.
- `run_state.go` COPY (plain struct, used by Service and module-state store; no database types).
- `compatibility_test.go` and `testdata/pre-db02-discovery.gob` KEEP the discovery fixture round-trip; the remainder belongs to other components.

### internal/server/database/storage

- `discovery.go` REWRITE: R10 `Store` methods that only manage a cached `discoveryBacklog` field plus `DiscoveryDQueDir()` and `DefaultDiscoveryDQueDir` (duplicate path rule with `infrastructure_pool.go:86`, R6); R5 `DiscoveryBacklog()` accessor and `DiscoveryDQueDir()`; `MapDiscoveryDequeueResult` exported only for a pass-through in `server` (R5, R10); `_ = ctx` in `ResetDiscoveryBacklog` (R12/R23); `WaitWrites` belongs with the write batcher, not "discovery" (R17). Keep: wipe-then-open, 1000 items/segment, TurboOn, error mapping, idempotent Close, cleanup-error-but-item semantics and log lines.
- `catalog.go` REWRITE: R16 `sql.NullInt64` and `gallerydb` row types stay inside this package (allowed) but the output `parallelwalkdir.DirEntState` with `*Valid` booleans (R15) leaks them to `discovery`; `BuildDirEntMapFromRows` exported "for tests and compatibility callers" (R5, R9); `GalleryDirPrefixFromReported` is a pass-through of `removePrefix` (R10); `SetROPool` + `roPool()` nil-store checks (R20). Keep: per-directory query, ErrNoRows -> empty map, key = HashPathBytes(basename), invalid/file merge semantics.
- `drain.go` REWRITE/MERGE: `FinalizeIndex` belongs next to `RebuildFileFolderIndex` (folder_index component); the `ctx.Err()` precheck and nil guards (R20). Keep the `ErrFolderIndexRebuild` wrapping contract.
- Tests: `discovery_test.go` (2) KEEP round-trip (drop `TestMapDiscoveryDequeueResult_empty` if the mapper is unexported); `catalog_test.go` (5) KEEP the merge/collision cases through `CatalogDirectory` rather than the unexported builder (R26); `catalog_integration_test.go` KEEP; `drain_test.go` DROP (nil-store guard only); `finalize_index_integration_test.go` KEEP (documents unavailable-resources error).

## 5. Boundary notes

`go list` (v2 checkout):

- `discovery` imports: gallerydb, parallelwalkdir, queue, storagepayload, thumbnail, workerpool. Imported by: server, server/files. It is not an import-cycle breaker; it holds logic moved out of `files`/`server` (R11 not triggered). It violates R16 (imports gallerydb, database/sql via worker.go). Interface `Backlog` duplicates `queue.Queuer`/`Dequeuer`.
- `parallelwalkdir` imports: gensyncpool, osza, queue. Imported by: discovery, server, server/database, server/database/storage, server/files. Owns walking; `storage` imports it only for `DirEntState`/`PathHash`/`GetDirEntMapFunc`.
- `workerpool` imports: nothing internal (v2) (was `dbconnpool` in dev). Imported by: discovery, server, server/files, server/metrics.
- `storagepayload` imports: gallerydb, gensyncpool. Imported by: cachelite, discovery, server/database, server/database/storage, server/files, server/modulestate. Exists in v2 to share payload types between `files` (producer) and `storage` (consumer) without a cycle (R11 candidate: it is a payload-types package whose purpose is to let `files`, `discovery` and `storage` talk without importing each other). Its discovery part (`DiscoveryPathWork`, `FileProcessingRunState`) could instead live in `discovery`, the owner of the concept; `storage` would then import `discovery`, which imports gallerydb (see below) so the dependency direction must be checked.
- `server/database/storage` imports: bodycodec, dbconnpool, dque, gallerydb, gallerylib, parallelwalkdir, queue, storagepayload, tableswap, thumbnail, writebatcher. Imported by: cachelite, server, server/database, server/files, server/modulestate. For discovery it owns the dque backlog and the catalog SQL.
- `server/files` imports `discovery` (for `Counters`, `Processor`, `NewPoolFunc`, `WorkerProcessorFactory`) and `storage`; `discovery` must not import `files` (hence `Processor` is defined in `discovery`, files satisfies it).

Proposals (need approval where marked):

1. Do not create `internal/storagepayload` for discovery types (R1). Move `DiscoveryPathWork` into `discovery` (wire format kept) and `FileProcessingRunState` next to the module-state store or in `discovery`. (Needs a decision with the storage/processing specs.)
2. Keep `internal/discovery` as a new package only if approved (R1): it owns walk, workers, run state and counters; today those are split between `files`, `server` and `app`. Alternative: leave them in `files`, but then `files` keeps a 1000-line mix. Recommend a new package `discovery` with a small, concrete API: `Service{...}.Run/Hydrate/Close`, taking a `*storage.DiscoveryBacklog`-like queue interface and a `Processor` interface.
3. Move the module-state calls (`SetActive`, `SaveFileProcessing`, `LoadFileProcessing`) behind one small `ModuleState` consumer interface (already so), implemented by the storage store.
4. Remove `files.WalkDeps/WalkImageDir`, `files.ProcessingStats`, `files.DiscoveryPathWork`, aliases; callers use `discovery` directly. In dev these are exported `files` APIs, so removal needs R2 approval (it is a removal of dev exports).
5. Put `FinalizeIndex` with the folder index code and `WaitWrites` with the write batcher; storage should not have a "discovery.go" that mixes the backlog, the path default and the write wait.
6. Let `workerpool.StartWorkerPool` use `errgroup` directly and drop `time.Sleep` stagger? (Do not change behavior without approval: the stagger is observable timing.) Leave as is.

## 6. Open questions (with recommendations)

1. Approve new package `internal/discovery` (R1)? Recommend YES: it is the owner of the moved logic and avoids growing `server`/`files`. Alternative is `files`.
2. Approve removal of `files.WalkImageDir`, `WalkDeps`, `ProcessingStats`, `DiscoveryPathWork` (dev exports, R2)? Recommend YES with the caveat that `DiscoveryPathWork` moves to `discovery`; no compatibility alias (R5).
3. Approve the workerpool API change (`PoolFunc`/`MonitorPool`/`StartWorkerPool` signatures, `DefaultMaxWorkers`) (R2)? Recommend YES for the DB-pool param removal and `DefaultMaxWorkers`; for the removed `WaitingTasks/DroppedTasks` recommend YES only if the dashboard does not read them (UNVERIFIED; grep `waiting_tasks`/`WaitingTasks` consumers before deciding), else keep.
4. Is `storagepayload` allowed as a package (R1, R11)? Recommend NO for the discovery types (see 5.1); defer to the processing/storage specs for the others.
5. Which unapproved changes in section 2 are accepted as intended? Recommend accept 1, 3, 4, 5 (race fixes and cancel semantics, parity-tested), accept 2 only if the dashboard counters after a refused run are acceptable, and request confirmation for 7 and 8.
6. Should the failure `reason` for a malformed JPEG be the stage string (`exif` vs `decode`) or strictly `decode`? Recommend strictly follow v2 behavior: category derived the same way, but verified by the corpus; if ExifTool rejects first the reason will be `exif`. Replace substring matching with typed sentinel errors while keeping the category strings (R12).
7. Should the `Override` whole-run seam stay? Recommend NO in `discovery`; keep the existing `testSeams.TriggerDiscovery` in `App.TriggerDiscovery` (server package already allows `*.testSeams.*`, R9 exception) and test `Service` through its real dependencies.
8. Keep polling (100 ms tickers) in `Wait`/`waitForProcessingStop`/`WaitWrites`? Recommend replace the two in-package waits by a single join (WaitGroup for senders and in-flight work, queue drained condition) while keeping `WaitWrites` polling because the batcher exposes only `PendingCount`.
