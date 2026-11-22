# Server wiring: design

Status: approved by the user on 2026-11-19, with decisions 1 to 3 accepted.

The fifth refactorv3 component. The image-stack subsystems now each have one
owner: `database.Writer` for writes, `discovery.Service` for discovery,
`files.FileProcessor` for processing, `memlimit` for memory. What remains is
how the server starts, reconfigures and stops them, the two config defaults the
spec decided, and the items earlier components deferred here:

- the reconfigure window, where the old pools close before the Writer moves
  to the new ones (write path);
- the untracked startup discovery goroutine (discovery);
- the whole-run test seam `AppTestSeams.TriggerDiscovery` and the tests that
  use it (write path, discovery);
- a `dev` test helper that reads a field with reflection (discovery);
- the `App.StartPool` forwarder (discovery).

`App`'s other forwarders to its managers are `dev`'s pattern outside the image
stack and stay.

## 1. Pools are reconfigured only before the Writer starts

Every call of `reconfigurePoolsFromConfig` is in startup or the batch-load CLI,
and each comes before `StartWriteBatcher` (`app_startup.go`, `app_cli.go`). A
config change at runtime restarts the process instead. So `Writer.Reconfigure`
never runs in production, and the reconfigure window deferred from the write
path cannot happen.

v3 makes that order the rule instead of fixing a case that does not occur:

- `InfrastructureService.ReconfigurePools` returns an error if the Writer has
  started, before touching the pools.
- `Writer.Reconfigure` goes, with the batch settings only it used (1000 writes,
  200 ms) and its tests. The Writer's pools become plain fields set by
  `NewWriter`; the atomic pointers existed only for a reconfigure while
  flushing.
- `RecreatePoolsWithConfig` keeps closing the old pools: nothing flushes on
  them yet.

The write-path finding about retrying a failed Writer restart goes with it.

## 2. Startup discovery is tracked, and its failure ends `Run`

In `dev` the startup discovery goroutine is untracked. On a folder-index rebuild
failure it calls `App.Shutdown` itself; on success it schedules the
post-discovery `PRAGMA optimize` and may request a restart. A shutdown that
starts meanwhile can close the pools under it.

v3 runs it on the runtime wait group (`RuntimeManager.wg`), which `Shutdown`
already waits for:

- On `database.ErrFolderIndexRebuild` it logs `startup folder-index rebuild
failed; shutting down` (as `dev`), records the error and cancels the app
  context. `Serve` returns, and `Run` returns the error, so the process exits 1;
  `main` then calls `Shutdown`. The goroutine never calls `Shutdown`, which
  would wait for itself.
- On success, if the app context is not canceled, it schedules the optimize
  and, with `RestartAfterDiscovery`, requests the restart, as `dev`.
- On any other error, it returns, as `dev`.

## 3. Config defaults (spec items 4 and 5, decided)

- `RestartAfterDiscovery` defaults to true: after startup discovery the process
  re-execs to return the memory used by decoding.
- `DBMinIdleConnections` defaults to 1; the config page's placeholder says 1.
- `SetupDB` takes its configured-versus-effective defaults from
  `config.DefaultConfig()` instead of the literals 100 and 10.
- `ENV_CONFIGURATION.md` states the new default for `restart_after_discovery`.
  `docs/ARCHITECTURE.md` is updated in the finish component.

## 4. Test seams and tests

- **`AppTestSeams.TriggerDiscovery`** (whole run) and
  **`OnStartupDiscoveryRebuildFailedShutdown`** go. Tests that used them run
  real discovery instead: an images directory with a few small files, so a run
  takes milliseconds, and for the failure path a real rebuild failure, made by
  dropping `file_folder_index` in the test database before `Run`.
- **Startup tests** then check: restart requested after a successful startup
  run when the flag is on and not when it is off; `Run` returns
  `ErrFolderIndexRebuild` on a failed rebuild and the app shuts down; startup
  hydrates counters only when discovery is skipped; `TriggerDiscovery` uses the
  app's context, not the caller's.
- **Reflection.** The route list `/gallery/`, `/lightbox/`, `/info/folder/`,
  `/info/image/` is written three times in the server. One function returns it,
  or the cache middleware's configured routes when the cache is on; the preload
  manager, the CLI and the cache setup use it. Tests check that function, and
  the reflective `preloadManagerRoutes` helper goes.
- **`App.StartPool`** goes; startup calls `SubsystemManager.StartPool`.

## Parity

No change to what discovery imports. `v3-parity.sh` sets
`restart-after-discovery: false` in its config, so the new default does not
restart the runs it measures. Expect the discovery component's results.

## Approvals needed

- **Exported API in `dev` packages:** removing `database.Writer.Reconfigure`
  (added and approved in the write path); removing `App.StartPool`,
  `AppTestSeams.TriggerDiscovery` and
  `AppTestSeams.OnStartupDiscoveryRebuildFailedShutdown`. Exact lines from
  `v3-check.sh` after approval.
- **No new package, no rename, no `go.mod` change.** The config defaults were
  decided in the spec.

## Decisions

1. **Make "reconfigure before the Writer starts" the rule and remove
   `Writer.Reconfigure`.** Recommended: it removes code that never runs, and with
   it the reconfigure window, instead of adding a handoff for it.
2. **A failed startup rebuild ends `Run` with its error.** Recommended: the
   process exits 1 instead of 0, and the goroutine never waits on itself. In
   `dev` the goroutine called `Shutdown` and `Run` returned nil.
3. **Test the startup paths with real discovery and a real rebuild failure.**
   Recommended over keeping the whole-run seam (R9, R26): the tests then check
   that startup actually reaches the rebuild.
