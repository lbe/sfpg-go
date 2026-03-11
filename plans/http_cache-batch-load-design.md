# HTTP Cache Batch Load Design

Date: 2026-03-11

## Summary

Add a **batch load** feature that warms the HTTP cache for **gallery**, **info box folder**, **info box image**, and **lightbox** routes. It must run from **CLI** and the **authenticated hamburger menu**, **never run while discovery is active**, and report **progress on the dashboard**. It reuses the existing cache middleware and internal-request warm path, but executes with a **bounded worker pool** rather than the preload manager’s high concurrency.

## Goals

- Warm HTTP cache for:
  - `/gallery/{id}`
  - `/info/folder/{id}`
  - `/info/image/{id}`
  - `/lightbox/{id}`
- Run from:
  - CLI (one-shot)
  - Authenticated hamburger menu (server-side trigger)
- Block when discovery is active (cross-process, via DB state)
- Provide dashboard progress reporting
- Use bounded concurrency and avoid mass fan-out
- Reuse existing internal request + cache key mechanics
- Document known limitations and add targeted tests

## Non-Goals

- No direct cache table writes (no bypass of middleware)
- No partial/full-page dual variants (HTMX-only per requirement)
- No UI for configuring worker count at this stage (defaults only)
- No scope filtering (by folder or range) in initial version

## Existing Relevant Components

- Internal request warm path:
  - `internal/server/cachepreload/internal_request.go`
- Cache key generation:
  - `internal/server/cachepreload/cache_key.go`
- Cache existence check:
  - `HttpCacheExistsByKey` in `internal/gallerydb/http_cache.sql.go`
- Preload manager and metrics patterns:
  - `internal/server/cachepreload/*`
- Dashboard metrics:
  - `internal/server/metrics/collector.go`
  - `web/templates/dashboard.html.tmpl`
- Server mgmt handlers and hamburger menu:
  - `internal/server/handlers/server_handlers.go`
  - `web/templates/hamburger-menu-items.html.tmpl`
- Application documentation:
  - `docs/ARCHITECTURE.md`

## Architecture Overview

Introduce a new **Batch Load Manager** that:

1. Enumerates target routes via a **single SQL query**.
2. Produces an internal request for each target (HTMX variant only).
3. Skips already-cached entries (lightweight `HttpCacheExistsByKey`).
4. Executes requests using a **bounded worker pool**.
5. Tracks metrics (scheduled/completed/failed/skipped, in-flight, timing).
6. Exposes metrics to the dashboard via a new collector source.

The batch load runs in-process and leverages the existing router and middleware chain, ensuring cache entries match real HTTP responses.

## Data Model: Discovery Active State (DB)

Because batch load must not run while discovery is active, and CLI runs in a separate process, we store discovery state in the DB.

### Table

Add a small table to record module state:

```
CREATE TABLE IF NOT EXISTS module_state (
  name TEXT PRIMARY KEY,
  is_active INTEGER NOT NULL,
  last_started_at INTEGER,
  last_finished_at INTEGER
);
```

### Semantics

- `name = 'discovery'`
- `is_active = 1` when discovery is running
- `is_active = 0` when discovery is idle
- `last_started_at`, `last_finished_at` are Unix seconds

### Lifecycle Updates

- When discovery starts: `is_active=1`, set `last_started_at`
- When discovery finishes: `is_active=0`, set `last_finished_at`

### Access API

Implemented in `internal/server/modulestate/`:

- `SetActive(ctx, name, active bool)` — sets active state and timestamps
- `IsActive(ctx, name) (bool, error)` — returns true if module is active (missing rows = inactive)

This will be used both by the server discovery path and by CLI/HTTP batch load to decide whether to run.

### Discovery Lifecycle Wiring (Implementation Detail)

**Problem:** `files.WalkImageDir` has no knowledge of `module_state`; `App` currently does not hold a `ModuleStateService`.

**Solution — server-side wrapper (no changes to `files` package):**

1. **Add `moduleStateService` to App**
   - Add field: `moduleStateService *modulestate.Service`
   - Construct in `database.Init` (or equivalent DB setup):  
     `app.moduleStateService = modulestate.NewService(app.dbRwPool)`
   - Must be created before discovery can run (after DB pools exist).

2. **Wrap `walkImageDir` to update module state**
   - Do **not** add callbacks to `files.WalkDeps` (avoids touching the `files` package).
   - In `app.walkImageDir()`:
     - Call `app.moduleStateService.SetActive(ctx, "discovery", true)` at start (obtain ctx via `app.getCtx()` or equivalent).
     - Use `defer app.moduleStateService.SetActive(ctx, "discovery", false)` so finish runs on all exit paths.
   - Place the `SetActive(true)` before `files.WalkImageDir`, and the deferred `SetActive(false)` so it runs when the goroutine returns (including panics).

## Batch Load Target Enumeration

HTMX-only targets with correct `HX-Target` and `gzip` encoding.

### SQL Query

`internal/gallerydb/custom.sql`

```
-- name: GetBatchLoadTargets :many
WITH cte_cache_map AS (
    SELECT 'folder' AS cache_type, '/gallery/'     AS cache_entry, 'true' AS htmx, 'gallery-content' AS hx_target, 'gzip' AS encoding
    UNION ALL
    SELECT 'folder' AS cache_type, '/info/folder/' AS cache_entry, 'true' AS htmx, 'box_info'        AS hx_target, 'gzip' AS encoding
    UNION ALL
    SELECT 'file'   AS cache_type, '/info/image/'  AS cache_entry, 'true' AS htmx, 'box_info'        AS hx_target, 'gzip' AS encoding
    UNION ALL
    SELECT 'file'   AS cache_type, '/lightbox/'    AS cache_entry, 'true' AS htmx, 'lightbox_content' AS hx_target, 'gzip' AS encoding
),
cte_folders AS (
    SELECT b.cache_entry || f.id AS path, b.htmx, b.hx_target, b.encoding
      FROM folders f
      JOIN cte_cache_map b
        ON b.cache_type = 'folder'
),
cte_files AS (
    SELECT b.cache_entry || f.id AS path, b.htmx, b.hx_target, b.encoding
      FROM files f
      JOIN cte_cache_map b
        ON b.cache_type = 'file'
),
u AS (
    SELECT path, htmx, hx_target, encoding FROM cte_folders
    UNION ALL
    SELECT path, htmx, hx_target, encoding FROM cte_files
)
SELECT path, htmx, hx_target, encoding
  FROM u
 ORDER BY
  CASE
    WHEN path LIKE '/gallery/%' THEN 0
    WHEN path LIKE '/info/folder/%' THEN 1
    WHEN path LIKE '/info/image/%' THEN 2
    WHEN path LIKE '/lightbox/%' THEN 3
    ELSE 9
  END,
  path;
```

### Output Row

- `path` (string): e.g., `/info/image/42`
- `htmx` (string): `"true"`
- `hx_target` (string): `"box_info"`, `"gallery-content"`, `"lightbox_content"`
- `encoding` (string): `"gzip"`

## Batch Load Manager Design

New package: `internal/server/cachebatch/`

### Core Types

```
type Target struct {
  Path     string
  HTMX     string // "true" or "false"
  HXTarget string
  Encoding string
}

type BatchLoadMetrics struct { /* atomic counters + timestamps */ }

type BatchLoadManager struct {
  mu        sync.Mutex
  running   atomic.Bool
  config    BatchLoadConfig
  metrics   *BatchLoadMetrics
  // deps: dbRoPool, getQueries, getHandler, getETagVersion, moduleStateStore
}
```

### Bounded Concurrency

- Implement as worker pool:
  - buffered channel `jobs` with size `queueSize`
  - `maxWorkers` goroutines
- Defaults (initial):
  - `maxWorkers = min(8, runtime.NumCPU())`
  - `queueSize = 1000`

### Database Load Considerations

- Batch load is bounded and significantly lower concurrency than cache preload.
- SQLite is in-process; read load is acceptable.
- Writes go through the existing HTTP cache write batcher, which already enforces
  batching and size limits.

### Scheduling Flow

1. **Check discovery active** via DB state.
2. **Check already running** guard.
3. **Load targets** via `GetBatchLoadTargets`.
4. **Compute cache key** for each target using `GenerateCacheKeyWithHX`.
5. **Skip cached**: use `HttpCacheExistsByKey`.
6. Enqueue remaining targets to worker pool.
7. Each worker executes `MakeInternalRequestWithVariant` using:
   - `HX-Request: true`
   - `HX-Target: hx_target`
   - `Accept-Encoding: encoding`
8. Update metrics for scheduled/completed/failed/skipped.
9. At end, record finish time and set module activity inactive.

### Cache Key Format

Use existing helper to guarantee key parity with middleware:

```
GenerateCacheKeyWithHX(
  "GET", path, "v=<etag>", "true", hxTarget, encoding,
)
```

### Internal Requests

Re-use existing internal-request helpers:

- `cachepreload.MakeInternalRequestWithVariant`
- Always set `X-SFPG-Internal-Preload` to avoid cascading preloads

## Discovery Guard (Batch Load Block)

Batch load should refuse to start if discovery is active.

### Server Process Check

`moduleStateService.IsActive(ctx, "discovery")`

- If true, return 409 and a warning toast.

### CLI Check

Same DB check (since CLI is a separate process).

- If active, exit non-zero with explanatory message.

## CLI Entry Point

Add a flag: `--cache-batch-load`

### CLI Flow

1. Parse flag in `internal/getopt/opt.go`
2. In `main.go`, before normal server startup:
   - Create app
   - `InitForBatchLoad()`
   - Run `BatchLoadManager.Run()`
   - Print summary to stdout

### InitForBatchLoad

New app init that:

- Sets root dir
- Opens DB pools
- Loads config
- Initializes HTTP cache middleware
- Builds handler chain (`getRouter`) for internal requests

No server start, no discovery start.

## UI Entry Point

Add a new server endpoint:

- `POST /server/cache-batch-load`
- Auth required
- Blocks if discovery active
- Starts batch load in goroutine
- Returns toast HTML

### Hamburger Menu

In `web/templates/hamburger-menu-items.html.tmpl`, add a menu item:

```
<button
  hx-post="/server/cache-batch-load"
  hx-target="#server-toast-container"
  hx-swap="innerHTML"
  class="text-success"
  aria-label="Run Cache Batch Load"
>
  ...icon...
  Run Cache Batch Load
</button>
```

### Toast Template

Add `web/templates/cache-batch-load-started.html.tmpl` matching discovery toast style:

- On success: “Cache batch load started”
- On blocked: “Cache batch load blocked: discovery active”

## Dashboard Progress Reporting

Extend metrics collector with a new section:

### New Metrics Type

In `internal/server/metrics/collector.go`:

```
type CacheBatchLoadMetrics struct {
  TargetsTotal      int64
  TargetsScheduled  int64
  TargetsCompleted  int64
  TargetsFailed     int64
  TargetsSkipped    int64
  InFlight          int64
  IsRunning         bool
  LastStartedAt     time.Time
  LastFinishedAt    time.Time
}
```

### New Source Interface

```
type CacheBatchLoadSource interface {
  GetMetrics() CacheBatchLoadMetrics
}
```

### Dashboard Rendering

Add a new card in `web/templates/dashboard.html.tmpl` near Cache Preload:

- Status badge: Running/Idle
- Progress: completed / total + progress bar
- Errors and skipped counts

## Module Status Integration

Record module activity in the collector:

- `RecordModuleActivity("cache_batch_load", isRunning)`

This populates the “Module Status” section in the dashboard.

## Error Handling

- If a target request returns HTTP >= 400, count as failed
- Treat HTTP 404 as failed (signals DB/route mismatch)
- Failures do not abort the run
- If DB query fails or handler chain is nil, abort run and set running=false

## Safety and Limits

- Bound concurrency to avoid memory spikes
- Skip already cached entries
- Block when discovery is active
- Do not enqueue new run when another is already running
- If HTTP cache is disabled, batch load should no-op with an explicit message

## Testing Plan

### Unit Tests

- BatchLoadTargets query returns expected paths and metadata
- BatchLoadManager skips cached entries
- BatchLoadManager respects discovery active flag
- Metrics counters and timestamps update correctly
- Batch load assumes public routes; add a guard/test verifying these routes are not
  auth-protected (or explicitly document that requirement)

### Integration Tests

- `POST /server/cache-batch-load` returns toast
- Dashboard includes new card and shows progress

## Open Follow-ups

- Future config knobs for max workers and queue size
- Optional dual-variant warming (HTMX + full) if needed
- Optional multi-theme warming if required

## Known Limitations (Documented)

- Batch load warms only the default theme because internal requests do not carry
  a theme cookie. This should be documented in `docs/ARCHITECTURE.md` as a known
  limitation of cache warm paths.

## Plan Tasks (Documentation)

- Update `docs/ARCHITECTURE.md` to document the default-theme-only cache warm limitation.
